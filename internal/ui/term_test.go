package ui

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"fzpm/internal/app"
	"fzpm/internal/cache"
	"fzpm/internal/img"
	"fzpm/internal/provider"
	"fzpm/internal/runner"
)

// Most UI tests check what an action would run; they use the full-terminal
// fallback so no commands are started.
func TestMain(m *testing.M) {
	startTerm = func([]*exec.Cmd, int, int) (*runner.Term, error) { return nil, errors.New("no pty in tests") }
	os.Exit(m.Run())
}

// nextTermMsg runs waitTerm until the modal reports something other than
// output.
func nextTermMsg(t *testing.T, m *Model) tea.Msg {
	t.Helper()
	for {
		ch := make(chan tea.Msg, 1)
		go func() { ch <- m.waitTerm()() }()
		select {
		case msg := <-ch:
			if _, ok := msg.(termUpdateMsg); ok {
				m.Update(msg)
				continue
			}
			return msg
		case <-time.After(10 * time.Second):
			t.Fatal("modal did not finish")
		}
	}
}

func TestCommandModal(t *testing.T) {
	startTerm = runner.StartTerm
	defer func() {
		startTerm = func([]*exec.Cmd, int, int) (*runner.Term, error) { return nil, errors.New("no pty") }
	}()

	store, _ := cache.OpenDir(t.TempDir())
	m := New(app.New(store, "test"), []provider.Provider{fake{}}, Options{ImageMode: img.Off, Dir: "/"})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})

	// A prompt is answered through the modal.
	cmd := m.execute([]*exec.Cmd{exec.Command("sh", "-c", `printf "ok? "; read a; echo "got $a"`)},
		[]provider.Package{{Provider: "fake", ID: "left-pad"}}, execDoneMsg{label: "install"})
	if m.term == nil || cmd == nil {
		t.Skip("no pseudo terminal")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(m.term.View(), "ok?") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	m.Update(press("y"))
	m.Update(press("enter"))
	done, ok := nextTermMsg(t, m).(termDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("done = %+v", done)
	}
	m.Update(done)
	v := m.View()
	for _, want := range []string{"install left-pad", "got y", "✓ done"} {
		if !strings.Contains(v, want) {
			t.Errorf("modal misses %q:\n%s", want, v)
		}
	}
	if !strings.Contains(m.status, "✓ install: left-pad") {
		t.Errorf("status = %q", m.status)
	}
	m.Update(termCloseMsg{m.term})
	if m.term != nil {
		t.Fatal("modal not closed after success")
	}

	// A failure stays open until esc.
	m.execute([]*exec.Cmd{exec.Command("sh", "-c", "echo boom; exit 2")},
		[]provider.Package{{Provider: "fake", ID: "lodash"}}, execDoneMsg{label: "remove"})
	m.Update(nextTermMsg(t, m))
	if v := m.View(); !strings.Contains(v, "boom") || !strings.Contains(v, "✗ failed") {
		t.Errorf("failure modal:\n%s", v)
	}
	m.Update(press("j"))
	if m.term == nil {
		t.Fatal("modal closed on an unrelated key")
	}
	m.Update(press("esc"))
	if m.term != nil || !strings.Contains(m.status, "✗ remove lodash failed") {
		t.Errorf("after esc: term=%v status=%q", m.term != nil, m.status)
	}
}

func TestKeyBytes(t *testing.T) {
	for _, c := range []struct {
		msg  tea.KeyMsg
		want string
	}{
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}, "y"},
		{tea.KeyMsg{Type: tea.KeyEnter}, "\r"},
		{tea.KeyMsg{Type: tea.KeyCtrlC}, "\x03"},
		{tea.KeyMsg{Type: tea.KeyBackspace}, "\x7f"},
		{tea.KeyMsg{Type: tea.KeyUp}, "\x1b[A"},
		{tea.KeyMsg{Type: tea.KeySpace}, " "},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b"), Alt: true}, "\x1bb"},
	} {
		if got := string(keyBytes(c.msg)); got != c.want {
			t.Errorf("%v: %q, want %q", c.msg, got, c.want)
		}
	}
}

func TestAutoUpdateGate(t *testing.T) {
	store, _ := cache.OpenDir(t.TempDir())
	newModel := func(version string) *Model {
		return New(app.New(store, "test"), []provider.Provider{fake{}}, Options{ImageMode: img.Off, Dir: "/", Version: version})
	}
	if newModel("dev").autoUpdate() != nil {
		t.Error("dev builds must not check for updates")
	}
	m := newModel("0.1.0")
	m.cfg.NoAutoUpdate = true
	if m.autoUpdate() != nil {
		t.Error("disabled in settings, still checking")
	}
	if newModel("0.1.0").autoUpdate() == nil {
		t.Error("release builds must check on every launch")
	}
}
