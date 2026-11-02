//go:build linux || darwin || freebsd || netbsd || openbsd

package img

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

type tty struct {
	fd  int
	old unix.Termios
}

// openTTY puts stdin into raw mode with a 100ms read timeout.
func openTTY() (*tty, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return nil, errors.New("not a terminal")
	}
	old, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return nil, err
	}
	raw := *old
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Iflag &^= unix.IXON | unix.ICRNL
	raw.Cc[unix.VMIN] = 0
	raw.Cc[unix.VTIME] = 1
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &raw); err != nil {
		return nil, err
	}
	return &tty{fd: fd, old: *old}, nil
}

func (t *tty) read(buf []byte) int {
	n, err := unix.Read(t.fd, buf)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// ttyCellSize derives the cell size from the window size in pixels.
func ttyCellSize() (int, int) {
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 || ws.Row == 0 || ws.Xpixel == 0 || ws.Ypixel == 0 {
		return 0, 0
	}
	return int(ws.Xpixel) / int(ws.Col), int(ws.Ypixel) / int(ws.Row)
}

func (t *tty) restore() { _ = unix.IoctlSetTermios(t.fd, ioctlSetTermios, &t.old) }
