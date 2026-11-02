package npmjs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fzpm/internal/provider"
	"fzpm/internal/provider/npm"
)

func TestActionsPerPackageManager(t *testing.T) {
	ps := []provider.Package{{ID: "zod"}, {ID: "@types/node"}}
	for lock, want := range map[string][2]string{
		"":               {"npm install zod @types/node", "npm install --save-dev zod @types/node"},
		"pnpm-lock.yaml": {"pnpm add zod @types/node", "pnpm add --save-dev zod @types/node"},
		"yarn.lock":      {"yarn add zod @types/node", "yarn add --dev zod @types/node"},
		"bun.lock":       {"bun add zod @types/node", "bun add --dev zod @types/node"},
	} {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, "package.json"), []byte(`{}`), 0o644)
		if lock != "" {
			os.WriteFile(filepath.Join(root, lock), nil, 0o644)
		}
		proj, ok := NPM{}.Detect(root)
		if !ok {
			t.Fatal("not detected")
		}
		for i, a := range (NPM{}).Actions() {
			cmds, err := a.Commands(ps, "", proj)
			if err != nil || strings.Join(cmds[0].Args, " ") != want[i] || cmds[0].Dir != root {
				t.Errorf("%s action %d = %v, %v; want %q", lock, i, cmds, err, want[i])
			}
		}
	}
}

func TestLocalAndRemove(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"dependencies":{"zod":"^4.0.0"},"devDependencies":{"vitest":"^3"}}`), 0o644)
	os.WriteFile(filepath.Join(root, "pnpm-lock.yaml"), nil, 0o644)
	os.MkdirAll(filepath.Join(root, "node_modules", "zod"), 0o755)
	os.WriteFile(filepath.Join(root, "node_modules", "zod", "package.json"), []byte(`{"version":"4.6.7"}`), 0o644)
	proj, _ := NPM{}.Detect(root)
	lps, err := NPM{}.Local(context.Background(), nil, proj)
	if err != nil || len(lps) != 2 || lps[1].ID != "zod" || lps[1].Version != "4.6.7" || lps[0].Version != "^3" || lps[0].Group != "devDependencies" {
		t.Fatalf("local = %+v, %v", lps, err)
	}
	cmds, err := NPM{}.RemoveCommands(lps, proj)
	if err != nil || strings.Join(cmds[0].Args, " ") != "pnpm remove vitest zod" {
		t.Errorf("remove = %v, %v", cmds, err)
	}
}

func TestSearchAndDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search":
			if r.URL.Query().Get("text") != "zod" {
				t.Errorf("search query = %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"objects":[{"downloads":{"weekly":442969207},"package":{"name":"zod","description":"Schemas","date":"2026-10-12T16:29:37.778Z"}}]}`))
		case r.URL.Path == "/dl":
			w.Write([]byte(`{"packages":[{"packageName":"zod","versions":[{"version":"4.6.7","downloads":400}]}]}`))
		case r.URL.Path == "/zod":
			w.Write([]byte(`{"name":"zod","description":"Schemas","dist-tags":{"latest":"4.6.7"},
				"time":{"created":"2020-03-07T00:00:00Z","modified":"2026-10-12T16:29:37Z","4.6.7":"2026-10-12T16:29:37Z"},
				"versions":{"4.6.7":{}},"readme":"# Zod","license":"MIT","repository":{"url":"git+https://github.com/colinhacks/zod.git"},
				"maintainers":[{"name":"colinhacks"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	oldS, oldR, oldD := npm.SearchURL, npm.RegistryURL, npm.DownloadsURL
	npm.SearchURL, npm.RegistryURL, npm.DownloadsURL = srv.URL+"/search", srv.URL+"/", srv.URL+"/dl?packages="
	defer func() { npm.SearchURL, npm.RegistryURL, npm.DownloadsURL = oldS, oldR, oldD }()
	ctx, c := context.Background(), srv.Client()

	ps, err := NPM{}.Search(ctx, c, "zod")
	if err != nil || len(ps) != 1 || ps[0].Provider != "npm" || ps[0].Popularity != 442969207 {
		t.Errorf("search = %+v, %v", ps, err)
	}
	d, err := NPM{}.Detail(ctx, c, "zod")
	if err != nil {
		t.Fatal(err)
	}
	if d.Body != "# Zod" || d.Popularity != 400 || len(d.Releases) != 1 || d.Releases[0].Constraint != "^4.6.7" {
		t.Errorf("detail = %+v", d)
	}
	var dev []string
	for _, s := range d.Sidebar {
		if s.Title == "Development" {
			for _, l := range s.Items {
				dev = append(dev, l.URL)
			}
		}
	}
	if len(dev) != 3 || dev[0] != "https://github.com/colinhacks/zod" {
		t.Errorf("development = %v", dev)
	}
}
