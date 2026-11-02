// Package runner executes provider action commands in the foreground,
// handing the terminal over from the TUI while they run.
package runner

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Seq runs commands one after another, echoing each command line. On failure
// it waits for Enter so the user can read the output before the TUI returns.
// It implements tea.ExecCommand.
type Seq struct {
	Cmds []*exec.Cmd
	// NoPause skips the "Press Enter" prompt on failure.
	NoPause bool
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
}

func (s *Seq) SetStdin(r io.Reader)  { s.stdin = r }
func (s *Seq) SetStdout(w io.Writer) { s.stdout = w }
func (s *Seq) SetStderr(w io.Writer) { s.stderr = w }

func (s *Seq) Run() error {
	for _, c := range s.Cmds {
		fmt.Fprintf(s.stdout, "\n\x1b[1;36m$ %s\x1b[0m\n", String(c))
		c.Stdin, c.Stdout, c.Stderr = s.stdin, s.stdout, s.stderr
		if err := c.Run(); err != nil {
			fmt.Fprintf(s.stderr, "\n\x1b[1;31m✗ %s: %v\x1b[0m\n", String(c), err)
			if !s.NoPause && s.stdin != nil {
				fmt.Fprint(s.stderr, "Press Enter to return…")
				bufio.NewReader(s.stdin).ReadString('\n')
			}
			return fmt.Errorf("%s: %w", String(c), err)
		}
	}
	return nil
}

// String renders a command line for display.
func String(c *exec.Cmd) string {
	return strings.Join(c.Args, " ")
}
