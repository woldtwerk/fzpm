package nuxt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"fzpm/internal/provider"
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
	weeklyDownloads(ctx, c, names, func(d map[string]int64) {
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

// Download counts come from npmx.dev, which returns weekly downloads for
// several packages per request, scoped ones included (api.npmjs.org's bulk
// endpoint cannot do scoped packages and rate-limits single requests hard).
// npmx answers large batches with 502s, so names are sent in small batches;
// a failing batch is split in half and retried.
const downloadsBatch = 25

// retryPause is the delay before retrying a failed batch (variable for tests).
var retryPause = time.Second

// weeklyDownloads reports last week's downloads batch by batch; packages
// that keep failing are left out.
func weeklyDownloads(ctx context.Context, c *http.Client, names []string, report func(map[string]int64)) {
	for i := 0; i < len(names) && ctx.Err() == nil; i += downloadsBatch {
		fetchDownloads(ctx, c, names[i:min(i+downloadsBatch, len(names))], report, 0)
	}
}

func fetchDownloads(ctx context.Context, c *http.Client, names []string, report func(map[string]int64), depth int) {
	got, err := getDownloads(ctx, c, names)
	if err == nil {
		report(got)
		return
	}
	if ctx.Err() != nil || depth >= 3 {
		return
	}
	pause(ctx, retryPause)
	if len(names) == 1 {
		fetchDownloads(ctx, c, names, report, depth+1)
		return
	}
	half := len(names) / 2
	fetchDownloads(ctx, c, names[:half], report, depth+1)
	fetchDownloads(ctx, c, names[half:], report, depth+1)
}

func getDownloads(ctx context.Context, c *http.Client, names []string) (map[string]int64, error) {
	r, err := provider.Fetch(ctx, c, downloadsURL+url.QueryEscape(strings.Join(names, ",")))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var res struct {
		Packages []struct {
			PackageName string `json:"packageName"`
			Versions    []struct {
				Downloads int64 `json:"downloads"`
			} `json:"versions"`
		} `json:"packages"`
	}
	if err := json.NewDecoder(r).Decode(&res); err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(res.Packages))
	for _, p := range res.Packages {
		var sum int64
		for _, v := range p.Versions {
			sum += v.Downloads
		}
		out[p.PackageName] = sum
	}
	return out, nil
}

func pause(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
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

type npmDoc struct {
	Name     string            `json:"name"`
	DistTags map[string]string `json:"dist-tags"`
	Time     map[string]string `json:"time"`
	Versions map[string]struct {
		Deprecated json.RawMessage `json:"deprecated"`
	} `json:"versions"`
	Readme      string          `json:"readme"`
	Description string          `json:"description"`
	Homepage    string          `json:"homepage"`
	License     json.RawMessage `json:"license"`
	Repository  json.RawMessage `json:"repository"`
	Bugs        json.RawMessage `json:"bugs"`
	Maintainers []struct {
		Name string `json:"name"`
	} `json:"maintainers"`
}

// Detail combines the npm registry (README, releases) with nuxt.com
// metadata (maintainers, category, compatibility, links).
func (n *Nuxt) Detail(ctx context.Context, c *http.Client, id string) (*provider.Detail, error) {
	if !validNpm.MatchString(id) {
		return nil, fmt.Errorf("invalid package name %q", id)
	}
	var (
		doc npmDoc
		mod module
		ok  bool
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		r, err := provider.Fetch(gctx, c, npmURL+strings.Replace(id, "/", "%2F", 1))
		if err != nil {
			return err
		}
		defer r.Close()
		return json.NewDecoder(r).Decode(&doc)
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
		repo = githubRepo(stringOrURL(doc.Repository))
	}
	g, gctx = errgroup.WithContext(ctx)
	if strings.TrimSpace(doc.Readme) == "" || strings.HasPrefix(doc.Readme, "ERROR: No README") {
		g.Go(func() error {
			doc.Readme = githubReadme(gctx, c, rawBase(repo))
			return nil
		})
	}
	var downloads int64
	g.Go(func() error {
		if d, err := getDownloads(gctx, c, []string{id}); err == nil {
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

func githubReadme(ctx context.Context, c *http.Client, base string) string {
	if base == "" {
		return ""
	}
	for _, name := range []string{"README.md", "readme.md", "Readme.md"} {
		r, err := provider.Fetch(ctx, c, base+name)
		if err != nil {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(r, 1<<20))
		r.Close()
		if err == nil {
			return string(b)
		}
	}
	return ""
}

func buildDetail(id string, doc npmDoc, mod module, listed bool) *provider.Detail {
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
		repo = githubRepo(stringOrURL(doc.Repository))
	}
	d.Body = cleanReadme(doc.Readme, rawBase(repo))
	d.Images = markdownImages(d.Body)
	d.Releases = releases(doc)

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
	if lic := stringOrURL(doc.License); lic != "" {
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
	if bugs := stringOrURL(doc.Bugs); bugs != "" {
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

// releases lists published versions, newest first.
func releases(doc npmDoc) []provider.Release {
	tags := map[string][]string{}
	for tag, v := range doc.DistTags {
		tags[v] = append(tags[v], tag)
	}
	var out []provider.Release
	for v, ts := range doc.Time {
		if _, ok := doc.Versions[v]; !ok {
			continue // "created", "modified", unpublished
		}
		t, _ := time.Parse(time.RFC3339, ts)
		r := provider.Release{
			Version:    v,
			Constraint: constraint(v),
			Date:       t,
			URL:        "https://www.npmjs.com/package/" + doc.Name + "/v/" + v,
		}
		sort.Strings(tags[v])
		r.Tags = append(r.Tags, tags[v]...)
		if dep := doc.Versions[v].Deprecated; len(dep) > 0 && string(dep) != "false" {
			r.Tags = append(r.Tags, "deprecated")
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.After(out[j].Date) })
	if len(out) > 30 {
		out = out[:30]
	}
	return out
}

// constraint: ^x.y.z for stable versions, the exact version otherwise.
func constraint(v string) string {
	if strings.ContainsAny(v, "-+") {
		return v
	}
	return "^" + v
}

// stringOrURL reads npm fields that are either "x" or {"url": "x"} /
// {"type": "x"}.
func stringOrURL(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct{ URL, Type string }
	if json.Unmarshal(raw, &o) == nil {
		if o.URL != "" {
			return o.URL
		}
		return o.Type
	}
	return ""
}

var ghRepoRe = regexp.MustCompile(`github\.com[/:]([^/]+/[^/#.]+)`)

// githubRepo extracts "owner/repo" from a repository URL.
func githubRepo(u string) string {
	if m := ghRepoRe.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return ""
}

// rawBase returns the raw.githubusercontent.com base for a nuxt.com "repo"
// value ("owner/repo", "owner/repo#branch", "owner/repo/sub/dir").
func rawBase(repo string) string {
	if repo == "" {
		return ""
	}
	path, branch, _ := strings.Cut(repo, "#")
	if branch == "" {
		branch = "HEAD"
	}
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if len(segs) < 2 {
		return ""
	}
	base := "https://raw.githubusercontent.com/" + segs[0] + "/" + segs[1] + "/" + branch + "/"
	if len(segs) > 2 {
		base += strings.Join(segs[2:], "/") + "/"
	}
	return base
}

var (
	htmlImgRe   = regexp.MustCompile(`(?i)<img\s[^>]*?src=["']([^"']+)["'][^>]*>`)
	htmlAltRe   = regexp.MustCompile(`(?i)\salt=["']([^"']*)["']`)
	htmlTagRe   = regexp.MustCompile(`(?i)</?(p|div|a|br|picture|source|center|span|h[1-6]|sup|sub|details|summary)(\s[^>]*)?/?>`)
	linkedImgRe = regexp.MustCompile(`\[!\[[^\]]*\]\(([^)\s]+)[^)]*\)\]\([^)]*\)`)
	mdImgRe     = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)([^)]*)\)`)
	ghBlobRe    = regexp.MustCompile(`^https://github\.com/([^/]+/[^/]+)/blob/(.+)$`)
)

// cleanReadme makes an npm README render well in the terminal: HTML images
// become markdown images, badges go, relative image paths become absolute,
// and layout-only HTML tags are dropped. Code blocks are left alone.
func cleanReadme(md, base string) string {
	md = inlineReferences(md)
	lines := strings.Split(md, "\n")
	inCode := false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		lines[i] = outsideInlineCode(l, func(l string) string { return cleanLine(l, base) })
	}
	return strings.TrimSpace(regexp.MustCompile(`\n{3,}`).ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

// outsideInlineCode applies fn to the parts of a line that are not inside
// `inline code`.
func outsideInlineCode(l string, fn func(string) string) string {
	parts := strings.Split(l, "`")
	for i := 0; i < len(parts); i += 2 {
		parts[i] = fn(parts[i])
	}
	return strings.Join(parts, "`")
}

func cleanLine(l, base string) string {
	l = htmlImgRe.ReplaceAllStringFunc(l, func(tag string) string {
		src := htmlImgRe.FindStringSubmatch(tag)[1]
		alt := ""
		if m := htmlAltRe.FindStringSubmatch(tag); m != nil {
			alt = m[1]
		}
		return "![" + alt + "](" + src + ")"
	})
	l = htmlTagRe.ReplaceAllString(l, "")
	l = linkedImgRe.ReplaceAllStringFunc(l, func(s string) string {
		if isBadge(linkedImgRe.FindStringSubmatch(s)[1]) {
			return ""
		}
		return s
	})
	l = mdImgRe.ReplaceAllStringFunc(l, func(s string) string {
		m := mdImgRe.FindStringSubmatch(s)
		if isBadge(m[2]) {
			return ""
		}
		return "![" + m[1] + "](" + absImage(m[2], base) + ")"
	})
	return l
}

var (
	refDefRe       = regexp.MustCompile(`(?m)^ {0,3}\[([^\]]+)\]:[ \t]*<?(\S+?)>?(?:[ \t]+["'(].*)?[ \t]*$\n?`)
	refImageRe     = regexp.MustCompile(`!\[([^\]]*)\]\[([^\]]*)\]`)
	refLinkedImgRe = regexp.MustCompile(`\[(!\[[^\]]*\]\([^)]*\))\]\[([^\]]*)\]`)
	refLinkRe      = regexp.MustCompile(`\[([^\]]+)\]\[([^\]]*)\]`)
)

// inlineReferences turns reference-style links and images ([x][ref] with
// "[ref]: url" definitions, popular for README badges) into inline ones.
func inlineReferences(md string) string {
	defs := map[string]string{}
	for _, m := range refDefRe.FindAllStringSubmatch(md, -1) {
		defs[strings.ToLower(m[1])] = m[2]
	}
	if len(defs) == 0 {
		return md
	}
	md = refDefRe.ReplaceAllString(md, "")
	lookup := func(label, ref string) (string, bool) {
		if ref == "" {
			ref = label
		}
		u, ok := defs[strings.ToLower(ref)]
		return u, ok
	}
	md = refImageRe.ReplaceAllStringFunc(md, func(s string) string {
		m := refImageRe.FindStringSubmatch(s)
		if u, ok := lookup(m[1], m[2]); ok {
			return "![" + m[1] + "](" + u + ")"
		}
		return s
	})
	for _, re := range []*regexp.Regexp{refLinkedImgRe, refLinkRe} {
		md = re.ReplaceAllStringFunc(md, func(s string) string {
			m := re.FindStringSubmatch(s)
			if u, ok := lookup(m[1], m[2]); ok {
				return "[" + m[1] + "](" + u + ")"
			}
			return s
		})
	}
	return md
}

func isBadge(u string) bool {
	l := strings.ToLower(u)
	return strings.Contains(l, "shields.io") || strings.Contains(l, "badgen.net") ||
		strings.Contains(l, "badge") || strings.Contains(l, "codecov.io") ||
		strings.HasSuffix(strings.SplitN(l, "?", 2)[0], ".svg")
}

func absImage(u, base string) string {
	if m := ghBlobRe.FindStringSubmatch(u); m != nil {
		return "https://raw.githubusercontent.com/" + m[1] + "/" + m[2]
	}
	if strings.Contains(u, "://") || base == "" || strings.HasPrefix(u, "data:") {
		return u
	}
	b, err := url.Parse(base)
	if err != nil {
		return u
	}
	ref, err := url.Parse(strings.TrimPrefix(u, "/"))
	if err != nil {
		return u
	}
	return b.ResolveReference(ref).String()
}

func markdownImages(md string) []string {
	var out []string
	for _, m := range mdImgRe.FindAllStringSubmatch(md, -1) {
		out = append(out, m[2])
	}
	return out
}

func groupDigits(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
