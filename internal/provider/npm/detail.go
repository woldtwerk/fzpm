package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"fzpm/internal/provider"
	"fzpm/internal/provider/readme"
)

// SearchURL is the registry's search endpoint (variable for tests).
var SearchURL = "https://registry.npmjs.org/-/v1/search"

// Search runs the registry search and returns the hits as packages of the
// named provider, with weekly downloads as popularity.
func Search(ctx context.Context, c *http.Client, prov, query string) ([]provider.Package, error) {
	q := url.Values{}
	q.Set("text", query)
	q.Set("size", "50")
	r, err := provider.Fetch(ctx, c, SearchURL+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var res struct {
		Objects []struct {
			Downloads struct {
				Weekly int64 `json:"weekly"`
			} `json:"downloads"`
			Package struct {
				Name        string    `json:"name"`
				Description string    `json:"description"`
				Date        time.Time `json:"date"`
			} `json:"package"`
		} `json:"objects"`
	}
	if err := json.NewDecoder(r).Decode(&res); err != nil {
		return nil, fmt.Errorf("npm search: %w", err)
	}
	out := make([]provider.Package, len(res.Objects))
	for i, o := range res.Objects {
		out[i] = provider.Package{
			Provider: prov, ID: o.Package.Name, Name: o.Package.Name, Summary: o.Package.Description,
			Popularity: o.Downloads.Weekly, Updated: o.Package.Date,
		}
	}
	return out, nil
}

// NoReadme reports whether the registry document lacks a README (common
// for packages published from monorepos).
func NoReadme(doc Doc) bool {
	return strings.TrimSpace(doc.Readme) == "" || strings.HasPrefix(doc.Readme, "ERROR: No README")
}

// Detail builds a package page from the registry: README (from GitHub when
// the registry has none), versions, maintainers and weekly downloads.
func Detail(ctx context.Context, c *http.Client, prov, id string) (*provider.Detail, error) {
	if !ValidName.MatchString(id) {
		return nil, fmt.Errorf("invalid package name %q", id)
	}
	doc, err := Fetch(ctx, c, id)
	if err != nil {
		return nil, err
	}
	repo := readme.GitHubRepo(StringOrURL(doc.Repository))
	var weekly int64
	g, gctx := errgroup.WithContext(ctx)
	if NoReadme(doc) {
		g.Go(func() error {
			doc.Readme = readme.Fetch(gctx, c, readme.RawBase(repo))
			return nil
		})
	}
	g.Go(func() error {
		if d, err := Downloads(gctx, c, []string{id}); err == nil {
			weekly = d[id]
		}
		return nil
	})
	g.Wait()

	created, _ := time.Parse(time.RFC3339, doc.Time["created"])
	modified, _ := time.Parse(time.RFC3339, doc.Time["modified"])
	d := &provider.Detail{
		Package: provider.Package{
			Provider: prov, ID: id, Name: id, Summary: doc.Description,
			Popularity: weekly, Created: created, Updated: modified,
		},
		URL:             "https://www.npmjs.com/package/" + id,
		PopularityLabel: "downloads / week",
	}
	d.Body = readme.Clean(doc.Readme, readme.RawBase(repo))
	if d.Body == "" {
		d.Body = doc.Description
	}
	d.Images = readme.Images(d.Body)
	d.Releases = Releases(doc)

	info := provider.Section{Title: "Package information"}
	add := func(label string) { info.Items = append(info.Items, provider.Link{Label: label}) }
	if latest := doc.DistTags["latest"]; latest != "" {
		add("Latest: " + latest)
	}
	if lic := StringOrURL(doc.License); lic != "" {
		add("License: " + lic)
	}
	if weekly > 0 {
		add(fmt.Sprintf("%s downloads / week", groupDigits(weekly)))
	}
	add("npm: " + id)
	d.Sidebar = append(d.Sidebar, info)

	maint := provider.Section{Title: "Maintainers"}
	for _, m := range doc.Maintainers {
		maint.Items = append(maint.Items, provider.Link{Label: m.Name, URL: "https://www.npmjs.com/~" + m.Name})
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
	link(&res, "Homepage", doc.Homepage)
	link(&res, "npm", d.URL)
	d.Sidebar = append(d.Sidebar, res)

	dev := provider.Section{Title: "Development"}
	gh := ""
	if repo != "" {
		gh = "https://github.com/" + repo
	}
	link(&dev, "Source code", gh)
	if bugs := StringOrURL(doc.Bugs); bugs != "" {
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
	return d, nil
}

func groupDigits(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
