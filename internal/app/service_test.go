package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fzpm/internal/cache"
	"fzpm/internal/provider"
)

type countingProvider struct {
	provider.Provider // only Name and Detail are used
	calls             int
}

func (*countingProvider) Name() string { return "fake" }
func (c *countingProvider) Detail(context.Context, *http.Client, string) (*provider.Detail, error) {
	c.calls++
	return &provider.Detail{Body: "fresh"}, nil
}

func TestDetailStaleWhileRevalidate(t *testing.T) {
	dir := t.TempDir()
	store, _ := cache.OpenDir(dir)
	p := &countingProvider{}
	svc := New(store, "test")

	// Miss: fetched from the provider and cached.
	if d, stale, err := svc.Detail(context.Background(), p, "x"); err != nil || stale || d.Body != "fresh" || p.calls != 1 {
		t.Fatalf("miss: d=%v stale=%v err=%v calls=%d", d, stale, err, p.calls)
	}
	// Age the entry beyond the TTL; a new store has no memory layer.
	old := time.Now().Add(-DetailTTL - time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "fake", "detail", "x"), old, old); err != nil {
		t.Fatal(err)
	}
	store2, _ := cache.OpenDir(dir)
	svc2 := New(store2, "test")
	d, stale, err := svc2.Detail(context.Background(), p, "x")
	if err != nil || !stale || d.Body != "fresh" || p.calls != 1 {
		t.Fatalf("stale hit must be served from cache without fetching: stale=%v err=%v calls=%d", stale, err, p.calls)
	}
	if svc2.HasFreshDetail(p, "x") {
		t.Error("stale entry reported fresh")
	}
	if _, err := svc2.RefreshDetail(context.Background(), p, "x"); err != nil || p.calls != 2 {
		t.Fatalf("refresh: err=%v calls=%d", err, p.calls)
	}
	if !svc2.HasFreshDetail(p, "x") {
		t.Error("refreshed entry not fresh")
	}
}
