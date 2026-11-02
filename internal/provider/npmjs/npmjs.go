// Package npmjs implements the "npm" provider for all packages on the npm
// registry. It steps back in projects a specific npm-based provider (Nuxt)
// detects.
package npmjs

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"sort"

	"fzpm/internal/provider"
	"fzpm/internal/provider/npm"
)

func init() { provider.Register(NPM{}) }

// NPM is the generic npm package provider. Package IDs are npm names.
type NPM struct{}

func (NPM) Name() string { return "npm" }
func (NPM) Generic()     {}

// Icon is Nerd Fonts' nf-dev-npm in npm red.
func (NPM) Icon() (string, string) { return "\ue71e", "#cb3837" }

// Detect walks up from dir to the next package.json.
func (NPM) Detect(dir string) (provider.Project, bool) {
	return npm.Find(dir, func(npm.PackageJSON) bool { return true })
}

// Index: the registry has no popularity listing, so the index starts empty
// and fills with search results (which are cached like an index).
func (NPM) Index(context.Context, *http.Client, func([]provider.Package)) error {
	return nil
}

func (NPM) Search(ctx context.Context, c *http.Client, query string) ([]provider.Package, error) {
	return npm.Search(ctx, c, "npm", query)
}

func (NPM) Detail(ctx context.Context, c *http.Client, id string) (*provider.Detail, error) {
	return npm.Detail(ctx, c, "npm", id)
}

// Actions: [0] add as dependency, [1] add as dev dependency, with the
// project's package manager.
func (NPM) Actions() []provider.Action {
	return []provider.Action{
		{Label: "install", Commands: func(ps []provider.Package, v string, proj provider.Project) ([]*exec.Cmd, error) {
			return add(ps, v, proj, false)
		}},
		{Label: "install (dev)", Commands: func(ps []provider.Package, v string, proj provider.Project) ([]*exec.Cmd, error) {
			return add(ps, v, proj, true)
		}},
	}
}

func (NPM) InstallHint(p provider.Package, version string) string {
	return "npm install " + npm.Spec(p.ID, version)
}

// verbs are the add / remove subcommands and the dev flag per package
// manager.
var verbs = map[string][3]string{
	"npm":  {"install", "uninstall", "--save-dev"},
	"pnpm": {"add", "remove", "--save-dev"},
	"yarn": {"add", "remove", "--dev"},
	"bun":  {"add", "remove", "--dev"},
}

func pmCmd(proj provider.Project, args ...string) *exec.Cmd {
	c := exec.Command(proj.Data["pm"], args...)
	c.Dir = proj.Root
	return c
}

func add(ps []provider.Package, version string, proj provider.Project, dev bool) ([]*exec.Cmd, error) {
	if len(ps) == 0 {
		return nil, fmt.Errorf("no packages to install")
	}
	if proj.Root == "" {
		return nil, fmt.Errorf("no package.json found from the current directory; run: npm install %s", npm.Spec(ps[0].ID, version))
	}
	if !npm.ValidVersion.MatchString(version) {
		return nil, fmt.Errorf("invalid version %q", version)
	}
	v, ok := verbs[proj.Data["pm"]]
	if !ok {
		return nil, fmt.Errorf("unknown package manager %q", proj.Data["pm"])
	}
	args := []string{v[0]}
	if dev {
		args = append(args, v[2])
	}
	for _, p := range ps {
		if !npm.ValidName.MatchString(p.ID) {
			return nil, fmt.Errorf("invalid package name %q", p.ID)
		}
		args = append(args, npm.Spec(p.ID, version))
	}
	return []*exec.Cmd{pmCmd(proj, args...)}, nil
}

var (
	_ provider.Lister  = NPM{}
	_ provider.Remover = NPM{}
)

// Local lists package.json dependencies with installed versions.
func (NPM) Local(_ context.Context, _ *http.Client, proj provider.Project) ([]provider.LocalPackage, error) {
	pkg, ok := npm.ReadPackageJSON(proj.Root)
	if !ok {
		return nil, fmt.Errorf("no package.json in %s", proj.Root)
	}
	var out []provider.LocalPackage
	add := func(deps map[string]string, group string) {
		for name, rng := range deps {
			v := npm.InstalledVersion(proj.Root, name)
			if v == "" {
				v = rng
			}
			out = append(out, provider.LocalPackage{
				Package: provider.Package{Provider: "npm", ID: name, Name: name},
				Module:  name,
				Group:   group,
				Origin:  "npm",
				Version: v,
				Enabled: true,
			})
		}
	}
	add(pkg.Dependencies, "dependencies")
	add(pkg.DevDependencies, "devDependencies")
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// EnableCommands: dependencies are always in use.
func (NPM) EnableCommands([]provider.LocalPackage, provider.Project) ([]*exec.Cmd, error) {
	return nil, fmt.Errorf("npm packages are in use once installed")
}

// RemoveCommands removes the packages with the project's package manager.
func (NPM) RemoveCommands(lps []provider.LocalPackage, proj provider.Project) ([]*exec.Cmd, error) {
	v, ok := verbs[proj.Data["pm"]]
	if !ok {
		return nil, fmt.Errorf("unknown package manager %q", proj.Data["pm"])
	}
	args := []string{v[1]}
	for _, lp := range lps {
		if !npm.ValidName.MatchString(lp.ID) {
			return nil, fmt.Errorf("invalid package name %q", lp.ID)
		}
		args = append(args, lp.ID)
	}
	return []*exec.Cmd{pmCmd(proj, args...)}, nil
}
