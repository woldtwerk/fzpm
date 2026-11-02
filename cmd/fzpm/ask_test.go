package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"fzpm/internal/provider"
)

type stub struct{ name string }

func (s stub) Name() string { return s.name }
func (stub) Index(context.Context, *http.Client, func([]provider.Package)) error {
	return nil
}
func (stub) Search(context.Context, *http.Client, string) ([]provider.Package, error) {
	return nil, nil
}
func (stub) Detail(context.Context, *http.Client, string) (*provider.Detail, error) {
	return nil, nil
}
func (stub) Detect(string) (provider.Project, bool)      { return provider.Project{}, false }
func (stub) Actions() []provider.Action                  { return nil }
func (stub) InstallHint(provider.Package, string) string { return "" }
func key(k byte) func() (byte, error)                    { return func() (byte, error) { return k, nil } }

func TestAskProviders(t *testing.T) {
	one := []provider.Provider{stub{"drupal"}}
	two := []provider.Provider{stub{"drupal"}, stub{"npm"}}
	cases := []struct {
		provs []provider.Provider
		key   byte
		want  []string // nil = cancelled
	}{
		{one, '\r', []string{"drupal"}},
		{one, 'y', []string{"drupal"}},
		{one, 'n', nil},
		{one, 3, nil}, // ctrl-c
		{two, '2', []string{"npm"}},
		{two, 'a', []string{"drupal", "npm"}},
		{two, '3', nil},
		{two, 'q', nil},
	}
	for _, c := range cases {
		got, err := askProviders(c.provs, "/tmp", key(c.key), io.Discard)
		if c.want == nil {
			if !errors.Is(err, errCancelled) {
				t.Errorf("key %q: want cancel, got %v %v", c.key, nameList(got), err)
			}
			continue
		}
		if err != nil || len(got) != len(c.want) {
			t.Errorf("key %q: got %v %v, want %v", c.key, nameList(got), err, c.want)
			continue
		}
		for i := range got {
			if got[i].Name() != c.want[i] {
				t.Errorf("key %q: got %v, want %v", c.key, nameList(got), c.want)
			}
		}
	}
}
