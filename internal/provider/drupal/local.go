package drupal

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"fzpm/internal/provider"
)

var _ provider.Lister = Drupal{}

// Local lists the project's modules from files only (no running site):
//   - enabled: the "module" list of core.extension.yml in the config sync dir
//   - present: *.info.yml files below the docroot, plus drupal/* packages in
//     composer.json that are not downloaded yet; versions from composer.lock
func (Drupal) Local(ctx context.Context, _ *http.Client, proj provider.Project) ([]provider.LocalPackage, error) {
	if proj.Root == "" {
		return nil, fmt.Errorf("no Drupal project")
	}
	docroot := webRoot(proj.Root)
	enabled, err := enabledModules(proj.Root, docroot)
	if err != nil {
		return nil, err
	}
	locked := lockPackages(proj.Root)
	versions := map[string]string{}
	for name, p := range locked {
		versions[name] = p.Version
	}
	byName := map[string]provider.LocalPackage{}

	// Modules in the codebase.
	for _, base := range []string{"core/modules", "core/profiles", "modules", "profiles"} {
		scanInfo(ctx, filepath.Join(docroot, base), func(path string, info infoFile) {
			name := strings.TrimSuffix(filepath.Base(path), ".info.yml")
			if info.Type != "module" && info.Type != "profile" {
				return
			}
			if info.Hidden && !enabled[name] {
				return
			}
			rel, _ := filepath.Rel(docroot, filepath.Dir(path))
			lp := provider.LocalPackage{
				Package: provider.Package{Provider: "drupal", Name: info.Name},
				Module:  name,
				Group:   info.Package,
				Enabled: enabled[name],
			}
			switch {
			case strings.HasPrefix(rel, "core"+string(filepath.Separator)):
				lp.Origin, lp.ID, lp.Version = "core", "drupal", versions["core"]
			case strings.Contains(rel, "contrib"+string(filepath.Separator)):
				project := strings.Split(rel, string(filepath.Separator))[2] // modules/contrib/<project>
				lp.Origin, lp.ID, lp.Version = "contrib", project, versions[project]
			default:
				lp.Origin = "custom"
			}
			byName[name] = lp
		})
	}

	// Required in composer.json but not downloaded (yet).
	downloaded := map[string]bool{}
	for _, lp := range byName {
		downloaded[lp.ID] = true
	}
	for _, project := range requiredProjects(proj.Root) {
		if lp, ok := locked[project]; ok && lp.Type != "drupal-module" {
			continue // themes, recipes, libraries, …
		}
		if _, ok := byName[project]; !ok && !downloaded[project] {
			byName[project] = provider.LocalPackage{
				Package: provider.Package{Provider: "drupal", ID: project, Name: project},
				Module:  project,
				Origin:  "contrib",
				Version: versions[project],
				Enabled: enabled[project],
				Group:   "not downloaded",
			}
		}
	}

	out := make([]provider.LocalPackage, 0, len(byName))
	for _, lp := range byName {
		if lp.Name == "" {
			lp.Name = lp.Module
		}
		out = append(out, lp)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

type composerJSON struct {
	Require map[string]string `json:"require"`
	Extra   struct {
		Scaffold struct {
			Locations struct {
				WebRoot string `json:"web-root"`
			} `json:"locations"`
		} `json:"drupal-scaffold"`
	} `json:"extra"`
}

func readComposer(root string) composerJSON {
	var c composerJSON
	if b, err := os.ReadFile(filepath.Join(root, "composer.json")); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

// webRoot finds the docroot: composer scaffold setting, else common names.
func webRoot(root string) string {
	if wr := readComposer(root).Extra.Scaffold.Locations.WebRoot; wr != "" {
		return filepath.Join(root, filepath.Clean(wr))
	}
	for _, d := range []string{"web", "docroot", "html", "public"} {
		if fi, err := os.Stat(filepath.Join(root, d)); err == nil && fi.IsDir() {
			return filepath.Join(root, d)
		}
	}
	return root
}

// requiredProjects lists drupal/* projects required in composer.json.
func requiredProjects(root string) []string {
	var out []string
	for name := range readComposer(root).Require {
		p, ok := strings.CutPrefix(name, "drupal/")
		if !ok || p == "core" || strings.HasPrefix(p, "core-") || !validID.MatchString(p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

type lockedPackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Type    string `json:"type"`
}

// lockPackages maps drupal project names (and "core") to composer.lock
// entries.
func lockPackages(root string) map[string]lockedPackage {
	out := map[string]lockedPackage{}
	b, err := os.ReadFile(filepath.Join(root, "composer.lock"))
	if err != nil {
		return out
	}
	var lock struct {
		Packages []lockedPackage `json:"packages"`
	}
	if json.Unmarshal(b, &lock) != nil {
		return out
	}
	for _, p := range lock.Packages {
		if name, ok := strings.CutPrefix(p.Name, "drupal/"); ok {
			out[name] = p
		}
	}
	return out
}

var syncDirRe = regexp.MustCompile(`\$settings\[\s*['"]config_sync_directory['"]\s*\]\s*=\s*['"]([^'"]+)['"]`)

// enabledModules reads core.extension.yml from the config sync directory.
func enabledModules(root, docroot string) (map[string]bool, error) {
	path := findCoreExtension(root, docroot)
	if path == "" {
		return nil, fmt.Errorf("no core.extension.yml found (export config with `drush cex`)")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ext struct {
		Module  map[string]int `yaml:"module"`
		Profile string         `yaml:"profile"`
	}
	if err := yaml.Unmarshal(b, &ext); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	enabled := make(map[string]bool, len(ext.Module))
	for name := range ext.Module {
		enabled[name] = true
	}
	return enabled, nil
}

func findCoreExtension(root, docroot string) string {
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	// Common locations first.
	for _, dir := range []string{"config/sync", "config/default", "config/staging"} {
		if p := filepath.Join(root, dir, "core.extension.yml"); exists(p) {
			return p
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(root, "config", "*", "core.extension.yml")); len(matches) > 0 {
		return matches[0]
	}
	// Fall back to the configured sync dir (relative to the docroot). Only
	// that one setting is extracted from settings.php.
	settings, _ := filepath.Glob(filepath.Join(docroot, "sites", "*", "settings*.php"))
	for _, s := range settings {
		b, err := os.ReadFile(s)
		if err != nil {
			continue
		}
		if m := syncDirRe.FindSubmatch(b); m != nil {
			dir := string(m[1])
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(docroot, dir)
			}
			if p := filepath.Join(dir, "core.extension.yml"); exists(p) {
				return p
			}
		}
	}
	return ""
}

type infoFile struct {
	Name    string `yaml:"name"`
	Type    string `yaml:"type"`
	Package string `yaml:"package"`
	Hidden  bool   `yaml:"hidden"`
}

// scanInfo walks dir for *.info.yml, skipping test fixtures and vendor code.
func scanInfo(ctx context.Context, dir string, fn func(path string, info infoFile)) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case "tests", "node_modules", "vendor", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".info.yml") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var info infoFile
		if yaml.Unmarshal(b, &info) == nil {
			fn(path, info)
		}
		return nil
	})
}

// EnableCommands enables (in Drupal terms: installs) a present module.
// Unlike listing, this needs a running site (drush, via ddev if present).
func (Drupal) EnableCommands(lp provider.LocalPackage, proj provider.Project) ([]*exec.Cmd, error) {
	if !validID.MatchString(lp.Module) {
		return nil, fmt.Errorf("invalid module name %q", lp.Module)
	}
	return []*exec.Cmd{drushCmd(proj, "en", lp.Module, "-y")}, nil
}
