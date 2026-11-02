// Package app glues providers to the cache. Providers only fetch; all caching
// policy lives here so every provider gets it for free.
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp"

	"fzpm/internal/cache"
	"fzpm/internal/provider"
)

const (
	IndexTTL  = 24 * time.Hour
	DetailTTL = 6 * time.Hour
	maxImage  = 10 << 20
)

// Service provides cached access to providers.
type Service struct {
	HTTP  *http.Client
	Cache *cache.Store

	mu       sync.Mutex
	images   map[string]image.Image
	inflight map[string]*call
}

type call struct {
	wg  sync.WaitGroup
	val any
	err error
}

// New returns a service with a sensible HTTP client.
func New(c *cache.Store, version string) *Service {
	return &Service{
		HTTP: &http.Client{
			Timeout:   20 * time.Second,
			Transport: userAgent{"fzpm/" + version, http.DefaultTransport},
		},
		Cache:    c,
		images:   map[string]image.Image{},
		inflight: map[string]*call{},
	}
}

type userAgent struct {
	ua   string
	next http.RoundTripper
}

func (u userAgent) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("User-Agent", u.ua)
	return u.next.RoundTrip(r)
}

// once deduplicates concurrent identical requests (e.g. prefetch + open).
func (s *Service) once(key string, fn func() (any, error)) (any, error) {
	s.mu.Lock()
	if c, ok := s.inflight[key]; ok {
		s.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	c := &call{}
	c.wg.Add(1)
	s.inflight[key] = c
	s.mu.Unlock()
	c.val, c.err = fn()
	c.wg.Done()
	s.mu.Lock()
	delete(s.inflight, key)
	s.mu.Unlock()
	return c.val, c.err
}

func indexKey(p provider.Provider) string { return p.Name() + "/index.json" }

// LoadIndex returns the cached index of a provider and whether it is fresh.
func (s *Service) LoadIndex(p provider.Provider) ([]provider.Package, bool) {
	var pkgs []provider.Package
	if !s.Cache.GetJSON(indexKey(p), 0, &pkgs) {
		return nil, false
	}
	age, _ := s.Cache.Age(indexKey(p))
	return pkgs, age < IndexTTL
}

// SaveIndex persists a provider's index.
func (s *Service) SaveIndex(p provider.Provider, pkgs []provider.Package) error {
	var own []provider.Package
	for _, pkg := range pkgs {
		if pkg.Provider == p.Name() {
			own = append(own, pkg)
		}
	}
	return s.Cache.PutJSON(indexKey(p), own)
}

// Search runs a remote search, cached for DetailTTL.
func (s *Service) Search(ctx context.Context, p provider.Provider, q string) ([]provider.Package, error) {
	key := p.Name() + "/search/" + strings.ToLower(q)
	var pkgs []provider.Package
	if s.Cache.GetJSON(key, DetailTTL, &pkgs) {
		return pkgs, nil
	}
	v, err := s.once(key, func() (any, error) {
		pkgs, err := p.Search(ctx, s.HTTP, q)
		if err == nil {
			_ = s.Cache.PutJSON(key, pkgs)
		}
		return pkgs, err
	})
	pkgs, _ = v.([]provider.Package)
	return pkgs, err
}

func detailKey(p provider.Provider, id string) string { return p.Name() + "/detail/" + id }

// Detail returns a package detail page. A cached page is returned whatever
// its age; stale reports that it is older than DetailTTL and should be
// refreshed (stale-while-revalidate).
func (s *Service) Detail(ctx context.Context, p provider.Provider, id string) (d *provider.Detail, stale bool, err error) {
	if b, age, ok := s.Cache.GetAny(detailKey(p, id)); ok {
		var cached provider.Detail
		if json.Unmarshal(b, &cached) == nil {
			return &cached, age > DetailTTL, nil
		}
	}
	d, err = s.RefreshDetail(ctx, p, id)
	return d, false, err
}

// HasFreshDetail reports whether a non-stale page is cached.
func (s *Service) HasFreshDetail(p provider.Provider, id string) bool {
	_, ok := s.Cache.Get(detailKey(p, id), DetailTTL)
	return ok
}

// RefreshDetail fetches a page from the provider, bypassing the cache.
func (s *Service) RefreshDetail(ctx context.Context, p provider.Provider, id string) (*provider.Detail, error) {
	key := detailKey(p, id)
	v, err := s.once(key, func() (any, error) {
		d, err := p.Detail(ctx, s.HTTP, id)
		if err == nil {
			_ = s.Cache.PutJSON(key, d)
		}
		return d, err
	})
	if err != nil {
		return nil, err
	}
	return v.(*provider.Detail), nil
}

// Image downloads (cached forever) and decodes an image.
func (s *Service) Image(ctx context.Context, url string) (image.Image, error) {
	s.mu.Lock()
	img, ok := s.images[url]
	s.mu.Unlock()
	if ok {
		return img, nil
	}
	v, err := s.once("img/"+url, func() (any, error) {
		b, ok := s.Cache.Get("img/"+url, 0)
		if !ok {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return nil, err
			}
			res, err := s.HTTP.Do(req)
			if err != nil {
				return nil, err
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("%s: %s", url, res.Status)
			}
			if b, err = io.ReadAll(io.LimitReader(res.Body, maxImage)); err != nil {
				return nil, err
			}
			_ = s.Cache.Put("img/"+url, b)
		}
		img, _, err := image.Decode(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.images[url] = img
		s.mu.Unlock()
		return img, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(image.Image), nil
}
