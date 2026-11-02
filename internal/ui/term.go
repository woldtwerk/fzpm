package ui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"fzpm/internal/runner"
)

// startTerm is replaced in tests.
var startTerm = runner.StartTerm

// termCloseDelay keeps the modal open briefly after success.
const termCloseDelay = 800 * time.Millisecond

type (
	termUpdateMsg struct{ t *runner.Term }
	termDoneMsg   struct {
		t   *runner.Term
		err error
	}
	termCloseMsg struct{ t *runner.Term }
)

// termSize is the inner size of the command modal: about three quarters of
// the width (at most 100 columns) and half the height (at most 20 rows),
// leaving the list visible around it.
func (m *Model) termSize() (int, int) {
	w := min(100, m.width*3/4, m.width-4)
	h := min(20, m.height/2, m.height-4)
	return max(20, w), max(5, h)
}

// waitTerm waits for new output or the end of the running commands.
func (m *Model) waitTerm() tea.Cmd {
	t := m.term
	return func() tea.Msg {
		select {
		case <-t.Updates():
			return termUpdateMsg{t}
		case err := <-t.Done():
			return termDoneMsg{t, err}
		}
	}
}

func (m *Model) closeTerm() {
	m.term.Close()
	m.term = nil
}

// termKey forwards keys to the running commands; once they are done, esc,
// enter or q close the modal.
func (m *Model) termKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.termFinished {
		switch msg.String() {
		case "esc", "enter", "q", "ctrl+c":
			m.closeTerm()
		}
		return m, nil
	}
	if b := keyBytes(msg); b != nil {
		m.term.Input(b)
	}
	return m, nil
}

// keyBytes encodes a key press as terminal input.
func keyBytes(msg tea.KeyMsg) []byte {
	var s string
	switch msg.Type {
	case tea.KeyRunes:
		s = string(msg.Runes)
	case tea.KeySpace:
		s = " "
	case tea.KeyShiftTab:
		s = "\x1b[Z"
	case tea.KeyUp:
		s = "\x1b[A"
	case tea.KeyDown:
		s = "\x1b[B"
	case tea.KeyRight:
		s = "\x1b[C"
	case tea.KeyLeft:
		s = "\x1b[D"
	case tea.KeyHome:
		s = "\x1b[H"
	case tea.KeyEnd:
		s = "\x1b[F"
	case tea.KeyPgUp:
		s = "\x1b[5~"
	case tea.KeyPgDown:
		s = "\x1b[6~"
	case tea.KeyDelete:
		s = "\x1b[3~"
	default:
		// Control keys (ctrl+c, enter, tab, esc, backspace) are their own
		// byte values in Bubble Tea.
		if (msg.Type >= 0 && msg.Type < 32) || msg.Type == 127 {
			s = string(rune(msg.Type))
		} else {
			return nil
		}
	}
	if msg.Alt {
		s = "\x1b" + s
	}
	return []byte(s)
}

func (m *Model) termView() string {
	w, h := m.termSize()
	color, status := accentColor, m.spinner.View()+" "+dim.Render("running · keys go to the command")
	if m.termFinished {
		if m.termResult.err != nil {
			color = lipgloss.AdaptiveColor{Light: "#e05555", Dark: "#e05555"}
			status = errStyle.Render("✗ failed") + dim.Render(" · esc/⏎ close")
		} else {
			color = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"}
			status = okStyle.Render("✓ done")
		}
	}
	lines := strings.Split(m.term.View(), "\n")
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, l := range lines[:h] {
		lines[i] = ansi.Truncate(l, w, "")
	}
	title := bold.Render(ansi.Truncate(m.termResult.label+" "+m.termResult.what, w, "…"))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(color).Padding(0, 1).Width(w + 2)
	return box.Render(title + "\n" + strings.Join(lines[:h], "\n") + "\n" + status)
}

// overlay draws box centred on top of bg (both rendered strings).
func overlay(bg, box string, width, height int) string {
	bgLines := strings.Split(bg, "\n")
	for len(bgLines) < height {
		bgLines = append(bgLines, "")
	}
	boxLines := strings.Split(box, "\n")
	bw := lipgloss.Width(box)
	x, y := max(0, (width-bw)/2), max(0, (height-len(boxLines))/2)
	for i, bl := range boxLines {
		if y+i >= len(bgLines) {
			break
		}
		line := bgLines[y+i]
		left := ansi.Truncate(line, x, "")
		if pad := x - lipgloss.Width(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		right := ansi.TruncateLeft(line, x+bw, "")
		bgLines[y+i] = left + "\x1b[0m" + bl + "\x1b[0m" + right
	}
	return strings.Join(bgLines, "\n")
}
