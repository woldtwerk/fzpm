// Package composer holds what providers for Composer-based projects share:
// project detection (with ddev), composer commands, composer.json /
// composer.lock reading and the packagist.org API.
package composer

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"fzpm/internal/provider"
)

// Validation of names and versions passed to commands.
var (
	ValidName       = regexp.MustCompile(`^[a-z0-9]([_.-]?[a-z0-9]+)*/[a-z0-9](([_.]|-{1,2})?[a-z0-9]+)*$`)
	ValidConstraint = regexp.MustCompile(`^[0-9A-Za-z.\-^~|<>=*@ ]*$`)
)

// JSON is the part of composer.json the providers look at.
type JSON struct {
	Require map[string]string `json:"require"`
	Config  struct {
		BinDir string `json:"bin-dir"`
	} `json:"config"`
	Extra map[string]json.RawMessage `json:"extra"`
}

// Requires reports whether a package is required (not require-dev).
func (c JSON) Requires(name string) bool {
	_, ok := c.Require[name]
	return ok
}

// Read reads dir/composer.json.
func Read(dir string) (JSON, bool) {
	var c JSON
	b, err := os.ReadFile(filepath.Join(dir, "composer.json"))
	if err != nil || json.Unmarshal(b, &c) != nil {
		return c, false
	}
	return c, true
}

// Find walks up from dir to the first composer.json that match accepts. A
// sibling .ddev directory makes commands run through ddev.
func Find(dir string, match func(JSON) bool) (provider.Project, bool) {
	for d := dir; ; d = filepath.Dir(d) {
		if c, ok := Read(d); ok && match(c) {
			p := provider.Project{Root: d, Data: map[string]string{}}
			kind := "composer"
			if fi, err := os.Stat(filepath.Join(d, ".ddev")); err == nil && fi.IsDir() {
				p.Data["ddev"] = "1"
				kind = "ddev"
			}
			p.Label = kind + ": " + ShortPath(d)
			return p, true
		}
		if parent := filepath.Dir(d); parent == d {
			return provider.Project{}, false
		}
	}
}

// ShortPath abbreviates the home directory with "~".
func ShortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// Spec is name:version, or name for the latest version.
func Spec(name, version string) string {
	if version != "" {
		return name + ":" + version
	}
	return name
}

// Command runs composer in the project (through ddev if present).
func Command(proj provider.Project, args ...string) *exec.Cmd {
	return Exec(proj, "composer", args...)
}

// Exec runs a tool in the project, through ddev if present (`ddev <name>`).
func Exec(proj provider.Project, name string, args ...string) *exec.Cmd {
	var c *exec.Cmd
	if proj.Data["ddev"] != "" {
		c = exec.Command("ddev", append([]string{name}, args...)...)
	} else {
		c = exec.Command(name, args...)
	}
	c.Dir = proj.Root
	return c
}

// Bin runs a vendor binary of the project (bin-dir from composer.json),
// through `ddev exec` if present.
func Bin(proj provider.Project, name string, args ...string) *exec.Cmd {
	bin := "vendor/bin"
	if c, ok := Read(proj.Root); ok && c.Config.BinDir != "" {
		bin = filepath.ToSlash(filepath.Clean(c.Config.BinDir))
	}
	var c *exec.Cmd
	if proj.Data["ddev"] != "" {
		c = exec.Command("ddev", append([]string{"exec", bin + "/" + name}, args...)...)
	} else {
		c = exec.Command(filepath.Join(proj.Root, bin, name), args...)
	}
	c.Dir = proj.Root
	return c
}

// Locked is a package entry of composer.lock.
type Locked struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Type    string `json:"type"`
	Dist    struct {
		Type string `json:"type"` // "path" for packages from a local path repository
	} `json:"dist"`
	Extra map[string]json.RawMessage `json:"extra"`
}

// Lock returns the packages of root/composer.lock (nil without one).
func Lock(root string) []Locked {
	b, err := os.ReadFile(filepath.Join(root, "composer.lock"))
	if err != nil {
		return nil
	}
	var lock struct {
		Packages []Locked `json:"packages"`
	}
	if json.Unmarshal(b, &lock) != nil {
		return nil
	}
	return lock.Packages
}
