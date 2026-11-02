package runner

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// Term runs commands one after another in a pseudo terminal and keeps their
// screen in a virtual terminal emulator, so a TUI can show them in a pane
// while colours, progress bars and prompts keep working.
type Term struct {
	emu     *vt.SafeEmulator
	updates chan struct{}
	done    chan error
	closing atomic.Bool

	mu   sync.Mutex
	ptmx *os.File // terminal of the running command; nil between commands
	w, h int
}

// StartTerm starts cmds in a w×h pseudo terminal. It fails only when the
// platform has no pseudo terminals; command failures are reported on Done.
func StartTerm(cmds []*exec.Cmd, w, h int) (*Term, error) {
	p, tty, err := pty.Open()
	if err != nil {
		return nil, err
	}
	p.Close()
	tty.Close()

	t := &Term{
		emu:     vt.NewSafeEmulator(w, h),
		updates: make(chan struct{}, 1),
		done:    make(chan error, 1),
		w:       w,
		h:       h,
	}
	go t.forwardInput()
	go func() {
		var err error
		for _, c := range cmds {
			fmt.Fprintf(t, "\x1b[1;36m$ %s\x1b[0m\r\n", String(c))
			if err = t.run(c); err != nil {
				fmt.Fprintf(t, "\r\n\x1b[1;31m✗ %s: %v\x1b[0m\r\n", String(c), err)
				err = fmt.Errorf("%s: %w", String(c), err)
				break
			}
		}
		t.done <- err
	}()
	return t, nil
}

// run executes one command and copies its output into the emulator.
func (t *Term) run(c *exec.Cmd) error {
	t.mu.Lock()
	ws := &pty.Winsize{Cols: uint16(t.w), Rows: uint16(t.h)}
	t.mu.Unlock()
	ptmx, err := pty.StartWithSize(c, ws)
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.ptmx = ptmx
	t.mu.Unlock()

	copied := make(chan struct{})
	go func() {
		io.Copy(t, ptmx) // ends when the terminal closes
		close(copied)
	}()
	err = c.Wait()
	// Read what is left; a background child may keep the terminal open, so
	// do not wait for it forever.
	select {
	case <-copied:
	case <-time.After(500 * time.Millisecond):
	}
	t.mu.Lock()
	ptmx.Close()
	t.ptmx = nil
	t.mu.Unlock()
	<-copied
	return err
}

// Write feeds command output into the emulator.
func (t *Term) Write(p []byte) (int, error) {
	n, err := t.emu.Write(p)
	select {
	case t.updates <- struct{}{}:
	default:
	}
	return n, err
}

// forwardInput passes the emulator's replies to terminal queries (cursor
// position, device attributes) to the running command. The emulator writes
// them to a pipe, so this must keep reading or output would block. It also
// closes the emulator: vt's Close and Read are not synchronized, so both
// happen on this goroutine.
func (t *Term) forwardInput() {
	buf := make([]byte, 256)
	for {
		n, err := t.emu.Read(buf)
		if err != nil {
			return
		}
		if t.closing.Load() {
			t.emu.Emulator.Close()
			return
		}
		t.Input(buf[:n])
	}
}

// Input sends keystrokes to the running command.
func (t *Term) Input(b []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ptmx != nil {
		t.ptmx.Write(b)
	}
}

// Resize changes the terminal size.
func (t *Term) Resize(w, h int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if w == t.w && h == t.h {
		return
	}
	t.w, t.h = w, h
	t.emu.Resize(w, h)
	if t.ptmx != nil {
		pty.Setsize(t.ptmx, &pty.Winsize{Cols: uint16(w), Rows: uint16(h)})
	}
}

// View renders the terminal screen with ANSI styles.
func (t *Term) View() string { return t.emu.Render() }

// Updates signals new output (coalesced).
func (t *Term) Updates() <-chan struct{} { return t.updates }

// Done delivers the result once all commands have finished.
func (t *Term) Done() <-chan error { return t.done }

// Close releases the emulator after Done.
func (t *Term) Close() {
	if t.closing.Swap(true) {
		return
	}
	t.emu.SendText("") // wakes forwardInput, which closes the emulator
}
