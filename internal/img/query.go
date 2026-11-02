package img

import (
	"bytes"
	"os"
	"regexp"
	"time"
)

const (
	queryTimeout = time.Second
	// tmux answers DA1 itself, before the outer terminal's kitty reply has
	// made its round trip; keep listening this long after DA1.
	tmuxGrace = 500 * time.Millisecond
)

var da1Re = regexp.MustCompile(`\x1b\[\?[0-9;]*c`)

// queryKitty sends a kitty graphics query followed by DA1. Terminals answer
// in order, so DA1 arriving first means "not supported" - except in tmux,
// which answers DA1 locally.
func queryKitty(tmux bool) bool {
	t, err := openTTY()
	if err != nil {
		return false
	}
	defer t.restore()

	q := "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\"
	if tmux {
		q = tmuxWrap(q)
	}
	if _, err := os.Stdout.WriteString(q + "\x1b[c"); err != nil {
		return false
	}

	var got []byte
	buf := make([]byte, 256)
	deadline := time.Now().Add(queryTimeout)
	for time.Now().Before(deadline) {
		n := t.read(buf) // returns after <=100ms without data
		got = append(got, buf[:n]...)
		if bytes.Contains(got, []byte("\x1b_Gi=31;")) {
			return bytes.Contains(got, []byte("\x1b_Gi=31;OK"))
		}
		if da1Re.Match(got) {
			if !tmux {
				return false
			}
			if d := time.Now().Add(tmuxGrace); d.Before(deadline) {
				deadline = d
			}
		}
	}
	return false
}

func tmuxWrap(seq string) string {
	return "\x1bPtmux;" + string(bytes.ReplaceAll([]byte(seq), []byte("\x1b"), []byte("\x1b\x1b"))) + "\x1b\\"
}
