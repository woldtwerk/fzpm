package ui

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/aymanbagabas/go-osc52/v2"
)

func insideTmux() bool { return os.Getenv("TMUX") != "" }

// paneVisible reports whether our tmux pane is on screen; tmux drops
// passthrough data for hidden panes. Cached briefly to avoid an exec per image.
func (m *Model) paneVisible() bool {
	if !insideTmux() {
		return true
	}
	if time.Since(m.visibleAt) < 500*time.Millisecond {
		return m.visible
	}
	out, err := exec.Command("tmux", "display", "-p", "-t", os.Getenv("TMUX_PANE"),
		"#{session_attached} #{window_active} #{window_zoomed_flag} #{pane_active}").Output()
	f := strings.Fields(string(out))
	m.visible = err != nil || len(f) != 4 || // unknown: assume visible
		(f[0] != "0" && f[1] == "1" && (f[2] == "0" || f[3] == "1"))
	m.visibleAt = time.Now()
	return m.visible
}

// copyToClipboard prefers native clipboard tools and falls back to OSC 52.
func copyToClipboard(text string) error {
	var c *exec.Cmd
	switch {
	case runtime.GOOS == "darwin":
		c = exec.Command("pbcopy")
	case os.Getenv("WAYLAND_DISPLAY") != "" && has("wl-copy"):
		c = exec.Command("wl-copy")
	case os.Getenv("DISPLAY") != "" && has("xclip"):
		c = exec.Command("xclip", "-selection", "clipboard")
	}
	if c != nil {
		c.Stdin = strings.NewReader(text)
		if err := c.Run(); err == nil {
			return nil
		}
	}
	seq := osc52.New(text)
	if insideTmux() {
		seq = seq.Tmux()
	}
	_, err := seq.WriteTo(os.Stdout)
	return err
}

func has(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

func openURL(url string) error {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return errors.New("refusing to open non-http URL")
	}
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	return c.Start()
}
