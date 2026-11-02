package nuxt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"fzpm/internal/provider"
)

var _ provider.Lister = (*Nuxt)(nil)

// Local lists the project's Nuxt modules. Which dependencies are Nuxt
// modules is decided by the official list on nuxt.com/modules (fetched once
// per session) plus whatever nuxt.config registers; the rest comes from files:
//   - enabled: string entries of the `modules: [...]` array in nuxt.config,
//     plus local modules in ./modules (Nuxt registers those automatically)
//   - present: package.json dependencies
//
// Without network access only modules registered in nuxt.config are found.
func (n *Nuxt) Local(ctx context.Context, c *http.Client, proj provider.Project) ([]provider.LocalPackage, error) {
	pkg, ok := readPackageJSON(proj.Root)
	if !ok {
		return nil, fmt.Errorf("no package.json in %s", proj.Root)
	}
	enabled := configModules(proj.Root)
	listed, _ := n.db.get(ctx, c) // nil when offline
	var out []provider.LocalPackage
	add := func(deps map[string]string, group string) {
		for name, rng := range deps {
			mod, onNuxtCom := listed[name]
			if !onNuxtCom && !enabled[name] {
				continue
			}
			version := installedVersion(proj.Root, name)
			if version == "" {
				version = rng
			}
			lp := provider.LocalPackage{
				Package: provider.Package{Provider: "nuxt", ID: name, Name: name},
				Module:  name,
				Group:   group,
				Origin:  "npm",
				Version: version,
				Enabled: enabled[name],
			}
			if onNuxtCom {
				lp.Name = mod.Name
				lp.Origin = mod.Type // official, community, 3rd-party
			}
			out = append(out, lp)
		}
	}
	add(pkg.Dependencies, "dependencies")
	add(pkg.DevDependencies, "devDependencies")

	// Local modules in ./modules are auto-registered.
	entries, _ := os.ReadDir(filepath.Join(proj.Root, "modules"))
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		out = append(out, provider.LocalPackage{
			Package: provider.Package{Provider: "nuxt", Name: name},
			Module:  "./modules/" + name,
			Group:   "local",
			Origin:  "custom",
			Enabled: true,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// basePackage strips a subpath from an npm module specifier:
// "@scope/pkg/nuxt" → "@scope/pkg", "pkg/nuxt" → "pkg". Paths and aliases
// are returned as they are.
func basePackage(spec string) string {
	if strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") || strings.HasPrefix(spec, "~") {
		return spec
	}
	parts := strings.SplitN(spec, "/", 3)
	if strings.HasPrefix(spec, "@") && len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

func installedVersion(root, name string) string {
	var p struct {
		Version string `json:"version"`
	}
	b, err := os.ReadFile(filepath.Join(root, "node_modules", name, "package.json"))
	if err != nil || json.Unmarshal(b, &p) != nil {
		return ""
	}
	return p.Version
}

var (
	modulesKeyRe = regexp.MustCompile(`(?m)^\s*modules\s*:\s*\[`)
	commentRe    = regexp.MustCompile(`(?m)/\*[\s\S]*?\*/|^\s*//.*$|\s//.*$`)
	quotedRe     = regexp.MustCompile(`['"\x60]([^'"\x60\s]+)['"\x60]`)
)

// configModules extracts the module names listed in nuxt.config's top-level
// `modules` array. It is a text scan, not a JS parser: module names must be
// string literals (the common case). Entries pointing into a package
// ("@vercel/analytics/nuxt") also mark the package itself.
func configModules(root string) map[string]bool {
	out := map[string]bool{}
	for _, name := range []string{"nuxt.config.ts", "nuxt.config.js", "nuxt.config.mjs", "nuxt.config.mts"} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		src := commentRe.ReplaceAllString(string(b), "")
		loc := modulesKeyRe.FindStringIndex(src)
		if loc == nil {
			return out
		}
		// Find the matching closing bracket.
		depth, end := 0, len(src)
		for i := loc[1] - 1; i < len(src); i++ {
			switch src[i] {
			case '[':
				depth++
			case ']':
				depth--
			}
			if depth == 0 {
				end = i
				break
			}
		}
		for _, m := range quotedRe.FindAllStringSubmatch(src[loc[1]:end], -1) {
			out[m[1]] = true
			out[basePackage(m[1])] = true
		}
		return out
	}
	return out
}

// EnableCommands registers installed modules in nuxt.config.
func (*Nuxt) EnableCommands(lps []provider.LocalPackage, proj provider.Project) ([]*exec.Cmd, error) {
	modules := make([]string, len(lps))
	for i, lp := range lps {
		modules[i] = lp.Module
	}
	return nuxiAdd(modules, "", proj, "--skipInstall")
}

// DisableCommands takes modules out of nuxt.config, keeping the packages.
func (*Nuxt) DisableCommands(lps []provider.LocalPackage, proj provider.Project) ([]*exec.Cmd, error) {
	return nuxiRemove(lps, proj, "--skipInstall")
}

// RemoveCommands uninstalls the modules' packages (the UI disables enabled
// ones first).
func (*Nuxt) RemoveCommands(lps []provider.LocalPackage, proj provider.Project) ([]*exec.Cmd, error) {
	return nuxiRemove(lps, proj, "--skipConfig")
}

func nuxiRemove(lps []provider.LocalPackage, proj provider.Project, flag string) ([]*exec.Cmd, error) {
	args := []string{"module", "remove"}
	for _, lp := range lps {
		if lp.ID == "" {
			return nil, fmt.Errorf("%s is custom code; remove it by hand", lp.Module)
		}
		if !validNpm.MatchString(lp.ID) {
			return nil, fmt.Errorf("invalid package name %q", lp.ID)
		}
		args = append(args, lp.ID)
	}
	c := runner(proj.Data["pm"], append(args, flag)...)
	c.Dir = proj.Root
	return []*exec.Cmd{c}, nil
}
