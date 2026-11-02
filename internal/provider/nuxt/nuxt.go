// Package nuxt implements a provider for Nuxt modules (nuxt.com/modules).
package nuxt

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"fzpm/internal/provider"
)

// Endpoints (variables so tests can point them at a local server).
var (
	modulesURL   = "https://api.nuxt.com/modules"
	npmURL       = "https://registry.npmjs.org/"
	downloadsURL = "https://npmx.dev/api/registry/downloads/versions?packages="
)

var (
	validNpm     = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._~-]*/)?[a-z0-9][a-z0-9._~-]*$`)
	validVersion = regexp.MustCompile(`^[0-9A-Za-z.^~<>=*|+ -]*$`)
)

func init() { provider.Register(&Nuxt{}) }

// Nuxt is the Nuxt module provider. Package IDs are npm package names,
// which is what package.json and nuxt.config refer to.
type Nuxt struct {
	db modulesDB // nuxt.com module list, loaded on demand
}

func (*Nuxt) Name() string { return "nuxt" }

// Icon is Nerd Fonts' nf-md-nuxt in Nuxt green.
func (*Nuxt) Icon() (string, string) { return "\U000F1106", "#00dc82" }

// Detect walks up from dir looking for a package.json depending on nuxt.
func (*Nuxt) Detect(dir string) (provider.Project, bool) {
	for d := dir; ; d = filepath.Dir(d) {
		if pkg, ok := readPackageJSON(d); ok && pkg.has("nuxt") {
			pm := packageManager(d)
			return provider.Project{
				Root:  d,
				Label: pm + ": " + shortPath(d),
				Data:  map[string]string{"pm": pm},
			}, true
		}
		if parent := filepath.Dir(d); parent == d {
			return provider.Project{}, false
		}
	}
}

type packageJSON struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func (p packageJSON) has(name string) bool {
	_, a := p.Dependencies[name]
	_, b := p.DevDependencies[name]
	return a || b
}

func readPackageJSON(dir string) (packageJSON, bool) {
	var p packageJSON
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil || json.Unmarshal(b, &p) != nil {
		return p, false
	}
	return p, true
}

// packageManager guesses the package manager from the lockfile.
func packageManager(dir string) string {
	for _, c := range []struct{ file, pm string }{
		{"pnpm-lock.yaml", "pnpm"},
		{"yarn.lock", "yarn"},
		{"bun.lock", "bun"},
		{"bun.lockb", "bun"},
	} {
		if _, err := os.Stat(filepath.Join(dir, c.file)); err == nil {
			return c.pm
		}
	}
	return "npm"
}

func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// Actions: [0] install the package only, [1] install and register it in
// nuxt.config (what `nuxi module add` does by default).
func (*Nuxt) Actions() []provider.Action {
	return []provider.Action{
		{Label: "install", Commands: func(p provider.Package, v string, proj provider.Project) ([]*exec.Cmd, error) {
			return nuxiAdd(p.ID, v, proj, "--skipConfig")
		}},
		{Label: "install + enable", Enables: true, Commands: func(p provider.Package, v string, proj provider.Project) ([]*exec.Cmd, error) {
			return nuxiAdd(p.ID, v, proj)
		}},
	}
}

func (*Nuxt) InstallHint(p provider.Package, version string) string {
	return "npx nuxi@latest module add " + spec(p.ID, version)
}

func spec(npm, version string) string {
	if version != "" {
		return npm + "@" + version
	}
	return npm
}

func nuxiAdd(npm, version string, proj provider.Project, flags ...string) ([]*exec.Cmd, error) {
	if proj.Root == "" {
		return nil, fmt.Errorf("no Nuxt project (package.json depending on nuxt) found from the current directory; run: npx nuxi@latest module add %s", spec(npm, version))
	}
	if !validNpm.MatchString(npm) {
		return nil, fmt.Errorf("invalid package name %q", npm)
	}
	if !validVersion.MatchString(version) {
		return nil, fmt.Errorf("invalid version %q", version)
	}
	args := append([]string{"module", "add", spec(npm, version)}, flags...)
	c := runner(proj.Data["pm"], args...)
	c.Dir = proj.Root
	return []*exec.Cmd{c}, nil
}

// runner runs nuxi through the project's package manager.
func runner(pm string, args ...string) *exec.Cmd {
	nuxi := append([]string{"nuxi@latest"}, args...)
	switch pm {
	case "pnpm":
		return exec.Command("pnpm", append([]string{"dlx"}, nuxi...)...)
	case "bun":
		return exec.Command("bunx", nuxi...)
	default: // npm, yarn (classic has no dlx)
		return exec.Command("npx", append([]string{"--yes"}, nuxi...)...)
	}
}
