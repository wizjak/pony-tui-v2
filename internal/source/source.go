// Package source resolves playable media URLs.
//
// The archive exposes videos at a predictable URL:
//
//	<vidPath>/s01e01-1080p.mp4
//
// Not every episode exists in every resolution, so Resolve probes candidates
// (HEAD, then a one-byte ranged GET) and returns the first available one.
package source

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// DefaultQualities is the fallback order used when resolving an episode.
var DefaultQualities = []string{"1080p", "720p", "480p", "360p"}

// Result is a resolved media URL.
type Result struct {
	URL      string
	Quality  string
	Fallback bool // true when the preferred quality was unavailable
}

// Resolver probes and remembers which qualities exist.
type Resolver struct {
	Client *http.Client

	mu    sync.Mutex
	known map[string]entry // key: season:episode:quality
}

type entry struct {
	ok bool
	at time.Time
}

// negativeTTL keeps "this quality is missing" answers for a while. A recently
// uploaded file will be found again after the TTL expires.
const negativeTTL = 10 * time.Minute

// New returns a resolver with sane timeouts.
func New() *Resolver {
	return &Resolver{
		Client: &http.Client{
			Timeout: 8 * time.Second,
			Transport: &http.Transport{
				Proxy:               http.ProxyFromEnvironment,
				MaxIdleConns:        8,
				IdleConnTimeout:     30 * time.Second,
				TLSHandshakeTimeout: 5 * time.Second,
			},
		},
		known: map[string]entry{},
	}
}

func cacheKey(season, episode int, quality string) string {
	return fmt.Sprintf("%d:%d:%s", season, episode, quality)
}

// Cached reports the last known availability for a quality and whether the
// answer is still fresh.
func (r *Resolver) Cached(season, episode int, quality string) (bool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.known[cacheKey(season, episode, quality)]
	if !ok {
		return false, false
	}
	if !e.ok && time.Since(e.at) > negativeTTL {
		delete(r.known, cacheKey(season, episode, quality))
		return false, false
	}
	return e.ok, true
}

// Remember records availability.
func (r *Resolver) Remember(season, episode int, quality string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.known[cacheKey(season, episode, quality)] = entry{ok: ok, at: time.Now()}
}

// Resolve finds the best available URL for an episode.
//
// prefs is tried in order and deduplicated; DefaultQualities are appended so
// an unavailable preferred quality falls back to something playable.
func (r *Resolver) Resolve(ctx context.Context, base string, season, episode int, prefs []string) (Result, error) {
	qualities := orderedQualities(prefs)

	type probe struct {
		index int
		q     string
		url   string
		ok    bool
	}

	results := make(chan probe, len(qualities))
	var wg sync.WaitGroup
	for i, q := range qualities {
		url := fmt.Sprintf("%ss%02de%02d-%s.mp4", ensureSlash(base), season, episode, q)

		if known, ok := r.Cached(season, episode, q); ok {
			results <- probe{index: i, q: q, url: url, ok: known}
			continue
		}

		wg.Add(1)
		go func(i int, q, url string) {
			defer wg.Done()
			ok := r.exists(ctx, url)
			r.Remember(season, episode, q, ok)
			results <- probe{index: i, q: q, url: url, ok: ok}
		}(i, q, url)
	}
	wg.Wait()
	close(results)

	best := probe{index: -1}
	for p := range results {
		if p.ok && (best.index == -1 || p.index < best.index) {
			best = p
		}
	}
	if best.index == -1 {
		return Result{}, fmt.Errorf("no playable file found for S%02dE%02d (tried %v)", season, episode, qualities)
	}
	return Result{
		URL:      best.url,
		Quality:  best.q,
		Fallback: best.index > 0,
	}, nil
}

func orderedQualities(prefs []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(q string) {
		if q == "" || seen[q] {
			return
		}
		seen[q] = true
		out = append(out, q)
	}
	for _, q := range prefs {
		add(q)
	}
	for _, q := range DefaultQualities {
		add(q)
	}
	return out
}

// exists probes a URL without downloading the media.
func (r *Resolver) exists(ctx context.Context, url string) bool {
	// HEAD is cheap and most static file servers answer it correctly.
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err == nil {
		resp, err := r.Client.Do(req)
		if err == nil {
			resp.Body.Close()
			switch {
			case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent:
				return true
			case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden:
				return false
			}
		}
	}

	// Some servers reject HEAD (405) or do not implement it reliably; ask for
	// a single byte instead and close immediately after the headers.
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Range", "bytes=0-0")
	resp, err := r.Client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent
}

// Qualities returns the preference order for a chosen quality: the choice
// first, then lower qualities, and finally higher ones. Degrading gracefully
// is preferable to jumping back to 1080p when 480p is missing.
func Qualities(current string) []string {
	ladder := []string{"1080p", "720p", "480p", "360p"}
	idx := -1
	for i, q := range ladder {
		if q == current {
			idx = i
			break
		}
	}
	out := []string{}
	if current != "" {
		out = append(out, current)
	}
	seen := map[string]bool{}
	if current != "" {
		seen[current] = true
	}
	add := func(q string) {
		if !seen[q] {
			seen[q] = true
			out = append(out, q)
		}
	}
	if idx >= 0 {
		for _, q := range ladder[idx+1:] {
			add(q)
		}
		for i := idx - 1; i >= 0; i-- {
			add(ladder[i])
		}
	} else {
		for _, q := range DefaultQualities {
			add(q)
		}
	}
	if len(out) == 0 {
		out = append(out, DefaultQualities...)
	}
	return out
}

// NextQuality returns the next quality in the cycle.
func NextQuality(current string) string {
	cycle := []string{"1080p", "720p", "480p"}
	for i, q := range cycle {
		if q == current {
			return cycle[(i+1)%len(cycle)]
		}
	}
	return cycle[0]
}

func ensureSlash(p string) string {
	if p == "" {
		return ""
	}
	if p[len(p)-1] != '/' {
		return p + "/"
	}
	return p
}
