package composer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"fzpm/internal/provider"
	"fzpm/internal/provider/readme"
)

// PackagistURL is the packagist.org base (variable so tests can point it at
// a local server).
var PackagistURL = "https://packagist.org"

// List returns the names of all packages of a type (e.g.
// "typo3-cms-extension"), abandoned ones left out.
func List(ctx context.Context, c *http.Client, typ string) ([]string, error) {
	r, err := provider.Fetch(ctx, c, PackagistURL+"/packages/list.json?fields[]=abandoned&type="+url.QueryEscape(typ))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var res struct {
		Packages map[string]struct {
			Abandoned json.RawMessage `json:"abandoned"`
		} `json:"packages"`
	}
	if err := json.NewDecoder(r).Decode(&res); err != nil {
		return nil, fmt.Errorf("packagist list: %w", err)
	}
	out := make([]string, 0, len(res.Packages))
	for name, p := range res.Packages {
		if !abandoned(p.Abandoned) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// SearchPageSize is the largest page packagist.org's search returns. The
// search only pages through the first few hundred hits.
const SearchPageSize = 100

// Search runs packagist.org's search (empty query: by popularity), optionally
// limited to a package type, and returns the results as packages of the
// named provider.
func Search(ctx context.Context, c *http.Client, prov, query, typ string, page int) ([]provider.Package, error) {
	q := url.Values{}
	q.Set("q", query)
	q.Set("per_page", fmt.Sprint(SearchPageSize))
	q.Set("page", fmt.Sprint(page))
	if typ != "" {
		q.Set("type", typ)
	}
	r, err := provider.Fetch(ctx, c, PackagistURL+"/search.json?"+q.Encode())
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var res struct {
		Results []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Downloads   int64           `json:"downloads"`
			Abandoned   json.RawMessage `json:"abandoned"`
		} `json:"results"`
	}
	if err := json.NewDecoder(r).Decode(&res); err != nil {
		return nil, fmt.Errorf("packagist search: %w", err)
	}
	out := make([]provider.Package, 0, len(res.Results))
	for _, p := range res.Results {
		if abandoned(p.Abandoned) {
			continue
		}
		out = append(out, provider.Package{Provider: prov, ID: p.Name, Name: p.Name, Summary: p.Description, Popularity: p.Downloads})
	}
	return out, nil
}

// Popular returns one page of packagist.org's most downloaded packages.
func Popular(ctx context.Context, c *http.Client, prov string, page int) ([]provider.Package, error) {
	r, err := provider.Fetch(ctx, c, fmt.Sprintf("%s/explore/popular.json?per_page=%d&page=%d", PackagistURL, SearchPageSize, page))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var res struct {
		Packages []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Downloads   int64  `json:"downloads"`
		} `json:"packages"`
	}
	if err := json.NewDecoder(r).Decode(&res); err != nil {
		return nil, fmt.Errorf("packagist popular: %w", err)
	}
	out := make([]provider.Package, len(res.Packages))
	for i, p := range res.Packages {
		out[i] = provider.Package{Provider: prov, ID: p.Name, Name: p.Name, Summary: p.Description, Popularity: p.Downloads}
	}
	return out, nil
}

// abandoned: the field is missing, false, true or the suggested replacement.
func abandoned(raw json.RawMessage) bool {
	s := string(raw)
	return s != "" && s != "false" && s != "null"
}

// Version is one version of a Packagist package.
type Version struct {
	Version  string                     `json:"version"`
	Time     time.Time                  `json:"time"`
	License  []string                   `json:"license"`
	Homepage string                     `json:"homepage"`
	Require  map[string]string          `json:"require"`
	Extra    map[string]json.RawMessage `json:"extra"`
	Support  struct {
		Docs   string `json:"docs"`
		Issues string `json:"issues"`
		Source string `json:"source"`
	} `json:"support"`
}

// Package is packagist.org's package document.
type Package struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Time        time.Time `json:"time"`
	Repository  string    `json:"repository"`
	Maintainers []struct {
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	} `json:"maintainers"`
	Versions  map[string]Version `json:"versions"`
	Abandoned json.RawMessage    `json:"abandoned"`
	Downloads struct {
		Total   int64 `json:"total"`
		Monthly int64 `json:"monthly"`
	} `json:"downloads"`
	Stars int64 `json:"github_stars"`
}

// Fetch loads a package document from packagist.org.
func Fetch(ctx context.Context, c *http.Client, name string) (*Package, error) {
	if !ValidName.MatchString(name) {
		return nil, fmt.Errorf("invalid package name %q", name)
	}
	r, err := provider.Fetch(ctx, c, PackagistURL+"/packages/"+name+".json")
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var doc struct {
		Package Package `json:"package"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("packagist %s: %w", name, err)
	}
	return &doc.Package, nil
}

// Stable lists the tagged (non-dev) versions, newest first.
func (p *Package) Stable() []Version {
	var out []Version
	for name, v := range p.Versions {
		if !strings.HasPrefix(name, "dev-") && !strings.HasSuffix(name, "-dev") {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}

// Latest is the newest tagged version, else the newest branch.
func (p *Package) Latest() Version {
	if s := p.Stable(); len(s) > 0 {
		return s[0]
	}
	var latest Version
	for _, v := range p.Versions {
		if v.Time.After(latest.Time) {
			latest = v
		}
	}
	return latest
}

// Detail builds a detail page: the GitHub README as body, the newest tagged
// versions as releases. compat names the requirement shown as release
// compatibility (e.g. "typo3/cms-core").
func Detail(ctx context.Context, c *http.Client, prov string, p *Package, compat string) *provider.Detail {
	latest := p.Latest()
	d := &provider.Detail{
		Package: provider.Package{
			Provider: prov, ID: p.Name, Name: p.Name, Summary: p.Description,
			Popularity: p.Downloads.Monthly, Created: p.Time, Updated: latest.Time,
		},
		URL:             PackagistURL + "/packages/" + p.Name,
		PopularityLabel: "downloads / month",
	}
	if repo := readme.GitHubRepo(p.Repository); repo != "" {
		base := readme.RawBase(repo)
		d.Body = readme.Clean(readme.Fetch(ctx, c, base), base)
		d.Images = readme.Images(d.Body)
	}
	if d.Body == "" {
		d.Body = p.Description
	}
	for i, v := range p.Stable() {
		if i == 30 {
			break
		}
		d.Releases = append(d.Releases, provider.Release{
			Version:    v.Version,
			Constraint: Constraint(v.Version),
			Date:       v.Time,
			Compat:     v.Require[compat],
			URL:        d.URL + "#" + v.Version,
		})
	}

	info := provider.Section{Title: "Package information"}
	add := func(label string) { info.Items = append(info.Items, provider.Link{Label: label}) }
	if abandoned(p.Abandoned) {
		repl := ""
		_ = json.Unmarshal(p.Abandoned, &repl)
		if repl != "" {
			add("Abandoned, use " + repl)
		} else {
			add("Abandoned")
		}
	}
	if len(d.Releases) > 0 {
		add("Latest: " + d.Releases[0].Version)
	}
	if len(latest.License) > 0 {
		add("License: " + strings.Join(latest.License, ", "))
	}
	add(fmt.Sprintf("%s downloads", groupDigits(p.Downloads.Total)))
	if p.Stars > 0 {
		add(fmt.Sprintf("%s GitHub stars", groupDigits(p.Stars)))
	}
	add("composer: " + p.Name)
	d.Sidebar = append(d.Sidebar, info)

	maint := provider.Section{Title: "Maintainers"}
	for _, m := range p.Maintainers {
		maint.Items = append(maint.Items, provider.Link{Label: m.Name, URL: PackagistURL + "/users/" + url.PathEscape(m.Name) + "/", Image: m.AvatarURL})
	}
	if len(maint.Items) > 0 {
		d.Sidebar = append(d.Sidebar, maint)
	}

	link := func(s *provider.Section, label, u string) {
		if u != "" {
			s.Items = append(s.Items, provider.Link{Label: label, URL: u})
		}
	}
	res := provider.Section{Title: "Resources"}
	link(&res, "Homepage", latest.Homepage)
	link(&res, "Documentation", latest.Support.Docs)
	link(&res, "Packagist", d.URL)
	d.Sidebar = append(d.Sidebar, res)

	dev := provider.Section{Title: "Development"}
	link(&dev, "Source code", strings.TrimSuffix(p.Repository, ".git"))
	link(&dev, "Issues", latest.Support.Issues)
	if repo := readme.GitHubRepo(p.Repository); repo != "" {
		link(&dev, "Releases", "https://github.com/"+repo+"/releases")
	}
	if len(dev.Items) > 0 {
		d.Sidebar = append(d.Sidebar, dev)
	}
	return d
}

// Constraint: ^x.y.z for stable versions, the exact version otherwise.
func Constraint(v string) string {
	v = strings.TrimPrefix(v, "v")
	if strings.ContainsAny(v, "-+") {
		return v
	}
	return "^" + v
}

func groupDigits(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
