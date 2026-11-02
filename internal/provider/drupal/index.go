package drupal

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"fzpm/internal/provider"
)

const (
	pageSize    = 50 // JSON:API maximum
	concurrency = 8
	indexFields = "title,field_project_machine_name,field_active_installs_total,body,created,changed"
)

// The local index holds the most installed modules plus the newest and the
// most recently updated ones. The JSON:API search index can sort by installs
// and creation date (other sort fields are silently ignored), so "recently
// changed" comes from the legacy api-d7 endpoint.
const (
	topCount     = 1000
	newestCount  = 500
	changedCount = 500
)

type jsonAPIDoc struct {
	Data []struct {
		Attributes struct {
			Title       string `json:"title"`
			MachineName string `json:"field_project_machine_name"`
			Installs    int64  `json:"field_active_installs_total"`
			Created     string `json:"created"`
			Changed     string `json:"changed"`
			Body        *struct {
				Value   string `json:"value"`
				Summary string `json:"summary"`
			} `json:"body"`
		} `json:"attributes"`
	} `json:"data"`
	Errors []struct {
		Detail string `json:"detail"`
	} `json:"errors"`
}

func (Drupal) Index(ctx context.Context, c *http.Client, emit func([]provider.Package)) error {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)
	fetch := func(f func() ([]provider.Package, error)) {
		g.Go(func() error {
			pkgs, err := f()
			if err != nil {
				return err
			}
			emit(pkgs)
			return nil
		})
	}
	jsonAPI := func(sort string, count int) {
		for off := 0; off < count; off += pageSize {
			q := listQuery()
			q.Set("sort", sort)
			q.Set("page[offset]", fmt.Sprint(off))
			fetch(func() ([]provider.Package, error) { return fetchList(ctx, c, q) })
		}
	}
	jsonAPI("-active_installs_total", topCount)
	jsonAPI("-created", newestCount)
	for page := 0; page*pageSize < changedCount; page++ {
		fetch(func() ([]provider.Package, error) { return fetchChanged(ctx, c, page) })
	}
	return g.Wait()
}

func (Drupal) Search(ctx context.Context, c *http.Client, query string) ([]provider.Package, error) {
	q := listQuery()
	q.Set("filter[fulltext]", query)
	return fetchList(ctx, c, q)
}

func listQuery() url.Values {
	q := url.Values{}
	q.Set("filter[status]", "1")
	q.Set("page[limit]", fmt.Sprint(pageSize))
	q.Set("fields[node--project_module]", indexFields)
	return q
}

func fetchList(ctx context.Context, c *http.Client, q url.Values) ([]provider.Package, error) {
	u := baseURL + "/jsonapi/index/project_modules?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.api+json")
	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var doc jsonAPIDoc
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("drupal.org: decoding %s: %w", res.Status, err)
	}
	if len(doc.Errors) > 0 {
		return nil, fmt.Errorf("drupal.org: %s", doc.Errors[0].Detail)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("drupal.org: %s", res.Status)
	}
	return toPackages(doc), nil
}

func toPackages(doc jsonAPIDoc) []provider.Package {
	out := make([]provider.Package, 0, len(doc.Data))
	for _, d := range doc.Data {
		a := d.Attributes
		if a.MachineName == "" {
			continue
		}
		p := provider.Package{
			Provider:   "drupal",
			ID:         a.MachineName,
			Name:       a.Title,
			Popularity: a.Installs,
		}
		p.Created, _ = time.Parse(time.RFC3339, a.Created)
		p.Updated, _ = time.Parse(time.RFC3339, a.Changed)
		if a.Body != nil {
			p.Summary = summarize(a.Body.Summary, a.Body.Value)
		}
		out = append(out, p)
	}
	return out
}

var (
	tagRe   = regexp.MustCompile(`<[^>]*>`)
	spaceRe = regexp.MustCompile(`\s+`)
)

// summarize returns a one-line plain text summary, at most ~200 chars.
func summarize(summary, body string) string {
	s := summary
	if strings.TrimSpace(s) == "" {
		s = body
	}
	s = html.UnescapeString(tagRe.ReplaceAllString(s, " "))
	s = strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
	if r := []rune(s); len(r) > 200 {
		s = string(r[:199]) + "…"
	}
	return s
}

// d7 API (legacy) response; several fields are [] when empty.
type d7Doc struct {
	List []struct {
		Title       string          `json:"title"`
		MachineName string          `json:"field_project_machine_name"`
		Created     string          `json:"created"`
		Changed     string          `json:"changed"`
		Body        json.RawMessage `json:"body"`
		Usage       json.RawMessage `json:"project_usage"`
	} `json:"list"`
}

func fetchChanged(ctx context.Context, c *http.Client, page int) ([]provider.Package, error) {
	q := url.Values{}
	q.Set("type", "project_module")
	q.Set("field_project_type", "full")
	q.Set("status", "1")
	q.Set("sort", "changed")
	q.Set("direction", "DESC")
	q.Set("limit", fmt.Sprint(pageSize))
	q.Set("page", fmt.Sprint(page))
	r, err := get(ctx, c, baseURL+"/api-d7/node.json?"+q.Encode())
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var doc d7Doc
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("drupal.org api-d7: %w", err)
	}
	out := make([]provider.Package, 0, len(doc.List))
	for _, n := range doc.List {
		if n.MachineName == "" {
			continue
		}
		p := provider.Package{Provider: "drupal", ID: n.MachineName, Name: n.Title}
		p.Created = unix(n.Created)
		p.Updated = unix(n.Changed)
		var body struct{ Value, Summary string }
		if json.Unmarshal(n.Body, &body) == nil {
			p.Summary = summarize(body.Summary, body.Value)
		}
		var usage map[string]json.Number
		if json.Unmarshal(n.Usage, &usage) == nil {
			for branch, v := range usage {
				// Only count Drupal 8+ branches, like active_installs_total.
				if !strings.HasPrefix(branch, "5.") && !strings.HasPrefix(branch, "6.") && !strings.HasPrefix(branch, "7.") {
					i, _ := v.Int64()
					p.Popularity += i
				}
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func unix(s string) time.Time {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(n, 0)
}
