// Package ui is the Bubble Tea front end. It only talks to providers through
// the provider interface and app.Service, so it works for any source.
package ui

import (
	"context"
	"fmt"
	"image"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"fzpm/internal/app"
	"fzpm/internal/config"
	"fzpm/internal/img"
	"fzpm/internal/provider"
	"fzpm/internal/runner"
	"fzpm/internal/search"
)

const (
	resultLimit      = 1000
	remoteMinLen     = 3
	remoteDelay      = 300 * time.Millisecond
	prefetchDelay    = 200 * time.Millisecond
	prefetchParallel = 3
)

// Options configure the UI.
type Options struct {
	Query     string
	Refresh   bool
	ImageMode img.Mode
	Dark      bool
	Dir       string
	Config    *config.Config // nil: settings are not persisted
	// Available lists all providers for the settings pane (default: all
	// registered providers).
	Available []provider.Provider
}

type screen int

const (
	screenList screen = iota
	screenDetail
)

// Model is the root Bubble Tea model.
type Model struct {
	svc      *app.Service
	provs    []provider.Provider
	projects map[string]provider.Project
	opts     Options
	ctx      context.Context
	cancel   context.CancelFunc
	events   chan tea.Msg

	index []provider.Package
	tab   tab

	// local project packages (Installed / Uninstalled tabs)
	local        []provider.LocalPackage
	localByKey   map[string]provider.LocalPackage // provider/module
	present      map[string]bool                  // provider/project -> any module enabled
	localLoading int
	detailLocal  *provider.LocalPackage // local package a detail page was opened from
	// enabledNow holds modules enabled (true) or disabled (false) in this
	// session (provider/module); project files only reflect that after a
	// config export.
	enabledNow map[string]bool
	// marked holds rows selected for a batch install/enable (key -> row).
	// Rows of the current tab only; switching tabs clears it.
	marked  map[string]provider.Package
	results []provider.Package
	cursor  int
	offset  int

	input  textinput.Model
	insert bool
	screen screen
	help   bool
	cfg    *config.Config

	settingsOpen   bool
	settingsCursor int
	pending        string // first key of a two-key sequence (gg, ]r, [r)
	width          int
	height         int
	spinner        spinner.Model
	spinning       bool

	indexing int
	// manualRefresh: index rebuild started with r
	manualRefresh bool
	searching     int
	searchSeq     int
	prefetch      int
	// prefetchGen invalidates queued prefetches when the visible rows change;
	// prefetchSem caps concurrent prefetch requests.
	prefetchGen atomic.Int64
	prefetchSem chan struct{}
	remoteDone  map[string]bool
	dirty       map[string]bool // providers whose index changed since save

	status    string
	statusErr bool

	// command modal (install/enable/disable run in a pseudo terminal)
	term         *runner.Term
	termFinished bool
	termResult   execDoneMsg // reported once the commands finish

	// detail screen
	detail        *provider.Detail
	detailFor     provider.Package
	detailErr     error
	vp            viewport.Model
	side          viewport.Model // sticky sidebar (two-column layout)
	sideW         int            // 0 when the sidebar is stacked below
	relCursor     int
	releasesLine  int
	images        map[string]image.Image
	imageFailed   map[string]bool
	showImages    bool
	kittySent     map[uint32]bool // images uploaded this session
	kittyPending  bool            // an upload was skipped while the pane was hidden
	visible       bool
	visibleAt     time.Time
	refreshing    bool
	md            *glamour.TermRenderer
	mdWidth       int
	renderedBody  string
	renderedWidth int
}

// New builds the model. Providers detect their local project from opts.Dir.
func New(svc *app.Service, provs []provider.Provider, opts Options) *Model {
	ti := textinput.New()
	ti.Prompt = "❯ "
	ti.Placeholder = "search " + providerNames(provs) + "…"
	ti.SetValue(opts.Query)
	ti.Focus()

	ctx, cancel := context.WithCancel(context.Background())
	m := &Model{
		svc:         svc,
		provs:       provs,
		projects:    map[string]provider.Project{},
		opts:        opts,
		ctx:         ctx,
		cancel:      cancel,
		events:      make(chan tea.Msg, 64),
		input:       ti,
		insert:      true,
		spinner:     spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(accent)),
		remoteDone:  map[string]bool{},
		dirty:       map[string]bool{},
		images:      map[string]image.Image{},
		imageFailed: map[string]bool{},
		showImages:  opts.ImageMode != img.Off,
		kittySent:   map[uint32]bool{},
		enabledNow:  map[string]bool{},
		marked:      map[string]provider.Package{},
		prefetchSem: make(chan struct{}, prefetchParallel),
		relCursor:   -1,
		width:       80,
		height:      24,
	}
	for _, p := range provs {
		if proj, ok := p.Detect(opts.Dir); ok {
			m.projects[p.Name()] = proj
		}
	}
	m.cfg = opts.Config
	if m.cfg == nil {
		m.cfg = &config.Config{}
	}
	return m
}

func providerNames(provs []provider.Provider) string {
	names := make([]string, len(provs))
	for i, p := range provs {
		names[i] = p.Name()
	}
	return strings.Join(names, ", ")
}

// Messages.
type (
	indexBatchMsg struct {
		prov string
		pkgs []provider.Package
	}
	indexDoneMsg struct {
		prov string
		err  error
	}
	remoteTickMsg struct {
		seq   int
		query string
	}
	remoteMsg struct {
		query string
		pkgs  []provider.Package
		err   error
	}
	prefetchTickMsg struct{ seq int }
	detailMsg       struct {
		pkg       provider.Package
		d         *provider.Detail
		err       error
		stale     bool // served from cache but older than the TTL
		refreshed bool // result of a (background or manual) refresh
	}
	imageMsg struct {
		url string
		img image.Image // nil when it could not be loaded
	}
	execDoneMsg struct {
		label string
		what  string // package names, for the status line
		err   error
		// enabled / disabled list modules turned on / off by this action
		// (ID = local module).
		enabled  []provider.Package
		disabled []provider.Package
		batch    bool // ran for the marked rows
	}
	statusMsg struct {
		text string
		err  bool
	}
)

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink}
	for _, p := range m.provs {
		cached, fresh := m.svc.LoadIndex(p)
		m.index, _ = search.Merge(m.index, cached)
		if !fresh || m.opts.Refresh {
			m.indexing++
			go m.runIndex(p)
		}
	}
	m.refilter(true)
	if m.indexing > 0 {
		cmds = append(cmds, m.waitEvent(), m.spin())
	}
	cmds = append(cmds, m.queryChanged(), m.loadLocal())
	return tea.Batch(cmds...)
}

// refreshList reloads the current tab: on Search it rebuilds every active
// provider's index from the source (like --refresh); on the local tabs it
// re-reads the project.
func (m *Model) refreshList() tea.Cmd {
	if m.tab != tabSearch {
		m.setStatus("reloading project modules…", false)
		return m.loadLocal()
	}
	if m.indexing > 0 {
		m.setStatus("index is already being updated", false)
		return nil
	}
	m.manualRefresh = true
	m.remoteDone = map[string]bool{} // let remote searches run again
	for _, p := range m.provs {
		m.indexing++
		go m.runIndex(p)
	}
	m.setStatus("refreshing index from "+providerNames(m.provs)+"…", false)
	return tea.Batch(m.waitEvent(), m.spin())
}

// runIndex streams a provider's index into the event channel.
func (m *Model) runIndex(p provider.Provider) {
	send := func(msg tea.Msg) {
		select {
		case m.events <- msg:
		case <-m.ctx.Done():
		}
	}
	err := p.Index(m.ctx, m.svc.HTTP, func(pkgs []provider.Package) {
		send(indexBatchMsg{p.Name(), pkgs})
	})
	send(indexDoneMsg{p.Name(), err})
}

func (m *Model) waitEvent() tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-m.events:
			return msg
		case <-m.ctx.Done():
			return nil
		}
	}
}

func (m *Model) busy() bool {
	return (m.term != nil && !m.termFinished) || m.indexing > 0 || m.searching > 0 || m.localLoading > 0 ||
		(m.screen == screenDetail && (m.refreshing || (m.detail == nil && m.detailErr == nil)))
}

func (m *Model) spin() tea.Cmd {
	if m.spinning {
		return nil
	}
	m.spinning = true
	return m.spinner.Tick
}

func (m *Model) setStatus(text string, isErr bool) {
	m.status, m.statusErr = text, isErr
}

// refilter recomputes results; keepSelection tries to keep the cursor on
// the same package (used when new data streams in).
func (m *Model) refilter(keepSelection bool) {
	var sel string
	if keepSelection && m.cursor < len(m.results) {
		sel = key(m.results[m.cursor])
	}
	m.results = search.Rank(m.input.Value(), m.tabSource(), resultLimit)
	m.cursor = 0
	if sel != "" {
		for i, p := range m.results {
			if key(p) == sel {
				m.cursor = i
				break
			}
		}
	}
	m.clampList()
}

func key(p provider.Package) string { return p.Provider + "/" + p.ID }

func (m *Model) provider(name string) provider.Provider {
	for _, p := range m.provs {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// selected returns the package under the cursor. On local tabs that is the
// upstream package (e.g. the drupal.org project); custom code has none.
func (m *Model) selected() (provider.Package, bool) {
	if m.screen == screenDetail {
		return m.detailFor, true
	}
	if m.cursor >= len(m.results) {
		return provider.Package{}, false
	}
	if m.tab != tabSearch {
		lp, ok := m.localByKey[key(m.results[m.cursor])]
		if !ok || lp.ID == "" {
			return provider.Package{}, false
		}
		return lp.Package, true
	}
	return m.results[m.cursor], true
}

// openSelected opens the detail page of the row under the cursor.
func (m *Model) openSelected() tea.Cmd {
	p, ok := m.selected()
	if !ok {
		if lp, isLocal := m.selectedLocal(); isLocal {
			m.setStatus(lp.Module+" is custom code; it has no page upstream", false)
		}
		return nil
	}
	lp, isLocal := m.selectedLocal()
	cmd := m.openDetail(p)
	if isLocal {
		m.detailLocal = &lp
	}
	return cmd
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = max(10, min(m.width/2, m.width-lipgloss.Width(m.projectLabel())-4))
		m.clampList()
		if m.term != nil {
			m.term.Resize(m.termSize())
		}
		if m.screen == screenDetail {
			return m, m.rebuildDetail(false)
		}
		return m, nil

	case spinner.TickMsg:
		if !m.busy() {
			m.spinning = false
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case indexBatchMsg:
		if m.provider(msg.prov) == nil { // disabled while indexing
			return m, m.waitEvent()
		}
		var added int
		m.index, added = search.Merge(m.index, msg.pkgs)
		m.dirty[msg.prov] = true
		if added > 0 || len(msg.pkgs) > 0 {
			m.refilter(true)
		}
		return m, m.waitEvent()

	case indexDoneMsg:
		m.indexing--
		if msg.err != nil && m.ctx.Err() == nil {
			m.setStatus(fmt.Sprintf("%s index: %v", msg.prov, msg.err), true)
		} else if m.manualRefresh && m.indexing == 0 {
			m.setStatus(fmt.Sprintf("✓ index updated (%d packages)", len(m.index)), false)
		}
		m.saveIndex(msg.prov)
		if m.indexing > 0 {
			return m, m.waitEvent()
		}
		m.manualRefresh = false
		return m, nil

	case localMsg:
		m.applyLocal(msg)
		return m, nil

	case remoteTickMsg:
		if msg.seq != m.searchSeq || m.tab != tabSearch {
			return m, nil
		}
		return m, m.remoteSearch(msg.query)

	case remoteMsg:
		m.searching--
		if msg.err != nil {
			m.setStatus("search: "+msg.err.Error(), true)
			return m, nil
		}
		var added int
		m.index, added = search.Merge(m.index, msg.pkgs)
		for _, p := range msg.pkgs {
			m.dirty[p.Provider] = true
		}
		if added > 0 && msg.query == m.input.Value() {
			m.refilter(true)
		}
		return m, nil

	case prefetchTickMsg:
		if msg.seq != m.prefetch || m.screen != screenList || m.tab != tabSearch {
			return m, nil
		}
		return m, m.prefetchVisible()

	case detailMsg:
		if m.screen != screenDetail || key(msg.pkg) != key(m.detailFor) {
			return m, nil
		}
		if msg.refreshed {
			m.refreshing = false
			if msg.err != nil {
				if m.detail != nil { // keep showing the cached page
					m.setStatus("refresh failed: "+msg.err.Error(), true)
					return m, m.rebuildDetail(false)
				}
				m.detailErr = msg.err
				return m, m.rebuildDetail(false)
			}
			m.detail, m.detailErr = msg.d, nil
			m.renderedBody = ""
			m.setStatus("updated from "+msg.pkg.Provider, false)
			return m, tea.Batch(m.fetchDetailImages(), m.rebuildDetail(false))
		}
		m.detail, m.detailErr = msg.d, msg.err
		cmds := []tea.Cmd{m.fetchDetailImages(), m.rebuildDetail(true)}
		if msg.stale {
			cmds = append(cmds, m.refreshDetail(msg.pkg))
		}
		return m, tea.Batch(cmds...)

	case imageMsg:
		if msg.img == nil {
			m.imageFailed[msg.url] = true
		} else {
			m.images[msg.url] = msg.img
		}
		if m.screen == screenDetail {
			return m, m.rebuildDetail(false)
		}
		return m, nil

	case termUpdateMsg:
		if msg.t != m.term {
			return m, nil
		}
		return m, m.waitTerm()

	case termDoneMsg:
		if msg.t != m.term {
			return m, nil
		}
		m.termFinished = true
		m.termResult.err = msg.err
		cmds := []tea.Cmd{m.finishExec(m.termResult)}
		if msg.err == nil {
			t := m.term
			cmds = append(cmds, tea.Tick(termCloseDelay, func(time.Time) tea.Msg { return termCloseMsg{t} }))
		}
		return m, tea.Batch(cmds...)

	case termCloseMsg:
		if msg.t == m.term {
			m.closeTerm()
		}
		return m, nil

	case execDoneMsg:
		return m, m.finishExec(msg)

	case statusMsg:
		m.setStatus(msg.text, msg.err)
		return m, nil

	case tea.FocusMsg:
		return m, m.retryKitty()

	case tea.KeyMsg:
		// A key press means the pane is visible: send skipped kitty uploads.
		retry := m.retryKitty()
		model, cmd := m.handleKey(msg)
		return model, tea.Batch(retry, cmd)
	}
	return m, nil
}

// retryKitty re-renders the detail page if a kitty upload was skipped while
// the pane was hidden.
func (m *Model) retryKitty() tea.Cmd {
	if !m.kittyPending || m.screen != screenDetail {
		return nil
	}
	m.kittyPending = false
	m.visibleAt = time.Time{}
	return m.rebuildDetail(false)
}

func (m *Model) saveIndex(prov string) {
	if !m.dirty[prov] {
		return
	}
	if p := m.provider(prov); p != nil {
		if err := m.svc.SaveIndex(p, m.index); err == nil {
			delete(m.dirty, prov)
		}
	}
}

// Quit saves dirty indexes and stops background work.
func (m *Model) quit() (tea.Model, tea.Cmd) {
	if m.term != nil {
		m.closeTerm()
	}
	for prov := range m.dirty {
		m.saveIndex(prov)
	}
	m.cancel()
	return m, tea.Quit
}

// queryChanged refilters and schedules the debounced remote search.
func (m *Model) queryChanged() tea.Cmd {
	m.refilter(false)
	m.searchSeq++
	seq, q := m.searchSeq, strings.TrimSpace(m.input.Value())
	if len([]rune(q)) < remoteMinLen || m.remoteDone[q] {
		return m.schedulePrefetch()
	}
	return tea.Batch(m.schedulePrefetch(), tea.Tick(remoteDelay, func(time.Time) tea.Msg {
		return remoteTickMsg{seq, q}
	}))
}

func (m *Model) remoteSearch(q string) tea.Cmd {
	m.remoteDone[q] = true
	var cmds []tea.Cmd
	for _, p := range m.provs {
		m.searching++
		cmds = append(cmds, func() tea.Msg {
			pkgs, err := m.svc.Search(m.ctx, p, q)
			return remoteMsg{q, pkgs, err}
		})
	}
	return tea.Batch(append(cmds, m.spin())...)
}

func (m *Model) schedulePrefetch() tea.Cmd {
	m.prefetch++
	seq := m.prefetch
	return tea.Tick(prefetchDelay, func(time.Time) tea.Msg { return prefetchTickMsg{seq} })
}

func (m *Model) fetchDetail(p provider.Package) tea.Cmd {
	prov := m.provider(p.Provider)
	if prov == nil {
		return nil
	}
	return func() tea.Msg {
		d, stale, err := m.svc.Detail(m.ctx, prov, p.ID)
		return detailMsg{pkg: p, d: d, err: err, stale: stale}
	}
}

// refreshDetail re-fetches a page bypassing the cache.
func (m *Model) refreshDetail(p provider.Package) tea.Cmd {
	prov := m.provider(p.Provider)
	if prov == nil || m.refreshing {
		return nil
	}
	m.refreshing = true
	return tea.Batch(m.spin(), func() tea.Msg {
		d, err := m.svc.RefreshDetail(m.ctx, prov, p.ID)
		return detailMsg{pkg: p, d: d, err: err, refreshed: true}
	})
}

// prefetchVisible warms the cache for all rows on screen (selected row
// first, including its images). Queued prefetches are dropped once the
// visible rows change.
func (m *Model) prefetchVisible() tea.Cmd {
	gen := m.prefetchGen.Add(1)
	var cmds []tea.Cmd
	add := func(p provider.Package, withImages bool) {
		prov := m.provider(p.Provider)
		if prov == nil {
			return
		}
		cmds = append(cmds, func() tea.Msg {
			select {
			case m.prefetchSem <- struct{}{}:
			case <-m.ctx.Done():
				return nil
			}
			defer func() { <-m.prefetchSem }()
			if !withImages && m.prefetchGen.Load() != gen {
				return nil
			}
			d, stale, err := m.svc.Detail(m.ctx, prov, p.ID)
			if err == nil && stale {
				d, err = m.svc.RefreshDetail(m.ctx, prov, p.ID)
			}
			if err != nil || !withImages || !m.showImages {
				return nil
			}
			for _, u := range d.Images {
				m.svc.Image(m.ctx, u)
			}
			return nil
		})
	}
	if p, ok := m.selected(); ok {
		add(p, true)
	}
	for i := m.offset; i < min(len(m.results), m.offset+m.listHeight()); i++ {
		if i != m.cursor {
			add(m.results[i], false)
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) fetchImage(url string) tea.Cmd {
	if _, ok := m.images[url]; ok {
		return nil
	}
	return func() tea.Msg {
		im, err := m.svc.Image(m.ctx, url)
		if err != nil {
			return imageMsg{url: url}
		}
		return imageMsg{url, im}
	}
}

func (m *Model) fetchDetailImages() tea.Cmd {
	if m.detail == nil || !m.showImages {
		return nil
	}
	var cmds []tea.Cmd
	for _, u := range append(m.sidebarImages(), m.detail.Images...) {
		cmds = append(cmds, m.fetchImage(u))
	}
	return tea.Batch(cmds...)
}

func (m *Model) openDetail(p provider.Package) tea.Cmd {
	m.screen = screenDetail
	m.detailFor = p
	m.detail, m.detailErr = nil, nil
	m.relCursor = -1
	m.renderedBody = ""
	m.refreshing = false
	m.detailLocal = nil
	m.vp = viewport.New(m.width, m.height-1)
	m.side = viewport.New(sidebarWidth, m.height-1)
	return tea.Batch(m.fetchDetail(p), m.spin(), m.rebuildDetail(true))
}

// runAction executes action n (0 = install, 1 = install + enable) for the
// marked rows, or else the selected package.
func (m *Model) runAction(n int) tea.Cmd {
	if m.screen == screenList && len(m.markedRows()) > 0 {
		return m.runMarked(n)
	}
	if cmd, handled := m.localAction(); handled {
		return cmd
	}
	pkg, ok := m.selected()
	if !ok {
		return nil
	}
	prov := m.provider(pkg.Provider)
	actions := prov.Actions()
	if n >= len(actions) {
		m.setStatus(fmt.Sprintf("%s has no action %d", prov.Name(), n+1), true)
		return nil
	}
	a := actions[n]
	cmds, err := a.Commands([]provider.Package{pkg}, m.selectedConstraint(), m.projects[prov.Name()])
	if err != nil {
		m.setStatus(err.Error(), true)
		return nil
	}
	var enabled []provider.Package
	if a.Enables {
		enabled = []provider.Package{pkg}
	}
	return m.execute(cmds, []provider.Package{pkg}, execDoneMsg{label: a.Label, enabled: enabled})
}

// runMarked runs action n for all marked rows: on Search one batched
// action per provider, on the local tabs enabling the uninstalled ones.
func (m *Model) runMarked(n int) tea.Cmd {
	rows := m.markedRows()
	if m.tab != tabSearch {
		var lps []provider.LocalPackage
		for _, r := range rows {
			if lp, ok := m.localByKey[key(r)]; ok && !lp.Enabled {
				lps = append(lps, lp)
			}
		}
		if len(lps) == 0 {
			m.setStatus("the selected modules are already installed", false)
			return nil
		}
		return m.enableLocal(lps, true)
	}
	var all []*exec.Cmd
	var enabled []provider.Package
	label := ""
	for _, prov := range m.provs {
		var group []provider.Package
		for _, r := range rows {
			if r.Provider == prov.Name() {
				group = append(group, r)
			}
		}
		if len(group) == 0 {
			continue
		}
		actions := prov.Actions()
		if n >= len(actions) {
			m.setStatus(fmt.Sprintf("%s has no action %d", prov.Name(), n+1), true)
			return nil
		}
		a := actions[n]
		cmds, err := a.Commands(group, "", m.projects[prov.Name()])
		if err != nil {
			m.setStatus(err.Error(), true)
			return nil
		}
		all = append(all, cmds...)
		label = a.Label
		if a.Enables {
			enabled = append(enabled, group...)
		}
	}
	return m.execute(all, rows, execDoneMsg{label: label, enabled: enabled, batch: true})
}

// execute runs cmds for pkgs in the command modal and reports back with
// done (what and err filled in). Without pseudo terminals it hands the whole
// terminal to the commands instead.
func (m *Model) execute(cmds []*exec.Cmd, pkgs []provider.Package, done execDoneMsg) tea.Cmd {
	done.what = pkgNames(pkgs)
	m.setStatus(done.label+" "+done.what+"…", false)
	w, h := m.termSize()
	t, err := startTerm(cmds, w, h)
	if err != nil {
		return tea.Exec(&runner.Seq{Cmds: cmds}, func(err error) tea.Msg {
			done.err = err
			return done
		})
	}
	m.term, m.termFinished, m.termResult = t, false, done
	return tea.Batch(m.waitTerm(), m.spin())
}

// finishExec records the result of an action and re-reads the project.
func (m *Model) finishExec(msg execDoneMsg) tea.Cmd {
	if msg.err != nil {
		m.setStatus(fmt.Sprintf("✗ %s %s failed: %v", msg.label, msg.what, msg.err), true)
		return m.loadLocal()
	}
	m.setStatus(fmt.Sprintf("✓ %s: %s", msg.label, msg.what), false)
	for _, p := range msg.enabled {
		m.enabledNow[key(p)] = true
	}
	for _, p := range msg.disabled {
		m.enabledNow[key(p)] = false
	}
	if msg.batch { // keep the selection on failure so it can be retried
		clear(m.marked)
	}
	return m.loadLocal()
}

func pkgNames(pkgs []provider.Package) string {
	ids := make([]string, len(pkgs))
	for i, p := range pkgs {
		ids[i] = p.ID
	}
	return strings.Join(ids, ", ")
}

// toggleMark selects/deselects the row under the cursor for a batch action,
// optionally moving down a line.
func (m *Model) toggleMark(down bool) tea.Cmd {
	if m.cursor >= len(m.results) {
		return nil
	}
	p := m.results[m.cursor]
	k := key(p)
	if lp, ok := m.localByKey[k]; ok && m.tab != tabSearch && lp.Enabled {
		m.setStatus(lp.Module+" is already installed", false)
		return nil
	}
	if _, ok := m.marked[k]; ok {
		delete(m.marked, k)
	} else {
		m.marked[k] = p
	}
	if !down {
		return nil
	}
	m.move(1)
	return m.schedulePrefetch()
}

// markedRows returns the marked rows of active providers, sorted.
func (m *Model) markedRows() []provider.Package {
	rows := make([]provider.Package, 0, len(m.marked))
	for _, p := range m.marked {
		if m.provider(p.Provider) != nil {
			rows = append(rows, p)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return key(rows[i]) < key(rows[j]) })
	return rows
}

func (m *Model) selectedConstraint() string {
	if m.screen == screenDetail && m.detail != nil && m.relCursor >= 0 && m.relCursor < len(m.detail.Releases) {
		return m.detail.Releases[m.relCursor].Constraint
	}
	return ""
}

func (m *Model) yank() tea.Cmd {
	pkg, ok := m.selected()
	if !ok {
		return nil
	}
	text := m.provider(pkg.Provider).InstallHint(pkg, m.selectedConstraint())
	return func() tea.Msg {
		if err := copyToClipboard(text); err != nil {
			return statusMsg{"yank: " + err.Error(), true}
		}
		return statusMsg{"yanked: " + text, false}
	}
}

func (m *Model) openBrowser() tea.Cmd {
	pkg, ok := m.selected()
	if !ok {
		return nil
	}
	url := ""
	if m.screen == screenDetail && m.detail != nil {
		url = m.detail.URL
	}
	return func() tea.Msg {
		if url == "" {
			d, _, err := m.svc.Detail(m.ctx, m.provider(pkg.Provider), pkg.ID)
			if err != nil {
				return statusMsg{err.Error(), true}
			}
			url = d.URL
		}
		if err := openURL(url); err != nil {
			return statusMsg{"open: " + err.Error(), true}
		}
		return statusMsg{"opened " + url, false}
	}
}

// writeTerminal writes raw escape sequences (kitty uploads). os.File writes
// are serialised, so this does not interleave with the renderer's frames.
func writeTerminal(b []byte) tea.Cmd {
	return func() tea.Msg {
		os.Stdout.Write(b)
		return nil
	}
}
