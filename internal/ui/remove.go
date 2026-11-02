package ui

import (
	"fmt"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"fzpm/internal/provider"
)

// disableAction handles d (disable) and D (disable + remove from the
// codebase) for the marked rows or the row under the cursor.
func (m *Model) disableAction(remove bool) tea.Cmd {
	targets, batch := m.localTargets()
	if len(targets) == 0 {
		m.setStatus("not in the project", false)
		return nil
	}
	var all []*exec.Cmd
	var disabled, shown []provider.Package
	for _, prov := range m.provs {
		var group, on []provider.LocalPackage
		for _, lp := range targets {
			if lp.Provider == prov.Name() {
				group = append(group, lp)
				if lp.Enabled {
					on = append(on, lp)
				}
			}
		}
		if len(group) == 0 {
			continue
		}
		proj := m.projects[prov.Name()]
		if len(on) > 0 {
			d, ok := prov.(provider.Disabler)
			if !ok {
				m.setStatus(prov.Name()+" modules cannot be disabled by fzpm", true)
				return nil
			}
			cmds, err := d.DisableCommands(on, proj)
			if err != nil {
				m.setStatus(err.Error(), true)
				return nil
			}
			all = append(all, cmds...)
			disabled = append(disabled, modulePkgs(on)...)
		}
		if !remove {
			shown = append(shown, modulePkgs(on)...)
			continue
		}
		r, ok := prov.(provider.Remover)
		if !ok {
			m.setStatus(prov.Name()+" modules cannot be removed by fzpm", true)
			return nil
		}
		cmds, err := r.RemoveCommands(group, proj)
		if err == nil {
			err = m.blockedRemoval(group)
		}
		if err != nil {
			m.setStatus(err.Error(), true)
			return nil
		}
		all = append(all, cmds...)
		shown = append(shown, modulePkgs(group)...)
	}
	if len(all) == 0 {
		m.setStatus("not enabled", false)
		return nil
	}
	label := "remove"
	switch {
	case !remove:
		label = "disable"
	case len(disabled) > 0:
		label = "disable + remove"
	}
	return m.execute(all, shown, execDoneMsg{label: label, disabled: disabled, batch: batch})
}

// localTargets returns the local packages d/D act on: the marked rows or the
// row under the cursor. Search rows (and their detail pages) stand for every
// local module of that upstream package.
func (m *Model) localTargets() (lps []provider.LocalPackage, batch bool) {
	var rows []provider.Package
	switch {
	case m.screen == screenDetail:
		if m.detailLocal != nil {
			return []provider.LocalPackage{*m.detailLocal}, false
		}
		rows = []provider.Package{m.detailFor}
	case len(m.markedRows()) > 0:
		rows, batch = m.markedRows(), true
	case m.cursor < len(m.results):
		rows = []provider.Package{m.results[m.cursor]}
	}
	upstream := m.screen == screenDetail || m.tab == tabSearch
	for _, r := range rows {
		if !upstream {
			if lp, ok := m.localByKey[key(r)]; ok {
				lps = append(lps, lp)
			}
			continue
		}
		for _, lp := range m.local {
			if r.ID != "" && lp.Provider == r.Provider && lp.ID == r.ID {
				lps = append(lps, lp)
			}
		}
	}
	return lps, batch
}

// blockedRemoval refuses to remove an upstream package while one of its
// modules that is not part of group is still enabled.
func (m *Model) blockedRemoval(group []provider.LocalPackage) error {
	removing := map[string]bool{}
	for _, lp := range group {
		removing[lp.Provider+"/"+lp.Module] = true
	}
	for _, lp := range m.local {
		if !lp.Enabled || removing[lp.Provider+"/"+lp.Module] {
			continue
		}
		for _, g := range group {
			if g.ID != "" && lp.Provider == g.Provider && lp.ID == g.ID {
				return fmt.Errorf("cannot remove %s: %s is still enabled", g.ID, lp.Module)
			}
		}
	}
	return nil
}

// modulePkgs identifies local packages by module name (what execDoneMsg and
// enabledNow use).
func modulePkgs(lps []provider.LocalPackage) []provider.Package {
	out := make([]provider.Package, len(lps))
	for i, lp := range lps {
		out[i] = provider.Package{Provider: lp.Provider, ID: lp.Module}
	}
	return out
}
