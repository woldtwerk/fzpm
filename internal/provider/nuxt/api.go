package nuxt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"fzpm/internal/provider"
	"fzpm/internal/provider/npm"
	"fzpm/internal/provider/readme"
)

// module is an entry of the nuxt.com module list.
type module struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Repo        string `json:"repo"`
	Npm         string `json:"npm"`
	Github      string `json:"github"`
	Website     string `json:"website"`
	LearnMore   string `json:"learn_more"`
	Category    string `json:"category"`
	Type        string `json:"type"`
	Maintainers []struct {
		Name   string `json:"name"`
		Github string `json:"github"`
	} `json:"maintainers"`
	Compatibility struct {
		Nuxt string `json:"nuxt"`
	} `json:"compatibility"`
	Stats struct {
		Version     string `json:"version"`
		Downloads   int64  `json:"downloads"`
		Stars       int64  `json:"stars"`
		PublishedAt int64  `json:"publishedAt"`
		CreatedAt   int64  `json:"createdAt"`
	} `json:"stats"`
}

// modulesDB caches the nuxt.com module list in memory for an hour.
type modulesDB struct {
	mu      sync.Mutex
	byNpm   map[string]module
	fetched time.Time
}

func (db *modulesDB) get(ctx context.Context, c *http.Client) (map[string]module, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.byNpm != nil && time.Since(db.fetched) < time.Hour {
		return db.byNpm, nil
	}
	r, err := provider.Fetch(ctx, c, modulesURL)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var doc struct {
		Modules []module `json:"modules"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("nuxt.com modules: %w", err)
	}
	db.byNpm = make(map[string]module, len(doc.Modules))
	for _, m := range doc.Modules {
		if m.Npm != "" {
			db.byNpm[m.Npm] = m
		}
	}
	db.fetched = time.Now()
	return db.byNpm, nil
}

// Index: one request returns every module listed on nuxt.com. nuxt.com's
// download counts are mostly missing (0), so they are replaced with weekly
// downloads from npmx.dev in a second pass.
func (n *Nuxt) Index(ctx context.Context, c *http.Client, emit func([]provider.Package)) error {
	mods, err := n.db.get(ctx, c)
	if err != nil {
		return err
	}
	out := make([]provider.Package, 0, len(mods))
	names := make([]string, 0, len(mods))
	for _, m := range mods {
		out = append(out, toPackage(m))
		names = append(names, m.Npm)
	}
	emit(out) // show results right away

	byID := make(map[string]int, len(out))
	for i, p := range out {
		byID[p.ID] = i
	}
	npm.WeeklyDownloads(ctx, c, names, func(d map[string]int64) {
		batch := make([]provider.Package, 0, len(d))
		for id, n := range d {
			if i, ok := byID[id]; ok {
				out[i].Popularity = n
				batch = append(batch, out[i])
			}
		}
		emit(batch)
	})
	return nil
}

func toPackage(m module) provider.Package {
	return provider.Package{
		Provider:   "nuxt",
		ID:         m.Npm,
		Name:       m.Name,
		Summary:    m.Description,
		Popularity: m.Stats.Downloads,
		Created:    time.UnixMilli(m.Stats.CreatedAt),
		Updated:    time.UnixMilli(m.Stats.PublishedAt),
	}
}

// Search: the index already holds the complete nuxt.com list.
func (*Nuxt) Search(context.Context, *http.Client, string) ([]provider.Package, error) {
	return nil, nil
}

// Detail combines the npm registry (README, releases) with nuxt.com
// metadata (maintainers, category, compatibility, links).
func (n *Nuxt) Detail(ctx context.Context, c *http.Client, id string) (*provider.Detail, error) {
	if !npm.ValidName.MatchString(id) {
		return nil, fmt.Errorf("invalid package name %q", id)
	}
	var (
		doc npm.Doc
		mod module
		ok  bool
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		doc, err = npm.Fetch(gctx, c, id)
		return err
	})
	g.Go(func() error {
		mods, err := n.db.get(gctx, c)
		if err == nil { // nuxt.com metadata is optional
			mod, ok = mods[id]
		}
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	// Gaps in the sources: npm often has no README for packages published
	// from monorepos, and nuxt.com mostly reports 0 downloads; npmx.dev's
	// weekly count is used throughout (as in the index).
	repo := mod.Repo
	if !ok {
		repo = readme.GitHubRepo(npm.StringOrURL(doc.Repository))
	}
	g, gctx = errgroup.WithContext(ctx)
	if strings.TrimSpace(doc.Readme) == "" || strings.HasPrefix(doc.Readme, "ERROR: No README") {
		g.Go(func() error {
			doc.Readme = readme.Fetch(gctx, c, readme.RawBase(repo))
			return nil
		})
	}
	var downloads int64
	g.Go(func() error {
		if d, err := npm.Downloads(gctx, c, []string{id}); err == nil {
			downloads = d[id]
		}
		return nil
	})
	g.Wait()
	if downloads > 0 {
		mod.Stats.Downloads = downloads
	}
	d := buildDetail(id, doc, mod, ok)
	if !ok && downloads > 0 {
		d.Popularity = downloads
	}
	return d, nil
}

func buildDetail(id string, doc npm.Doc, mod module, listed bool) *provider.Detail {
	d := &provider.Detail{
		Package: provider.Package{Provider: "nuxt", ID: id, Name: id, Summary: doc.Description},
		URL:     "https://www.npmjs.com/package/" + id,

		PopularityLabel: "downloads / week",
	}
	repo := mod.Repo
	if listed {
		d.Package = toPackage(mod)
		d.URL = "https://nuxt.com/modules/" + mod.Name
		d.PopularityLabel = "downloads / week"
	} else {
		repo = readme.GitHubRepo(npm.StringOrURL(doc.Repository))
	}
	d.Body = readme.Clean(doc.Readme, readme.RawBase(repo))
	d.Images = readme.Images(d.Body)
	d.Releases = npm.Releases(doc)

	latest := doc.DistTags["latest"]
	info := provider.Section{Title: "Module information"}
	add := func(label string) { info.Items = append(info.Items, provider.Link{Label: label}) }
	if listed {
		add("Category: " + mod.Category)
		add("Type: " + mod.Type)
		if mod.Compatibility.Nuxt != "" {
			add("Nuxt: " + mod.Compatibility.Nuxt)
		}
		add(fmt.Sprintf("%s downloads / week", groupDigits(mod.Stats.Downloads)))
		add(fmt.Sprintf("%s GitHub stars", groupDigits(mod.Stats.Stars)))
	}
	if latest != "" {
		add("Latest: " + latest)
	}
	if lic := npm.StringOrURL(doc.License); lic != "" {
		add("License: " + lic)
	}
	add("npm: " + id)
	d.Sidebar = append(d.Sidebar, info)

	maint := provider.Section{Title: "Maintainers"}
	if listed {
		for _, m := range mod.Maintainers {
			l := provider.Link{Label: m.Name}
			if m.Github != "" {
				l.URL = "https://github.com/" + m.Github
				l.Image = "https://github.com/" + m.Github + ".png?size=80"
				if l.Label == "" {
					l.Label = m.Github
				}
			}
			maint.Items = append(maint.Items, l)
		}
	} else {
		for _, m := range doc.Maintainers {
			maint.Items = append(maint.Items, provider.Link{Label: m.Name, URL: "https://www.npmjs.com/~" + m.Name})
		}
	}
	if len(maint.Items) > 0 {
		d.Sidebar = append(d.Sidebar, maint)
	}

	res := provider.Section{Title: "Resources"}
	link := func(s *provider.Section, label, u string) {
		if u != "" {
			s.Items = append(s.Items, provider.Link{Label: label, URL: u})
		}
	}
	if listed {
		link(&res, "Website", mod.Website)
		link(&res, "Documentation", mod.LearnMore)
		link(&res, "nuxt.com", d.URL)
	} else {
		link(&res, "Homepage", doc.Homepage)
	}
	link(&res, "npm", "https://www.npmjs.com/package/"+id)
	d.Sidebar = append(d.Sidebar, res)

	dev := provider.Section{Title: "Development"}
	gh := mod.Github
	if gh == "" && repo != "" {
		gh = "https://github.com/" + strings.SplitN(repo, "#", 2)[0]
	}
	link(&dev, "Source code", gh)
	if bugs := npm.StringOrURL(doc.Bugs); bugs != "" {
		link(&dev, "Issues", bugs)
	} else if gh != "" {
		link(&dev, "Issues", gh+"/issues")
	}
	if gh != "" {
		link(&dev, "Releases", gh+"/releases")
	}
	if len(dev.Items) > 0 {
		d.Sidebar = append(d.Sidebar, dev)
	}
	return d
}

func groupDigits(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
