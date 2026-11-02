package runner

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func waitTerm(t *testing.T, term *Term) error {
	t.Helper()
	select {
	case err := <-term.Done():
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("commands did not finish")
		return nil
	}
}

func TestTerm(t *testing.T) {
	term, err := StartTerm([]*exec.Cmd{
		exec.Command("printf", `\033[32mgreen\033[0m\n`),
		exec.Command("sh", "-c", "test -t 0 && echo tty"),
	}, 40, 6)
	if err != nil {
		t.Skip("no pseudo terminal:", err)
	}
	if err := waitTerm(t, term); err != nil {
		t.Fatal(err)
	}
	v := term.View()
	for _, want := range []string{"$ printf", "green", "tty"} {
		if !strings.Contains(v, want) {
			t.Errorf("screen misses %q:\n%s", want, v)
		}
	}
	term.Close()
}

func TestTermFailureAndInput(t *testing.T) {
	term, err := StartTerm([]*exec.Cmd{
		exec.Command("sh", "-c", `printf "name? "; read a; echo "hi $a"; exit 3`),
		exec.Command("echo", "never"),
	}, 40, 6)
	if err != nil {
		t.Skip("no pseudo terminal:", err)
	}
	// Wait for the prompt, then answer it.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(term.View(), "name?") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	term.Input([]byte("bob\r"))
	err = waitTerm(t, term)
	if err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("err = %v", err)
	}
	v := term.View()
	if !strings.Contains(v, "hi bob") || strings.Contains(v, "never") || !strings.Contains(v, "✗") {
		t.Errorf("screen:\n%s", v)
	}
	term.Resize(60, 8)
	term.Close()
}
