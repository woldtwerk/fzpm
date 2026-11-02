package composer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindAndBin(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{"require":{"x/y":"^1"}}`), 0o644)
	sub := filepath.Join(root, "a", "b")
	os.MkdirAll(sub, 0o755)

	proj, ok := Find(sub, func(c JSON) bool { return c.Requires("x/y") })
	if !ok || proj.Root != root || proj.Data["ddev"] != "" || !strings.HasPrefix(proj.Label, "composer: ") {
		t.Fatalf("find = %+v, %v", proj, ok)
	}
	if got := strings.Join(Bin(proj, "typo3", "cache:flush").Args, " "); got != filepath.Join(root, "vendor/bin/typo3")+" cache:flush" {
		t.Errorf("bin = %q", got)
	}
	if _, ok := Find(sub, func(c JSON) bool { return c.Requires("z/z") }); ok {
		t.Error("found a project that does not match")
	}
}

func TestConstraint(t *testing.T) {
	for in, want := range map[string]string{"v1.2.3": "^1.2.3", "2.0.0": "^2.0.0", "v3.0.0-RC1": "3.0.0-RC1"} {
		if got := Constraint(in); got != want {
			t.Errorf("Constraint(%q) = %q, want %q", in, got, want)
		}
	}
}
