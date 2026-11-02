package drupal

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/PuerkitoBio/goquery"
	"golang.org/x/sync/errgroup"

	"fzpm/internal/provider"
)

func (Drupal) Detail(ctx context.Context, c *http.Client, id string) (*provider.Detail, error) {
	if !validID.MatchString(id) {
		return nil, fmt.Errorf("invalid module name %q", id)
	}
	var (
		d        *provider.Detail
		releases []provider.Release
		status   []provider.Link
	)
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		r, err := get(ctx, c, baseURL+"/project/"+id)
		if err != nil {
			return err
		}
		defer r.Close()
		d, err = parsePage(r, id)
		return err
	})
	g.Go(func() error {
		r, err := get(ctx, c, fmt.Sprintf(releasesURL, id))
		if err != nil {
			return nil // releases are optional; the page is not
		}
		defer r.Close()
		releases, status, _ = parseReleases(r)
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	d.Releases = releases
	if len(status) > 0 {
		// Prepend maintenance/development status to "Project information".
		for i := range d.Sidebar {
			if d.Sidebar[i].Title == "Project information" {
				d.Sidebar[i].Items = append(status, d.Sidebar[i].Items...)
				return d, nil
			}
		}
		d.Sidebar = append([]provider.Section{{Title: "Project information", Items: status}}, d.Sidebar...)
	}
	return d, nil
}

func get(ctx context.Context, c *http.Client, u string) (io.ReadCloser, error) {
	return provider.Fetch(ctx, c, u)
}

// parsePage scrapes a drupal.org project page.
func parsePage(r io.Reader, id string) (*provider.Detail, error) {
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, err
	}
	d := &provider.Detail{
		Package: provider.Package{Provider: "drupal", ID: id},
		URL:     baseURL + "/project/" + id,
	}
	d.Name = strings.TrimSpace(doc.Find(`meta[property="og:title"]`).AttrOr("content", ""))
	if d.Name == "" {
		d.Name = strings.TrimSpace(doc.Find("h1").First().Text())
	}
	d.Summary = summarize(doc.Find(`meta[name="description"]`).AttrOr("content", ""), "")

	node := doc.Find(".node-project-module").First()
	node.Find(".field-name-field-project-images a").Each(func(_ int, a *goquery.Selection) {
		if href, ok := a.Attr("href"); ok {
			d.Images = append(d.Images, abs(href))
		}
	})
	body := node.Find(".field-name-body .field-item").First()
	seen := map[string]bool{}
	for _, u := range d.Images {
		seen[u] = true
	}
	body.Find("img[src]").Each(func(_ int, im *goquery.Selection) {
		if u := abs(im.AttrOr("src", "")); !seen[u] {
			seen[u] = true
			d.Images = append(d.Images, u)
		}
	})
	if body, err := body.Html(); err == nil && body != "" {
		md, err := htmltomarkdown.ConvertString(body, converter.WithDomain(baseURL))
		if err == nil {
			d.Body = strings.TrimSpace(md)
		}
	}

	info := provider.Section{Title: "Project information"}
	doc.Find("ul.project-info > li").Each(func(_ int, li *goquery.Selection) {
		text := strings.Join(strings.Fields(li.Text()), " ")
		if strings.Contains(text, "sites report using") {
			n := strings.ReplaceAll(li.Find("strong").First().Text(), ",", "")
			d.Popularity, _ = strconv.ParseInt(n, 10, 64)
		}
		if strings.HasPrefix(text, "Stable releases for this project are covered") {
			text = "Covered by the security advisory policy"
		}
		info.Items = append(info.Items, provider.Link{Label: text})
	})
	info.Items = append(info.Items, provider.Link{Label: "Composer: drupal/" + id})

	d.Sidebar = append(d.Sidebar, info)
	if s := maintainers(doc); len(s.Items) > 0 {
		d.Sidebar = append(d.Sidebar, s)
	}
	d.Sidebar = append(d.Sidebar,
		linkSection(doc, "#block-drupalorg-project-resources", "a", "Resources"),
		linkSection(doc, "#block-drupalorg-project-development", "a", "Development"),
	)
	if s := linkSection(doc, ".field-name-field-supporting-organizations", ".field-name-field-supporting-organization a", "Supporting organizations"); len(s.Items) > 0 {
		d.Sidebar = append(d.Sidebar, s)
	}
	return d, nil
}

func maintainers(doc *goquery.Document) provider.Section {
	s := provider.Section{Title: "Maintainers"}
	doc.Find("#block-drupalorg-project-maintainers .maintainer").Each(func(_ int, m *goquery.Selection) {
		a := m.Find("small a").First()
		label := strings.Join(strings.Fields(a.Text()), " ")
		if label == "" {
			return
		}
		l := provider.Link{Label: label, URL: abs(a.AttrOr("href", ""))}
		if src := m.Find(".user-picture img").AttrOr("src", ""); src != "" {
			l.Image = abs(src)
		}
		s.Items = append(s.Items, l)
	})
	return s
}

func linkSection(doc *goquery.Document, block, sel, title string) provider.Section {
	s := provider.Section{Title: title}
	doc.Find(block).First().Find(sel).Each(func(_ int, a *goquery.Selection) {
		label := strings.Join(strings.Fields(a.Text()), " ")
		href, _ := a.Attr("href")
		if label == "" || href == "" {
			return
		}
		s.Items = append(s.Items, provider.Link{Label: label, URL: abs(href)})
	})
	return s
}

func abs(href string) string {
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	base, _ := url.Parse(baseURL + "/")
	return base.ResolveReference(u).String()
}
