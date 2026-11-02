package ui

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"fzpm/internal/update"
)

// autoUpdate checks for a newer fzpm release on every launch and installs
// it in the background; it is used from the next start on.
func (m *Model) autoUpdate() tea.Cmd {
	current := m.opts.Version
	if !update.IsRelease(current) || m.cfg.NoAutoUpdate || os.Getenv("FZPM_NO_UPDATE") != "" {
		return nil
	}
	return func() tea.Msg {
		latest, err := update.Latest(m.ctx, m.svc.HTTP)
		if err != nil || !update.Newer(latest, current) {
			return nil // up to date, offline or rate limited
		}
		exe, err := os.Executable()
		if err == nil {
			exe, err = filepath.EvalSymlinks(exe)
		}
		if err == nil {
			err = update.Install(m.ctx, m.svc.HTTP, latest, exe)
		}
		if err != nil {
			return statusMsg{fmt.Sprintf("fzpm %s is available; updating failed: %v", latest, err), true}
		}
		return statusMsg{fmt.Sprintf("✓ fzpm updated to %s; restart to use it", latest), false}
	}
}
