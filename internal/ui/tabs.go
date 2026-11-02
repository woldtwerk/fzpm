package ui

import (
	"fmt"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"fzpm/internal/provider"
)

type tab int

const (
	tabSearch tab = iota
	tabInstalled
	tabUninstalled
)

var tabNames = [...]string{"Search", "Installed", "Uninstalled"}

type localMsg struct {
	prov string
	list []provider.LocalPackage
	err  error
}

// listers returns providers that can list the detected local project.
func (m *Model) listers() []provider.Provider {
	var out []provider.Provider
	for _, p := range m.provs {
		if _, ok := p.(provider.Lister); ok {
			if _, ok := m.projects[p.Name()]; ok {
				out = append(out, p)
			}
		}
	}
	return out
}

func (m *Model) hasTabs() bool { return len(m.listers()) > 0 }

// loadLocal (re)reads the local project's packages in the background.
func (m *Model) loadLocal() tea.Cmd {
	var cmds []tea.Cmd
	for _, p := range m.listers() {
		m.localLoading++
		lister, proj := p.(provider.Lister), m.projects[p.Name()]
		cmds = append(cmds, func() tea.Msg {
			list, err := lister.Local(m.ctx, m.svc.HTTP, proj)
			return localMsg{p.Name(), list, err}
		})
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(append(cmds, m.spin())...)
}

func (m *Model) applyLocal(msg localMsg) {
	m.localLoading--
	if msg.err != nil {
		if m.ctx.Err() == nil {
			m.setStatus(msg.prov+": "+msg.err.Error(), true)
		}
		return
	}
	kept := m.local[:0]
	for _, lp := range m.local {
		if lp.Provider != msg.prov {
			kept = append(kept, lp)
		}
	}
	if m.provider(msg.prov) == nil { // disabled meanwhile
		return
	}
	m.local = append(kept, msg.list...)
	for i, lp := range m.local {
		if m.enabledNow[lp.Provider+"/"+lp.Module] {
			m.local[i].Enabled = true
		}
	}
	m.rebuildLocalMaps()
	if m.tab != tabSearch {
		m.refilter(true)
	}
}

// rebuildLocalMaps derives the lookup maps from m.local.
func (m *Model) rebuildLocalMaps() {
	m.localByKey = map[string]provider.LocalPackage{}
	m.present = map[string]bool{}
	for _, lp := range m.local {
		m.localByKey[lp.Provider+"/"+lp.Module] = lp
		if lp.ID != "" {
			k := lp.Provider + "/" + lp.ID
			m.present[k] = m.present[k] || lp.Enabled
		}
	}
}

// tabSource is what the fuzzy search runs over in the current tab. Local
// rows use the module name as ID so they stay unique.
func (m *Model) tabSource() []provider.Package {
	if m.tab == tabSearch {
		return m.index
	}
	want := m.tab == tabInstalled
	out := make([]provider.Package, 0, len(m.local))
	for _, lp := range m.local {
		if lp.Enabled == want {
			out = append(out, provider.Package{Provider: lp.Provider, ID: lp.Module, Name: lp.Name})
		}
	}
	return out
}

func (m *Model) count(t tab) int {
	n := 0
	for _, lp := range m.local {
		if lp.Enabled == (t == tabInstalled) {
			n++
		}
	}
	return n
}

func (m *Model) setTab(t tab) tea.Cmd {
	if !m.hasTabs() || t == m.tab {
		return nil
	}
	m.tab = t
	m.cursor, m.offset = 0, 0
	clear(m.marked) // marks are per tab (search rows vs. local modules)
	m.updatePlaceholder()
	m.refilter(false)
	return m.schedulePrefetch()
}

func (m *Model) updatePlaceholder() {
	m.input.Placeholder = map[tab]string{
		tabSearch:      "search " + providerNames(m.provs) + "…",
		tabInstalled:   "filter installed modules…",
		tabUninstalled: "filter uninstalled modules…",
	}[m.tab]
}

func (m *Model) cycleTab(d int) tea.Cmd {
	return m.setTab(tab((int(m.tab) + d + len(tabNames)) % len(tabNames)))
}

// selectedLocal returns the local package under the cursor (local tabs) or
// the one a detail page was opened from.
func (m *Model) selectedLocal() (provider.LocalPackage, bool) {
	if m.screen == screenDetail {
		if m.detailLocal != nil {
			return *m.detailLocal, true
		}
		return provider.LocalPackage{}, false
	}
	if m.tab == tabSearch || m.cursor >= len(m.results) {
		return provider.LocalPackage{}, false
	}
	lp, ok := m.localByKey[key(m.results[m.cursor])]
	return lp, ok
}

// localAction handles i/I for local packages: enabling uninstalled ones.
func (m *Model) localAction() (tea.Cmd, bool) {
	lp, ok := m.selectedLocal()
	if !ok {
		return nil, false
	}
	if lp.Enabled {
		m.setStatus(lp.Module+" is already installed", false)
		return nil, true
	}
	return m.enableLocal([]provider.LocalPackage{lp}, false), true
}

// enableLocal enables present packages, one batched command per provider.
func (m *Model) enableLocal(lps []provider.LocalPackage, batch bool) tea.Cmd {
	var all []*exec.Cmd
	var pkgs []provider.Package
	for _, prov := range m.provs {
		lister, ok := prov.(provider.Lister)
		if !ok {
			continue
		}
		var group []provider.LocalPackage
		for _, lp := range lps {
			if lp.Provider == prov.Name() {
				group = append(group, lp)
				pkgs = append(pkgs, provider.Package{Provider: lp.Provider, ID: lp.Module})
			}
		}
		if len(group) == 0 {
			continue
		}
		cmds, err := lister.EnableCommands(group, m.projects[prov.Name()])
		if err != nil {
			m.setStatus(err.Error(), true)
			return nil
		}
		all = append(all, cmds...)
	}
	return m.execute("enable", all, pkgs, pkgs, batch)
}

func (m *Model) tabBar() string {
	var parts []string
	for i, name := range tabNames {
		t := tab(i)
		label := name
		if t != tabSearch {
			label = fmt.Sprintf("%s %d", name, m.count(t))
		}
		label = fmt.Sprintf(" %d %s ", i+1, label)
		if t == m.tab {
			parts = append(parts, tabActive.Render(label))
		} else {
			parts = append(parts, tabInactive.Render(label))
		}
	}
	return strings.Join(parts, " ")
}
