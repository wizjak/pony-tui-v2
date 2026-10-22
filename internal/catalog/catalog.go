// Package catalog loads and caches the MLP:FiM episode catalog.
//
// The catalog is a static JSON file published at fim.heartshine.gay. A copy is
// embedded in the binary so the tool always works offline; a cached/refreshed
// copy takes precedence when available.
package catalog

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RemoteURL is where the upstream catalog lives. It is a variable so tests
// can point it at a local server.
var RemoteURL = "https://fim.heartshine.gay/db.json"

//go:embed db.json
var embedded []byte

const maxCatalogSize = 16 << 20 // 16 MiB: the real file is ~70 KiB

// Subtitle is one subtitle track offered for an episode.
type Subtitle struct {
	Lang  string `json:"lang"`
	Label string `json:"label"`
}

// Episode is a single item in the catalog.
type Episode struct {
	Num   int        `json:"epNum"`
	Title string     `json:"epTitle"`
	Subs  []Subtitle `json:"epSubs"`
}

// Season is a collection: seasons 1-9, then movies, specials and so on.
type Season struct {
	Num      int       `json:"seasNum"`
	Episodes []Episode `json:"episodes"`
}

// Series holds the catalog metadata and all collections.
type Series struct {
	TitleFull   string   `json:"titleFull"`
	TitleAbbrev string   `json:"titleAbbrev"`
	VidPath     string   `json:"vidPath"`
	VTTPath     string   `json:"vttPath"`
	VidRes      []int    `json:"vidRes"`
	Seasons     []Season `json:"seasons"`
}

// DB is the on-disk JSON shape.
type DB struct {
	Page struct {
		Title string `json:"pageTitle"`
	} `json:"page"`
	Series Series `json:"series"`
}

// Catalog is a parsed, validated catalog plus provenance information.
type Catalog struct {
	DB     DB
	Origin string // "embedded", "cache" or "network"
	Age    time.Duration
}

// Ref identifies one episode anywhere in the catalog.
type Ref struct {
	Season int
	Number int
	Title  string
	Subs   []Subtitle
}

// Category is a display-ready collection.
type Category struct {
	Num      int
	Label    string
	Episodes []Episode
}

var collectionLabels = map[int]string{
	10: "Movies",
	11: "Specials",
	12: "Shorts",
	13: "Clip Shows",
	14: "Claymation Shorts",
}

// CollectionLabel returns the human name for a collection number.
func CollectionLabel(n int) string {
	if l, ok := collectionLabels[n]; ok {
		return l
	}
	return fmt.Sprintf("Season %d", n)
}

// Parse validates and normalizes catalog JSON.
func Parse(data []byte, origin string) (*Catalog, error) {
	var db DB
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&db); err != nil {
		return nil, fmt.Errorf("invalid catalog JSON: %w", err)
	}
	if len(db.Series.Seasons) == 0 {
		return nil, errors.New("catalog has no seasons")
	}
	if strings.TrimSpace(db.Series.TitleFull) == "" {
		db.Series.TitleFull = "My Little Pony: Friendship is Magic"
	}
	if strings.TrimSpace(db.Series.TitleAbbrev) == "" {
		db.Series.TitleAbbrev = "MLP:FiM"
	}

	seen := map[int]bool{}
	for i := range db.Series.Seasons {
		s := &db.Series.Seasons[i]
		if s.Num <= 0 {
			return nil, fmt.Errorf("catalog contains an invalid collection number (%d)", s.Num)
		}
		if seen[s.Num] {
			return nil, fmt.Errorf("catalog contains duplicate collection %d", s.Num)
		}
		seen[s.Num] = true
		sort.SliceStable(s.Episodes, func(a, b int) bool {
			return s.Episodes[a].Num < s.Episodes[b].Num
		})
		for j := range s.Episodes {
			e := &s.Episodes[j]
			if e.Num <= 0 {
				return nil, fmt.Errorf("collection %d contains an episode without a number", s.Num)
			}
			if strings.TrimSpace(e.Title) == "" {
				e.Title = fmt.Sprintf("Episode %d", e.Num)
			}
			if e.Subs == nil {
				e.Subs = []Subtitle{}
			}
		}
	}
	sort.SliceStable(db.Series.Seasons, func(a, b int) bool {
		return db.Series.Seasons[a].Num < db.Series.Seasons[b].Num
	})

	return &Catalog{DB: db, Origin: origin, Age: 0}, nil
}

// Categories returns every collection in order.
func (c *Catalog) Categories() []Category {
	out := make([]Category, 0, len(c.DB.Series.Seasons))
	for _, s := range c.DB.Series.Seasons {
		out = append(out, Category{Num: s.Num, Label: CollectionLabel(s.Num), Episodes: s.Episodes})
	}
	return out
}

// Season looks up a collection by number.
func (c *Catalog) Season(num int) (Season, bool) {
	for _, s := range c.DB.Series.Seasons {
		if s.Num == num {
			return s, true
		}
	}
	return Season{}, false
}

// Refs returns every episode in catalog order.
func (c *Catalog) Refs() []Ref {
	var out []Ref
	for _, s := range c.DB.Series.Seasons {
		for _, e := range s.Episodes {
			out = append(out, Ref{Season: s.Num, Number: e.Num, Title: e.Title, Subs: e.Subs})
		}
	}
	return out
}

// Ref looks up a single episode.
func (c *Catalog) Ref(season, number int) (Ref, bool) {
	s, ok := c.Season(season)
	if !ok {
		return Ref{}, false
	}
	for _, e := range s.Episodes {
		if e.Num == number {
			return Ref{Season: season, Number: number, Title: e.Title, Subs: e.Subs}, true
		}
	}
	return Ref{}, false
}

// Next returns the following episode, crossing collection boundaries.
func (c *Catalog) Next(r Ref) (Ref, bool) {
	refs := c.Refs()
	for i := range refs {
		if refs[i].Season == r.Season && refs[i].Number == r.Number {
			if i+1 < len(refs) {
				return refs[i+1], true
			}
			return Ref{}, false
		}
	}
	return Ref{}, false
}

// Prev returns the previous episode, crossing collection boundaries.
func (c *Catalog) Prev(r Ref) (Ref, bool) {
	refs := c.Refs()
	for i := range refs {
		if refs[i].Season == r.Season && refs[i].Number == r.Number {
			if i > 0 {
				return refs[i-1], true
			}
			return Ref{}, false
		}
	}
	return Ref{}, false
}

// Playlist returns a frozen copy of all refs, useful for next/prev lookups.
func (c *Catalog) Playlist() []Ref { return c.Refs() }

func ensureSlash(p string) string {
	if p == "" {
		return ""
	}
	if !strings.HasSuffix(p, "/") {
		return p + "/"
	}
	return p
}

// VideoBase returns the media base URL with a trailing slash.
func (s *Series) VideoBase() string {
	if b := strings.TrimSpace(s.VidPath); b != "" {
		return ensureSlash(b)
	}
	return "https://static.heartshine.gay/g4-fim/"
}

// SubtitleBase returns the subtitle base URL with a trailing slash.
func (s *Series) SubtitleBase() string {
	if p := strings.TrimSpace(s.VTTPath); p != "" {
		return ensureSlash(p)
	}
	return ensureSlash(s.VideoBase() + "vtt")
}

// VideoURL builds the media URL for an episode and quality.
func (s *Series) VideoURL(season, episode int, quality string) string {
	return fmt.Sprintf("%ss%02de%02d-%s.mp4", s.VideoBase(), season, episode, quality)
}

// SubtitleURL builds the subtitle URL for an episode and language.
func (s *Series) SubtitleURL(season, episode int, lang string) string {
	return fmt.Sprintf("%ss%02de%02d-%s.vtt", s.SubtitleBase(), season, episode, lang)
}

// Title returns a readable "S01E01" or "Movies #1" label.
func RefLabel(r Ref) string {
	if r.Season <= 9 {
		return fmt.Sprintf("S%02dE%02d", r.Season, r.Number)
	}
	return fmt.Sprintf("%s #%d", CollectionLabel(r.Season), r.Number)
}

// Store manages the on-disk catalog cache.
type Store struct {
	Dir string
}

// DefaultStore returns the XDG cache location for the catalog.
func DefaultStore() (Store, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return Store{}, fmt.Errorf("cannot determine cache directory: %w", err)
	}
	return Store{Dir: filepath.Join(dir, "pony")}, nil
}

func (s Store) catalogPath() string { return filepath.Join(s.Dir, "db.json") }
func (s Store) metaPath() string    { return filepath.Join(s.Dir, "db.meta.json") }

type meta struct {
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	FetchedAt    time.Time `json:"fetched_at"`
	URL          string    `json:"url"`
}

func (s Store) readMeta() meta {
	var m meta
	data, err := os.ReadFile(s.metaPath())
	if err != nil {
		return m
	}
	_ = json.Unmarshal(data, &m)
	return m
}

// Age reports how old the cached catalog is. A missing cache is very old.
func (s Store) Age() time.Duration {
	m := s.readMeta()
	if m.FetchedAt.IsZero() {
		info, err := os.Stat(s.catalogPath())
		if err != nil {
			return 1 << 62
		}
		return time.Since(info.ModTime())
	}
	return time.Since(m.FetchedAt)
}

// Load returns the cached catalog when valid, otherwise the embedded one.
func (s Store) Load() (*Catalog, error) {
	if data, err := os.ReadFile(s.catalogPath()); err == nil {
		if c, err := Parse(data, "cache"); err == nil {
			c.Age = s.Age()
			return c, nil
		}
		// A corrupt cache should not stick around and confuse the next start.
		_ = os.Remove(s.catalogPath())
		_ = os.Remove(s.metaPath())
	}
	return Parse(embedded, "embedded")
}

// Embedded returns the catalog compiled into the binary.
func Embedded() (*Catalog, error) { return Parse(embedded, "embedded") }

// Refresh downloads the catalog. It returns updated=false on HTTP 304 and
// leaves the cached file untouched when the download fails.
func (s Store) Refresh(ctx context.Context) (cat *Catalog, updated bool, err error) {
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, RemoteURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", "pony/1.0 (+https://github.com/wizjak/pony-tui)")
	req.Header.Set("Accept", "application/json")

	old := s.readMeta()
	if old.ETag != "" {
		req.Header.Set("If-None-Match", old.ETag)
	}
	if old.LastModified != "" {
		req.Header.Set("If-Modified-Since", old.LastModified)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("could not download the catalog: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		c, lerr := s.Load()
		if lerr != nil {
			return nil, false, lerr
		}
		// Refresh the timestamp so we don't hit the server every start.
		old.FetchedAt = time.Now()
		old.URL = RemoteURL
		s.writeMeta(old)
		c.Age = 0
		return c, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("catalog server returned %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogSize+1))
	if err != nil {
		return nil, false, fmt.Errorf("could not read the catalog: %w", err)
	}
	if len(data) > maxCatalogSize {
		return nil, false, errors.New("catalog download is unexpectedly large")
	}
	c, err := Parse(data, "network")
	if err != nil {
		return nil, false, err
	}
	if err := s.writeCatalog(data); err != nil {
		return c, true, fmt.Errorf("catalog updated but could not be cached: %w", err)
	}
	m := meta{
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		FetchedAt:    time.Now(),
		URL:          RemoteURL,
	}
	s.writeMeta(m)
	return c, true, nil
}

func (s Store) writeCatalog(data []byte) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, "db.*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, s.catalogPath())
}

func (s Store) writeMeta(m meta) {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(s.Dir, "db.meta.*.json")
	if err != nil {
		return
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err == nil {
		tmp.Close()
		_ = os.Chmod(name, 0o644)
		_ = os.Rename(name, s.metaPath())
		return
	}
	tmp.Close()
}
