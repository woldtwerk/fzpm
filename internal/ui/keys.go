package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		return m.quit()
	}
	// Several runes can arrive in one message (fast typing, tmux); outside
	// the search input, treat them as individual key presses.
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && !msg.Paste && (!m.insert || m.screen == screenDetail) {
		var cmds []tea.Cmd
		for _, r := range msg.Runes {
			_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: msg.Alt})
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}
	if m.settingsOpen {
		return m.settingsKey(k)
	}
	if m.help {
		m.help = false
		return m, nil
	}
	// Two-key sequences (gg, ]r, [r).
	if m.pending != "" {
		seq := m.pending + k
		m.pending = ""
		return m.handleSeq(seq)
	}
	if m.screen == screenDetail {
		return m.detailKey(k, msg)
	}
	if m.insert {
		return m.insertKey(k, msg)
	}
	return m.normalKey(k)
}

func (m *Model) handleSeq(seq string) (tea.Model, tea.Cmd) {
	switch seq {
	case "gg":
		if m.screen == screenDetail {
			m.vp.GotoTop()
		} else {
			m.move(-len(m.results))
			return m, m.schedulePrefetch()
		}
	case "gt":
		return m, m.cycleTab(1)
	case "gT":
		return m, m.cycleTab(-1)
	case "]r":
		if m.screen == screenDetail {
			m.vp.SetYOffset(m.releasesLine)
		}
	case "[r":
		if m.screen == screenDetail {
			m.vp.GotoTop()
		}
	}
	return m, nil
}

// insertKey: typing filters; navigation and actions via ctrl/alt chords so no
// mode switch is needed for the common path.
func (m *Model) insertKey(k string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k {
	case "esc":
		if len(m.marked) > 0 { // first esc ends the multi-selection
			clear(m.marked)
			return m, nil
		}
		m.insert = false
		m.input.Blur()
		return m, nil
	case "enter":
		return m, m.openSelected()
	case "alt+1", "alt+2", "alt+3":
		return m, m.setTab(tab(k[4] - '1'))
	case "down", "ctrl+n", "ctrl+j":
		m.move(1)
		return m, m.schedulePrefetch()
	case "up", "ctrl+p", "ctrl+k", "shift+tab":
		m.move(-1)
		return m, m.schedulePrefetch()
	case "pgdown", "ctrl+d":
		m.move(m.listHeight() / 2)
		return m, m.schedulePrefetch()
	case "pgup":
		m.move(-m.listHeight() / 2)
		return m, m.schedulePrefetch()
	case "alt+i":
		return m, m.runAction(0)
	case "alt+e", "alt+I":
		return m, m.runAction(1)
	case "tab":
		return m, m.toggleMark(true)
	case "ctrl+@": // ctrl+space
		return m, m.toggleMark(false)
	case "alt+,":
		m.openSettings()
		return m, nil
	case "alt+r":
		return m, m.refreshList()
	case "alt+y":
		return m, m.yank()
	case "alt+o":
		return m, m.openBrowser()
	}
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != before {
		return m, tea.Batch(cmd, m.queryChanged())
	}
	return m, cmd
}

func (m *Model) normalKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "q":
		return m.quit()
	case "/", "a", "A":
		m.insert = true
		m.input.CursorEnd()
		return m, m.input.Focus()
	case "S":
		m.insert = true
		m.input.SetValue("")
		return m, tea.Batch(m.input.Focus(), m.queryChanged())
	case "j", "down", "ctrl+n":
		m.move(1)
	case "k", "up", "ctrl+p":
		m.move(-1)
	case "ctrl+d":
		m.move(m.listHeight() / 2)
	case "ctrl+u":
		m.move(-m.listHeight() / 2)
	case "ctrl+f", "pgdown":
		m.move(m.listHeight())
	case "ctrl+b", "pgup":
		m.move(-m.listHeight())
	case "G":
		m.move(len(m.results))
	case "g":
		m.pending = "g"
		return m, nil
	case "H":
		m.cursor = m.offset
	case "L":
		m.cursor = min(len(m.results)-1, m.offset+m.listHeight()-1)
	case "l", "enter":
		return m, m.openSelected()
	case "1", "2", "3":
		return m, m.setTab(tab(k[0] - '1'))
	case "r":
		return m, m.refreshList()
	case "i":
		return m, m.runAction(0)
	case "e", "I":
		return m, m.runAction(1)
	case "d":
		return m, m.disableAction(false)
	case "D":
		return m, m.disableAction(true)
	case " ", "ctrl+@":
		return m, m.toggleMark(false)
	case "tab":
		return m, m.toggleMark(true)
	case "esc":
		clear(m.marked)
		return m, nil
	case "y":
		return m, m.yank()
	case "o":
		return m, m.openBrowser()
	case "?":
		m.help = true
		return m, nil
	case ",":
		m.openSettings()
		return m, nil
	default:
		return m, nil
	}
	return m, m.schedulePrefetch()
}

func (m *Model) detailKey(k string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k {
	case "h", "esc", "q", "backspace", "left":
		m.screen = screenList
		return m, nil
	case "j", "down", "ctrl+n", "ctrl+e":
		m.vp.ScrollDown(1)
	case "k", "up", "ctrl+p", "ctrl+y":
		m.vp.ScrollUp(1)
	case "ctrl+d":
		m.vp.HalfPageDown()
	case "ctrl+u":
		m.vp.HalfPageUp()
	case "ctrl+f", "pgdown", " ":
		m.vp.PageDown()
	case "ctrl+b", "pgup":
		m.vp.PageUp()
	case "G":
		m.vp.GotoBottom()
	case "g", "]", "[":
		m.pending = k
	case "n":
		return m, m.moveRelease(1)
	case "N":
		return m, m.moveRelease(-1)
	case "i", "alt+i":
		return m, m.runAction(0)
	case "e", "I", "alt+e", "alt+I":
		return m, m.runAction(1)
	case "d":
		return m, m.disableAction(false)
	case "D":
		return m, m.disableAction(true)
	case "y":
		return m, m.yank()
	case "o":
		return m, m.openBrowser()
	case "r":
		if m.refreshing {
			return m, nil
		}
		m.setStatus("reloading…", false)
		return m, m.refreshDetail(m.detailFor)
	case "J":
		m.side.ScrollDown(1)
	case "K":
		m.side.ScrollUp(1)
	case "p":
		m.showImages = !m.showImages && m.opts.ImageMode != 0
		return m, tea.Batch(m.fetchDetailImages(), m.rebuildDetail(false))
	case "?":
		m.help = true
	}
	return m, nil
}

// moveRelease selects the next/previous release; install actions then use
// its version constraint.
func (m *Model) moveRelease(d int) tea.Cmd {
	if m.detail == nil || len(m.detail.Releases) == 0 {
		return nil
	}
	m.relCursor = max(-1, min(len(m.detail.Releases)-1, m.relCursor+d))
	cmd := m.rebuildDetail(false)
	if m.relCursor >= 0 {
		line := m.releaseRow(m.relCursor)
		if line < m.vp.YOffset || line >= m.vp.YOffset+m.vp.Height {
			m.vp.SetYOffset(max(0, line-m.vp.Height/2))
		}
	}
	return cmd
}

func (m *Model) listHeight() int {
	h := m.height - 3
	if m.hasTabs() {
		h--
	}
	return max(1, h)
}

func (m *Model) move(d int) {
	m.cursor += d
	m.clampList()
}

func (m *Model) clampList() {
	m.cursor = max(0, min(m.cursor, len(m.results)-1))
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	m.offset = max(0, min(m.offset, len(m.results)-h))
}
