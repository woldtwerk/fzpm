//go:build !(linux || darwin || freebsd || netbsd || openbsd)

package img

import "errors"

type tty struct{}

func ttyCellSize() (int, int) { return 0, 0 }

func openTTY() (*tty, error) { return nil, errors.New("terminal query not supported") }
func (*tty) read([]byte) int { return 0 }
func (*tty) restore()        {}
