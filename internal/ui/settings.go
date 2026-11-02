package ui

import (
	"fmt"
	"os"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"fzpm/internal/provider"
	"fzpm/internal/search"
)

// The settings pane switches providers (plugins) on and off.
//
//   - enabled (checkbox, saved): the plugin may be used at all
//   - active (this session): enabled and used right now; at startup that is
//     the enabled plugins that detect a project here (or all enabled ones
//     when none does)

func (m *Model) openSettings() {
	m.settingsOpen = true
	m.settingsCursor = 0
}

func (m *Model) settingsKey(k string) (tea.Model, tea.Cmd) {
	all := m.available()
	switch k {
	case "esc", "q", ",", "alt+,":
		m.settingsOpen = false
	case "j", "down", "ctrl+n", "tab":
		m.settingsCursor = min(len(all)+len(m.options())-1, m.settingsCursor+1)
	case "k", "up", "ctrl+p", "shift+tab":
		m.settingsCursor = max(0, m.settingsCursor-1)
	case " ", "x", "enter":
		if i := m.settingsCursor - len(all); i >= 0 {
			m.options()[i].toggle()
			if err := m.cfg.Save(); err != nil {
				m.setStatus("settings not saved: "+err.Error(), true)
			}
			return m, nil
		}
		if k == "enter" {
			return m, m.toggleActive(all[m.settingsCursor])
		}
		return m, m.toggleEnabled(all[m.settingsCursor])
	}
	return m, nil
}

// option is a general on/off setting shown below the plugins.
type option struct {
	label, hint string
	on          bool
	toggle      func()
}

func (m *Model) options() []option {
	return []option{
		{"Ask which plugin to use when no project is found", "off: search all enabled plugins",
			!m.cfg.SkipPrompt, func() { m.cfg.SkipPrompt = !m.cfg.SkipPrompt }},
		{"Nerd Font icons for plugins", "off: show plugin names",
			!m.cfg.TextIcons, func() { m.cfg.TextIcons = !m.cfg.TextIcons }},
	}
}

// icon is a provider's colored Nerd Font glyph, or "" when icons are off or
// it has none.
func (m *Model) icon(name string) string {
	ic, ok := m.lookup(name).(provider.Iconer)
	if !ok || m.cfg.TextIcons {
		return ""
	}
	glyph, color := ic.Icon()
	// Set and reset only the foreground, so a row highlight's background
	// continues under the icon.
	var r, g, b int
	if _, err := fmt.Sscanf(color, "#%02x%02x%02x", &r, &g, &b); err != nil {
		return glyph
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[39m", r, g, b, glyph)
}

// providerTag labels a row's provider: its icon, or its name as fallback.
func (m *Model) providerTag(name string) string {
	if ic := m.icon(name); ic != "" {
		return ic + " "
	}
	return badge.Render(pad(name, 7)) + " "
}

// lookup finds an available provider by name (active or not).
func (m *Model) lookup(name string) provider.Provider {
	for _, p := range m.available() {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

func (m *Model) active(name string) bool { return m.provider(name) != nil }

// toggleEnabled switches the saved setting; enabling also activates.
func (m *Model) toggleEnabled(p provider.Provider) tea.Cmd {
	name := p.Name()
	if !m.cfg.Enabled(name) {
		m.saveConfig(name, true)
		m.setStatus(name+" enabled", false)
		if m.active(name) {
			return nil
		}
		return m.activate(p)
	}
	if m.active(name) && len(m.provs) == 1 {
		m.setStatus("at least one plugin has to stay enabled", true)
		return nil
	}
	m.saveConfig(name, false)
	m.setStatus(name+" disabled", false)
	if m.active(name) {
		m.deactivate(p)
	}
	return nil
}

// toggleActive uses an enabled plugin in this session or stops using it,
// without changing the saved setting.
func (m *Model) toggleActive(p provider.Provider) tea.Cmd {
	name := p.Name()
	switch {
	case !m.cfg.Enabled(name):
		m.setStatus(name+" is disabled; press space to enable it", true)
	case !m.active(name):
		m.setStatus(name+" active in this session", false)
		return m.activate(p)
	case len(m.provs) == 1:
		m.setStatus("at least one plugin has to stay active", true)
	default:
		m.deactivate(p)
		m.setStatus(name+" not used in this session", false)
	}
	return nil
}

func (m *Model) saveConfig(name string, on bool) {
	m.cfg.SetEnabled(name, on)
	if err := m.cfg.Save(); err != nil {
		m.setStatus("settings not saved: "+err.Error(), true)
	}
}

func (m *Model) activate(p provider.Provider) tea.Cmd {
	m.provs = append(m.provs, p)
	slices.SortFunc(m.provs, func(a, b provider.Provider) int { return strings.Compare(a.Name(), b.Name()) })
	if proj, ok := p.Detect(m.opts.Dir); ok {
		m.projects[p.Name()] = proj
	}
	var cmds []tea.Cmd
	cached, fresh := m.svc.LoadIndex(p)
	m.index, _ = search.Merge(m.index, cached)
	if !fresh {
		m.indexing++
		go m.runIndex(p)
		cmds = append(cmds, m.waitEvent(), m.spin())
	}
	m.refreshProviderUI()
	return tea.Batch(append(cmds, m.loadLocal())...)
}

func (m *Model) deactivate(p provider.Provider) {
	name := p.Name()
	m.saveIndex(name)
	m.provs = slices.DeleteFunc(m.provs, func(q provider.Provider) bool { return q.Name() == name })
	delete(m.projects, name)
	m.index = slices.DeleteFunc(m.index, func(pkg provider.Package) bool { return pkg.Provider == name })
	m.local = slices.DeleteFunc(m.local, func(lp provider.LocalPackage) bool { return lp.Provider == name })
	m.rebuildLocalMaps()
	if !m.hasTabs() {
		m.tab = tabSearch
	}
	m.refreshProviderUI()
}

// refreshProviderUI updates everything derived from the active providers.
func (m *Model) refreshProviderUI() {
	m.updatePlaceholder()
	m.input.Width = max(10, min(m.width/2, m.width-lipgloss.Width(m.projectLabel())-4))
	m.refilter(true)
}

func (m *Model) settingsView() string {
	var b strings.Builder
	b.WriteString(bold.Inherit(accent).Render("Plugins") + "\n\n")
	for i, p := range m.available() {
		marker := "  "
		if i == m.settingsCursor {
			marker = accent.Render("▸ ")
		}
		box, state := dim.Render("[ ]"), dim.Render("disabled")
		if m.cfg.Enabled(p.Name()) {
			box, state = okStyle.Render("[x]"), installedStyle.Render("enabled, not used here")
			if m.active(p.Name()) {
				state = okStyle.Render("active")
			}
		}
		where := dim.Render("no project here")
		if proj, ok := p.Detect(m.opts.Dir); ok {
			where = proj.Label
		}
		ic := m.icon(p.Name())
		if ic == "" {
			ic = " "
		}
		line := fmt.Sprintf("%s%s %s %s  %s  %s", marker, box, ic, bold.Render(pad(p.Name(), 8)), pad(state, 22), where)
		if i == m.settingsCursor {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + bold.Inherit(accent).Render("General") + "\n\n")
	for i, o := range m.options() {
		sel := m.settingsCursor == len(m.available())+i
		marker, check := "  ", okStyle.Render("[x]")
		if sel {
			marker = accent.Render("▸ ")
		}
		if !o.on {
			check = dim.Render("[ ]")
		}
		line := marker + check + " " + o.label + " " + dim.Render("("+o.hint+")")
		if sel {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + dim.Render("space enable/disable (saved) · ⏎ use plugin in this session only · j/k move · esc close"))
	if p := m.cfg.Path(); p != "" {
		b.WriteString("\n" + dim.Render("saved to "+shortHome(p)))
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentColor).Padding(1, 3)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box.Render(b.String()))
}

func shortHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// available lists every provider that can be switched on.
func (m *Model) available() []provider.Provider {
	if m.opts.Available != nil {
		return m.opts.Available
	}
	return provider.All()
}
