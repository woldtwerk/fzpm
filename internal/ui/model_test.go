package ui

import (
	"context"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"fzpm/internal/app"
	"fzpm/internal/cache"
	"fzpm/internal/config"
	"fzpm/internal/img"
	"fzpm/internal/provider"
)

// fake is an in-memory provider proving the UI is source-agnostic.
type fake struct{}

var fakePkgs = []provider.Package{
	{Provider: "fake", ID: "left-pad", Name: "left-pad", Popularity: 1000, Summary: "pads left"},
	{Provider: "fake", ID: "lodash", Name: "lodash", Popularity: 9000, Summary: "utilities"},
}

func (fake) Name() string { return "fake" }
func (fake) Index(_ context.Context, _ *http.Client, emit func([]provider.Package)) error {
	emit(fakePkgs)
	return nil
}
func (fake) Search(context.Context, *http.Client, string) ([]provider.Package, error) {
	return []provider.Package{{Provider: "fake", ID: "longtail", Name: "longtail"}}, nil
}
func (fake) Detail(_ context.Context, _ *http.Client, id string) (*provider.Detail, error) {
	return &provider.Detail{
		Package:  provider.Package{Provider: "fake", ID: id, Name: id},
		Body:     "# Hello\n\nbody text",
		Releases: []provider.Release{{Version: "2.0.0", Constraint: "^2.0"}, {Version: "1.0.0", Constraint: "^1.0"}},
		Sidebar:  []provider.Section{{Title: "Maintainers", Items: []provider.Link{{Label: "alice", URL: "https://example.com/alice"}}}},
	}, nil
}
func (fake) Detect(string) (provider.Project, bool) {
	return provider.Project{Root: "/tmp", Label: "fake: /tmp"}, true
}
func (fake) Actions() []provider.Action {
	return []provider.Action{{Label: "install", Commands: func(p provider.Package, v string, _ provider.Project) ([]*exec.Cmd, error) {
		return []*exec.Cmd{exec.Command("true", p.ID, v)}, nil
	}}}
}
func (fake) InstallHint(p provider.Package, v string) string { return "fake add " + p.ID }

func press(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// drain runs a command (and nested batches) synchronously, feeding messages
// back into the model. Ticks and blocking waits are skipped.
func drain(t *testing.T, m *Model, cmd tea.Cmd, depth int) {
	if cmd == nil || depth > 5 {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-timeout():
		return
	}
	switch msg := msg.(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			drain(t, m, c, depth+1)
		}
	case nil:
	default:
		_, next := m.Update(msg)
		drain(t, m, next, depth+1)
	}
}

func TestModelFlow(t *testing.T) {
	store, _ := cache.OpenDir(t.TempDir())
	m := New(app.New(store, "test"), []provider.Provider{fake{}}, Options{ImageMode: img.Off, Dir: "/"})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	drain(t, m, m.Init(), 0)

	if len(m.results) != 2 || m.results[0].ID != "lodash" {
		t.Fatalf("results after index = %+v", m.results)
	}
	for _, r := range "lpad" {
		_, cmd := m.Update(press(string(r)))
		drain(t, m, cmd, 0)
	}
	if m.results[0].ID != "left-pad" {
		t.Fatalf("fuzzy top = %s", m.results[0].ID)
	}
	if !strings.Contains(m.View(), "left-pad") || !strings.Contains(m.View(), "INSERT") {
		t.Errorf("list view missing content:\n%s", m.View())
	}

	_, cmd := m.Update(press("enter"))
	drain(t, m, cmd, 0)
	if m.screen != screenDetail || m.detail == nil {
		t.Fatalf("detail not loaded: screen=%v err=%v", m.screen, m.detailErr)
	}
	v := m.View()
	for _, want := range []string{"Hello", "Releases", "Maintainers", "alice", "DETAIL"} {
		if !strings.Contains(v, want) {
			t.Errorf("detail view missing %q", want)
		}
	}
	m.Update(press("n"))
	if got := m.selectedConstraint(); got != "^2.0" {
		t.Errorf("selected constraint = %q", got)
	}
	m.Update(press("h"))
	if m.screen != screenList {
		t.Error("h did not go back")
	}
	m.Update(press("esc"))
	if m.insert {
		t.Error("esc did not leave insert mode")
	}
	m.Update(press("j"))
	m.Update(press("g"))
	m.Update(press("g"))
	if m.cursor != 0 {
		t.Errorf("gg cursor = %d", m.cursor)
	}
}

func timeout() <-chan struct{} {
	c := make(chan struct{})
	go func() { <-time.After(500 * time.Millisecond); close(c) }()
	return c
}

func TestInlineImages(t *testing.T) {
	body := "Intro\n\n[![Wizard](https://x/a.png)  \n**Caption A**](https://x/a.png)\n\nMiddle ![](https://x/b.png) end"
	got := bodyImages(body)
	if len(got) != 2 || got[0] != "https://x/a.png" || got[1] != "https://x/b.png" {
		t.Fatalf("bodyImages = %v", got)
	}
	store, _ := cache.OpenDir(t.TempDir())
	m := New(app.New(store, "test"), []provider.Provider{fake{}}, Options{ImageMode: img.HalfBlock, Dir: "/"})
	m.detail = &provider.Detail{Body: body}
	out := m.renderBody(80)
	for _, want := range []string{"Intro", imageMarker + "https://x/a.png", "Caption A", imageMarker + "https://x/b.png", "end"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered body missing %q:\n%q", want, out)
		}
	}
	if strings.Contains(out, "](") {
		t.Errorf("leftover markdown link syntax:\n%s", out)
	}
}

func (fake) Local(context.Context, *http.Client, provider.Project) ([]provider.LocalPackage, error) {
	return []provider.LocalPackage{
		{Package: provider.Package{Provider: "fake", ID: "lodash", Name: "Lodash"}, Module: "lodash", Origin: "contrib", Enabled: true},
		{Package: provider.Package{Provider: "fake", ID: "lodash", Name: "Lodash FP"}, Module: "lodash_fp", Origin: "contrib"},
		{Package: provider.Package{Provider: "fake", Name: "Mine"}, Module: "mine", Origin: "custom"},
	}, nil
}
func (fake) EnableCommands(lp provider.LocalPackage, _ provider.Project) ([]*exec.Cmd, error) {
	return []*exec.Cmd{exec.Command("true", "enable", lp.Module)}, nil
}

func TestTabs(t *testing.T) {
	store, _ := cache.OpenDir(t.TempDir())
	m := New(app.New(store, "test"), []provider.Provider{fake{}}, Options{ImageMode: img.Off, Dir: "/"})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	drain(t, m, m.Init(), 0)
	if !m.hasTabs() || len(m.local) != 3 {
		t.Fatalf("tabs=%v local=%d", m.hasTabs(), len(m.local))
	}
	if v := m.View(); !strings.Contains(v, "Installed 1") || !strings.Contains(v, "Uninstalled 2") || !strings.Contains(v, "● enabled") {
		t.Errorf("tab bar / installed marker missing:\n%s", v)
	}
	m.Update(press("esc"))
	m.Update(press("2"))
	if m.tab != tabInstalled || len(m.results) != 1 || m.results[0].ID != "lodash" {
		t.Fatalf("installed tab results = %+v", m.results)
	}
	if _, cmd := m.Update(press("i")); cmd != nil || !strings.Contains(m.status, "already installed") {
		t.Errorf("install on installed tab: status=%q", m.status)
	}
	m.Update(press("3"))
	if m.tab != tabUninstalled || len(m.results) != 2 {
		t.Fatalf("uninstalled tab results = %+v", m.results)
	}
	// Custom module: no upstream page.
	for i, p := range m.results {
		if p.ID == "mine" {
			m.cursor = i
		}
	}
	if cmd := m.openSelected(); cmd != nil || m.screen != screenList {
		t.Error("custom module must not open a detail page")
	}
	// Submodule: enable runs the provider's enable command.
	for i, p := range m.results {
		if p.ID == "lodash_fp" {
			m.cursor = i
		}
	}
	if _, cmd := m.Update(press("I")); cmd == nil || !strings.Contains(m.status, "enabling lodash_fp") {
		t.Errorf("enable: cmd=%v status=%q", cmd != nil, m.status)
	}
	// After a successful enable it moves to Installed, even though the project
	// files (core.extension.yml) only change after a config export.
	_, cmd := m.Update(execDoneMsg{label: "enable", pkg: provider.Package{Provider: "fake", ID: "lodash_fp"}, enabled: "lodash_fp"})
	drain(t, m, cmd, 0)
	if m.count(tabInstalled) != 2 || m.count(tabUninstalled) != 1 {
		t.Errorf("after enable: installed=%d uninstalled=%d", m.count(tabInstalled), m.count(tabUninstalled))
	}
	for i, p := range m.results {
		if p.ID == "mine" {
			m.cursor = i
		}
	}
	m.Update(press("2"))
	m.Update(press("3"))
	for i, p := range m.results {
		if p.ID == "lodash_fp" {
			t.Fatalf("lodash_fp still uninstalled at row %d", i)
		}
	}
	m.Update(press("2"))
	for i, p := range m.results {
		if p.ID == "lodash_fp" {
			m.cursor = i
		}
	}
	// Its detail page is the upstream project.
	m.openSelected()
	if m.screen != screenDetail || m.detailFor.ID != "lodash" || m.detailLocal == nil || m.detailLocal.Module != "lodash_fp" {
		t.Errorf("detail target = %+v local=%v", m.detailFor, m.detailLocal)
	}
}

func TestSearchHighlight(t *testing.T) {
	store, _ := cache.OpenDir(t.TempDir())
	m := New(app.New(store, "test"), []provider.Provider{fake{}}, Options{ImageMode: img.Off, Dir: "/"})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 20})
	drain(t, m, m.Init(), 0)
	m.present["fake/left-pad"] = false // in the codebase, nothing enabled
	v := m.View()
	for _, row := range strings.Split(v, "\n") {
		switch {
		case strings.Contains(row, "left-pad") && !strings.Contains(row, "○ installed"):
			t.Errorf("left-pad row not marked installed: %q", row)
		case strings.Contains(row, "lodash") && !strings.Contains(row, "● enabled"):
			t.Errorf("lodash row not marked enabled: %q", row)
		}
	}
}

// fake2 is a second provider without a project.
type fake2 struct{ fake }

func (fake2) Name() string { return "fake2" }
func (fake2) Index(_ context.Context, _ *http.Client, emit func([]provider.Package)) error {
	emit([]provider.Package{{Provider: "fake2", ID: "zebra", Name: "zebra", Popularity: 5}})
	return nil
}
func (fake2) Detect(string) (provider.Project, bool) { return provider.Project{}, false }

func TestSettingsPane(t *testing.T) {
	cfg, _ := config.LoadFile(filepath.Join(t.TempDir(), "config.json"))
	store, _ := cache.OpenDir(t.TempDir())
	all := []provider.Provider{fake{}, fake2{}}
	m := New(app.New(store, "test"), []provider.Provider{fake{}}, Options{ImageMode: img.Off, Dir: "/", Config: cfg, Available: all})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	drain(t, m, m.Init(), 0)

	m.Update(press("esc"))
	m.Update(press(","))
	if !m.settingsOpen || !strings.Contains(m.View(), "fake2") {
		t.Fatalf("settings pane not shown:\n%s", m.View())
	}
	// fake2 is enabled by default but has no project: not used here.
	if m.active("fake2") || !cfg.Enabled("fake2") || !strings.Contains(m.View(), "not used here") {
		t.Fatalf("fake2 should be enabled but inactive:\n%s", m.View())
	}
	// ⏎ uses it in this session without touching the saved setting.
	m.Update(press("j"))
	_, cmd := m.Update(press("enter"))
	drain(t, m, cmd, 0)
	if !m.active("fake2") || len(cfg.Disabled) != 0 {
		t.Fatalf("fake2 not activated: active=%v cfg=%v", m.active("fake2"), cfg.Disabled)
	}
	found := false
	for _, p := range m.index {
		found = found || p.ID == "zebra"
	}
	if !found {
		t.Error("fake2 index not loaded")
	}
	// space disables fake (saved): rows, project and tabs go away.
	m.Update(press("k"))
	m.Update(press(" "))
	if m.active("fake") || cfg.Enabled("fake") || m.hasTabs() {
		t.Fatalf("fake still active: %v tabs=%v", m.active("fake"), m.hasTabs())
	}
	for _, p := range m.index {
		if p.Provider == "fake" {
			t.Fatalf("fake rows left in index: %+v", p)
		}
	}
	reloaded, _ := config.LoadFile(cfg.Path())
	if reloaded.Enabled("fake") || !reloaded.Enabled("fake2") {
		t.Errorf("saved config = %v", reloaded.Disabled)
	}
	// space again re-enables and activates it.
	_, cmd = m.Update(press(" "))
	drain(t, m, cmd, 0)
	if !m.active("fake") || !cfg.Enabled("fake") {
		t.Fatalf("fake not re-enabled")
	}
	m.Update(press(" ")) // disable fake again
	// The last active plugin cannot be disabled or deactivated.
	m.Update(press("j"))
	m.Update(press(" "))
	if !m.active("fake2") || !strings.Contains(m.status, "at least one") {
		t.Errorf("last plugin disabled: status=%q", m.status)
	}
	m.Update(press("enter"))
	if !m.active("fake2") {
		t.Error("last plugin deactivated")
	}
	// The general option row toggles and saves SkipPrompt.
	m.Update(press("j"))
	m.Update(press(" "))
	if r, _ := config.LoadFile(cfg.Path()); !cfg.SkipPrompt || !r.SkipPrompt {
		t.Errorf("SkipPrompt not saved: %v", cfg.SkipPrompt)
	}
	if !strings.Contains(m.View(), "Ask which plugin") {
		t.Error("option row missing")
	}
	m.Update(press("esc"))
	if m.settingsOpen {
		t.Error("esc did not close settings")
	}
}

type iconFake struct{ fake2 }

func (iconFake) Icon() (string, string) { return "", "#0678be" }

func TestProviderIcons(t *testing.T) {
	store, _ := cache.OpenDir(t.TempDir())
	m := New(app.New(store, "test"), []provider.Provider{fake{}, iconFake{}}, Options{ImageMode: img.Off, Dir: "/",
		Available: []provider.Provider{fake{}, iconFake{}}})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	drain(t, m, m.Init(), 0)
	var zebra, lodash string
	for _, row := range strings.Split(m.View(), "\n") {
		switch {
		case strings.Contains(row, "zebra"):
			zebra = row
		case strings.Contains(row, "lodash"):
			lodash = row
		}
	}
	if !strings.Contains(zebra, "") || strings.Contains(zebra, "fake2") {
		t.Errorf("icon provider row = %q", zebra)
	}
	if !strings.Contains(lodash, "fake") { // no icon: name as fallback
		t.Errorf("text fallback row = %q", lodash)
	}
	m.cfg.TextIcons = true
	if tag := m.providerTag("fake2"); strings.Contains(tag, "") || !strings.Contains(tag, "fake2") {
		t.Errorf("text icons setting ignored: %q", tag)
	}
}

// countingFake counts index runs.
type countingFake struct {
	fake
	runs *int
}

func (c countingFake) Index(ctx context.Context, h *http.Client, emit func([]provider.Package)) error {
	*c.runs++
	return c.fake.Index(ctx, h, emit)
}

func TestRefreshSearchList(t *testing.T) {
	store, _ := cache.OpenDir(t.TempDir())
	runs := 0
	p := countingFake{runs: &runs}
	m := New(app.New(store, "test"), []provider.Provider{p}, Options{ImageMode: img.Off, Dir: "/"})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	drain(t, m, m.Init(), 0)
	if runs != 1 {
		t.Fatalf("initial index runs = %d", runs)
	}
	// Second start: index cached and fresh, no fetch.
	m2 := New(app.New(store, "test"), []provider.Provider{p}, Options{ImageMode: img.Off, Dir: "/"})
	m2.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	drain(t, m2, m2.Init(), 0)
	if runs != 1 {
		t.Fatalf("fresh cache refetched: runs = %d", runs)
	}
	// r on the search tab rebuilds it.
	m2.Update(press("esc"))
	_, cmd := m2.Update(press("r"))
	drain(t, m2, cmd, 0)
	if runs != 2 || !strings.Contains(m2.status, "index updated") || m2.indexing != 0 {
		t.Errorf("after r: runs=%d status=%q indexing=%d", runs, m2.status, m2.indexing)
	}
}
