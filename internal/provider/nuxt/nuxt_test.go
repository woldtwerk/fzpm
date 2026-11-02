package nuxt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fzpm/internal/provider"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectAndActions(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{"devDependencies":{"nuxt":"^4.0.0"}}`)
	write(t, root, "pnpm-lock.yaml", "")
	n := &Nuxt{}
	proj, ok := n.Detect(filepath.Join(root, "app", "pages"))
	if !ok || proj.Root != root || proj.Data["pm"] != "pnpm" {
		t.Fatalf("detect = %+v %v", proj, ok)
	}
	cmds, err := n.Actions()[0].Commands([]provider.Package{{ID: "@nuxt/image"}}, "^2.2.1", proj)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cmds[0].Args, " "); got != "pnpm dlx nuxi@latest module add @nuxt/image@^2.2.1 --skipConfig" || cmds[0].Dir != root {
		t.Errorf("install cmd = %q", got)
	}
	cmds, _ = n.Actions()[1].Commands([]provider.Package{{ID: "@nuxt/image"}}, "", proj)
	if got := strings.Join(cmds[0].Args, " "); got != "pnpm dlx nuxi@latest module add @nuxt/image" {
		t.Errorf("install+enable cmd = %q", got)
	}
	cmds, _ = n.Actions()[1].Commands([]provider.Package{{ID: "@nuxt/image"}, {ID: "@nuxt/ui"}}, "", proj)
	if got := strings.Join(cmds[0].Args, " "); len(cmds) != 1 || got != "pnpm dlx nuxi@latest module add @nuxt/image @nuxt/ui" {
		t.Errorf("batch cmd = %q", got)
	}
	if _, err := n.Actions()[0].Commands([]provider.Package{{ID: "@nuxt/ui"}, {ID: "x; rm -rf /"}}, "", proj); err == nil {
		t.Error("expected invalid name error")
	}
	rm := []provider.LocalPackage{{Package: provider.Package{ID: "@nuxt/image"}, Module: "@nuxt/image"}, {Package: provider.Package{ID: "@nuxt/ui"}, Module: "@nuxt/ui"}}
	cmds, _ = n.DisableCommands(rm, proj)
	if got := strings.Join(cmds[0].Args, " "); got != "pnpm dlx nuxi@latest module remove @nuxt/image @nuxt/ui --skipInstall" || cmds[0].Dir != root {
		t.Errorf("disable cmd = %q", got)
	}
	cmds, _ = n.RemoveCommands(rm, proj)
	if got := strings.Join(cmds[0].Args, " "); got != "pnpm dlx nuxi@latest module remove @nuxt/image @nuxt/ui --skipConfig" {
		t.Errorf("remove cmd = %q", got)
	}
	if _, err := n.DisableCommands([]provider.LocalPackage{{Module: "./modules/mine"}}, proj); err == nil {
		t.Error("disabling a local module must fail")
	}
	if _, ok := n.Detect(t.TempDir()); ok {
		t.Error("detected nuxt in empty dir")
	}
}

func TestLocal(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{
		"dependencies": {"nuxt": "^4", "@nuxt/image": "^2.0.0", "vue": "^3", "@pinia/nuxt": "^0.11", "nuxt-og-image": "^5"},
		"devDependencies": {"@nuxt/eslint": "^1", "some-module": "^1", "@nuxt/kit": "^4"}
	}`)
	write(t, root, "nuxt.config.ts", `export default defineNuxtConfig({
  // '@nuxt/eslint' is commented out
  modules: [
    '@nuxt/image',
    ['@pinia/nuxt', { autoImports: ['defineStore'] }],
    /* 'nuxt-og-image', */
  ],
  image: { provider: 'ipx' },
})`)
	write(t, root, "node_modules/@nuxt/image/package.json", `{"version":"2.2.1"}`)
	write(t, root, "node_modules/some-module/package.json", `{"version":"1.0.0","dependencies":{"@nuxt/kit":"^4"}}`)
	write(t, root, "modules/my-local.ts", "export default {}")

	// The official nuxt.com list (normally fetched from api.nuxt.com).
	n := &Nuxt{}
	n.db.byNpm = map[string]module{
		"@nuxt/image":   {Name: "image", Npm: "@nuxt/image", Type: "official"},
		"@nuxt/eslint":  {Name: "eslint", Npm: "@nuxt/eslint", Type: "official"},
		"nuxt-og-image": {Name: "og-image", Npm: "nuxt-og-image", Type: "community"},
	}
	n.db.fetched = time.Now()
	list, err := n.Local(context.Background(), nil, provider.Project{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]provider.LocalPackage{}
	for _, lp := range list {
		got[lp.Module] = lp
	}
	check := func(mod, name, origin string, enabled bool, version string) {
		t.Helper()
		lp, ok := got[mod]
		if !ok || lp.Name != name || lp.Origin != origin || lp.Enabled != enabled || (version != "" && lp.Version != version) {
			t.Errorf("%s = %+v (present=%v)", mod, lp, ok)
		}
	}
	check("@nuxt/image", "image", "official", true, "2.2.1")   // listed + configured
	check("@pinia/nuxt", "@pinia/nuxt", "npm", true, "^0.11")  // not listed, but configured
	check("nuxt-og-image", "og-image", "community", false, "") // listed, config entry commented out
	check("@nuxt/eslint", "eslint", "official", false, "")     // listed, not configured
	check("./modules/my-local", "my-local", "custom", true, "")
	// Not on nuxt.com and not configured: not a module, whatever it depends on.
	for _, not := range []string{"some-module", "nuxt", "vue", "@nuxt/kit", "defineStore", "ipx"} {
		if _, ok := got[not]; ok {
			t.Errorf("%s must not be listed", not)
		}
	}
}

func TestBuildDetail(t *testing.T) {
	var doc npmDoc
	json.Unmarshal([]byte(`{
		"name": "@nuxt/image",
		"dist-tags": {"latest": "2.2.1", "rc": "3.0.0-rc.1"},
		"time": {"created": "2020-01-01T00:00:00Z", "2.2.0": "2026-10-19T17:23:23Z", "2.2.1": "2026-10-30T16:58:07Z", "3.0.0-rc.1": "2026-10-31T00:00:00Z", "1.0.0": "2021-01-01T00:00:00Z"},
		"versions": {"2.2.0": {}, "2.2.1": {}, "3.0.0-rc.1": {}, "1.0.0": {"deprecated": "use v2"}},
		"license": "MIT",
		"bugs": {"url": "https://github.com/nuxt/image/issues"},
		"readme": "<p align=\"center\"><img src=\"./docs/logo.png\" alt=\"Logo\" width=\"300\"></p>\n\n[![npm](https://img.shields.io/npm/v/@nuxt/image)](https://npmjs.com)\n\n# Nuxt Image\n\n![shot](https://github.com/nuxt/image/blob/main/shot.png)\n\n`+"```html\\n<img src=\\\"keep.png\\\">\\n```"+`"
	}`), &doc)
	var mod module
	json.Unmarshal([]byte(`{"name":"image","npm":"@nuxt/image","repo":"nuxt/image#main","github":"https://github.com/nuxt/image",
		"website":"https://image.nuxt.com","category":"Images","type":"official",
		"maintainers":[{"name":"Daniel Roe","github":"danielroe"}],"compatibility":{"nuxt":">=3.0.0"},"stats":{"downloads":1234567,"stars":1500}}`), &mod)

	d := buildDetail("@nuxt/image", doc, mod, true)
	if d.Name != "image" || d.URL != "https://nuxt.com/modules/image" || d.Popularity != 1234567 {
		t.Errorf("package = %+v url=%s", d.Package, d.URL)
	}
	for _, want := range []string{
		"![Logo](https://raw.githubusercontent.com/nuxt/image/main/docs/logo.png)",
		"![shot](https://raw.githubusercontent.com/nuxt/image/main/shot.png)",
		`<img src="keep.png">`, // code block untouched
	} {
		if !strings.Contains(d.Body, want) {
			t.Errorf("body missing %q:\n%s", want, d.Body)
		}
	}
	if strings.Contains(d.Body, "shields.io") || strings.Contains(d.Body, "<p") {
		t.Errorf("badge/html left in body:\n%s", d.Body)
	}
	if len(d.Releases) != 4 || d.Releases[0].Version != "3.0.0-rc.1" || d.Releases[0].Constraint != "3.0.0-rc.1" ||
		d.Releases[1].Constraint != "^2.2.1" || !strings.Contains(strings.Join(d.Releases[1].Tags, ","), "latest") ||
		!strings.Contains(strings.Join(d.Releases[3].Tags, ","), "deprecated") {
		t.Errorf("releases = %+v", d.Releases)
	}
	sections := map[string]provider.Section{}
	for _, s := range d.Sidebar {
		sections[s.Title] = s
	}
	if m := sections["Maintainers"].Items; len(m) != 1 || m[0].Image != "https://github.com/danielroe.png?size=80" {
		t.Errorf("maintainers = %+v", m)
	}
	if !strings.Contains(sections["Module information"].Items[2].Label, ">=3.0.0") {
		t.Errorf("info = %+v", sections["Module information"].Items)
	}
	if dev := sections["Development"].Items; len(dev) != 3 || dev[1].URL != "https://github.com/nuxt/image/issues" {
		t.Errorf("development = %+v", dev)
	}
}

func TestCleanReadmeReferences(t *testing.T) {
	md := "# Title\n\n- `<nuxt-picture>` replaces `<picture>`\n\n[![npm version][npm-version-src]][npm-version-href]\n[![License][license-src]][license-href]\n\nSee the [docs][docs] and ![Shot][shot].\n\n[npm-version-src]: https://npmx.dev/api/registry/badge/version/@nuxt/image\n[npm-version-href]: https://npmx.dev/package/@nuxt/image\n[license-src]: https://img.shields.io/npm/l/x.svg\n[license-href]: https://npmjs.com\n[docs]: https://image.nuxt.com\n[shot]: ./shot.png \"Screenshot\"\n"
	out := cleanReadme(md, "https://raw.githubusercontent.com/nuxt/image/main/")
	for _, bad := range []string{"badge", "shields", "[npm-version", "]: "} {
		if strings.Contains(out, bad) {
			t.Errorf("leftover %q in:\n%s", bad, out)
		}
	}
	for _, want := range []string{"`<nuxt-picture>` replaces `<picture>`", "[docs](https://image.nuxt.com)", "![Shot](https://raw.githubusercontent.com/nuxt/image/main/shot.png)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestIndexUsesNpmxDownloads(t *testing.T) {
	failed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/modules":
			w.Write([]byte(`{"modules":[
				{"name":"og-image","npm":"nuxt-og-image","stats":{"downloads":0}},
				{"name":"security","npm":"nuxt-security","stats":{"downloads":0}},
				{"name":"image","npm":"@nuxt/image","stats":{"downloads":0}},
				{"name":"gone","npm":"nuxt-gone","stats":{"downloads":42}}]}`))
		case "/dl":
			names := strings.Split(r.URL.Query().Get("packages"), ",")
			if len(names) > 1 && !failed { // first full batch fails: split + retry
				failed = true
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			weekly := map[string]string{
				"nuxt-og-image": `[{"version":"5.0.0","downloads":100000},{"version":"4.0.0","downloads":19381}]`,
				"nuxt-security": `[{"version":"2.0.0","downloads":67005}]`,
				"@nuxt/image":   `[{"version":"2.2.1","downloads":500000},{"version":"1.11.2","downloads":12345}]`,
			}
			var parts []string
			for _, n := range names {
				if v, ok := weekly[n]; ok {
					parts = append(parts, `{"packageName":"`+n+`","versions":`+v+`}`)
				}
			}
			w.Write([]byte(`{"packages":[` + strings.Join(parts, ",") + `]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	oldM, oldD, oldP := modulesURL, downloadsURL, retryPause
	modulesURL, downloadsURL, retryPause = srv.URL+"/modules", srv.URL+"/dl?packages=", 0
	defer func() { modulesURL, downloadsURL, retryPause = oldM, oldD, oldP }()

	got := map[string]int64{}
	batches := 0
	err := (&Nuxt{}).Index(context.Background(), srv.Client(), func(p []provider.Package) {
		batches++
		for _, pkg := range p {
			if pkg.Popularity != 0 || got[pkg.ID] == 0 {
				got[pkg.ID] = pkg.Popularity
			}
		}
	})
	if err != nil || batches < 2 || !failed {
		t.Fatalf("err=%v batches=%d failedOnce=%v", err, batches, failed)
	}
	want := map[string]int64{"nuxt-og-image": 119381, "nuxt-security": 67005, "@nuxt/image": 512345, "nuxt-gone": 42}
	for id, n := range want {
		if got[id] != n {
			t.Errorf("%s = %d, want %d", id, got[id], n)
		}
	}
}
