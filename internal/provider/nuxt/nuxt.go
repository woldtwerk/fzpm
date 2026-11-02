// Package nuxt implements a provider for Nuxt modules (nuxt.com/modules).
package nuxt

import (
	"fmt"
	"os/exec"

	"fzpm/internal/provider"
	"fzpm/internal/provider/npm"
)

// Endpoint (variable so tests can point it at a local server).
var modulesURL = "https://api.nuxt.com/modules"

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
	return npm.Find(dir, func(p npm.PackageJSON) bool { return p.Has("nuxt") })
}

// Actions: [0] install the package only, [1] install and register it in
// nuxt.config (what `nuxi module add` does by default). Several modules go
// into a single `nuxi module add`.
func (*Nuxt) Actions() []provider.Action {
	return []provider.Action{
		{Label: "install", Commands: func(ps []provider.Package, v string, proj provider.Project) ([]*exec.Cmd, error) {
			return nuxiAdd(ids(ps), v, proj, "--skipConfig")
		}},
		{Label: "install + enable", Enables: true, Commands: func(ps []provider.Package, v string, proj provider.Project) ([]*exec.Cmd, error) {
			return nuxiAdd(ids(ps), v, proj)
		}},
	}
}

func ids(ps []provider.Package) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

func (*Nuxt) InstallHint(p provider.Package, version string) string {
	return "npx nuxi@latest module add " + npm.Spec(p.ID, version)
}

func nuxiAdd(npms []string, version string, proj provider.Project, flags ...string) ([]*exec.Cmd, error) {
	if len(npms) == 0 {
		return nil, fmt.Errorf("no modules to install")
	}
	if proj.Root == "" {
		return nil, fmt.Errorf("no Nuxt project (package.json depending on nuxt) found from the current directory; run: npx nuxi@latest module add %s", npm.Spec(npms[0], version))
	}
	if !npm.ValidVersion.MatchString(version) {
		return nil, fmt.Errorf("invalid version %q", version)
	}
	args := []string{"module", "add"}
	for _, name := range npms {
		if !npm.ValidName.MatchString(name) {
			return nil, fmt.Errorf("invalid package name %q", name)
		}
		args = append(args, npm.Spec(name, version))
	}
	args = append(args, flags...)
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
