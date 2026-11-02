package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"fzpm/internal/provider"
)

var errCancelled = errors.New("cancelled")

// askProviders asks, with a single key press, whether (and which provider)
// to search when the current directory is not a project of any provider.
func askProviders(all []provider.Provider, dir string, readKey func() (byte, error), w io.Writer) ([]provider.Provider, error) {
	fmt.Fprintf(w, "No %s project found in %s.\r\n", strings.Join(nameList(all), "/"), shortPath(dir))
	if len(all) == 1 {
		fmt.Fprintf(w, "Search %s packages anyway? [Y/n] ", all[0].Name())
	} else {
		fmt.Fprint(w, "Search which source? ")
		for i, p := range all {
			fmt.Fprintf(w, "[%d] %s  ", i+1, p.Name())
		}
		fmt.Fprint(w, "[a] all  [q] quit ")
	}
	k, err := readKey()
	fmt.Fprint(w, "\r\n")
	if err != nil {
		return nil, err
	}
	switch {
	case len(all) == 1 && (k == 'y' || k == 'Y' || k == '\r' || k == '\n'):
		return all, nil
	case len(all) > 1 && (k == 'a' || k == '\r' || k == '\n'):
		return all, nil
	case len(all) > 1 && k >= '1' && int(k-'0') <= len(all):
		return all[k-'1' : k-'0'], nil
	}
	return nil, errCancelled
}

// readKey reads a single key press from the terminal.
func readKey() (byte, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return 0, errors.New("no supported project found; use --provider to search anyway")
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return 0, err
	}
	defer term.Restore(fd, old)
	b := make([]byte, 1)
	if _, err := os.Stdin.Read(b); err != nil {
		return 0, err
	}
	return b[0], nil
}

func nameList(provs []provider.Provider) []string {
	n := make([]string, len(provs))
	for i, p := range provs {
		n[i] = p.Name()
	}
	return n
}

func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
