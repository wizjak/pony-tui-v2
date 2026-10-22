package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEmbeddedCatalog(t *testing.T) {
	c, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(c.Refs()); got != 262 {
		t.Fatalf("got %d refs, want 262", got)
	}
	s1, ok := c.Season(1)
	if !ok || len(s1.Episodes) != 26 {
		t.Fatalf("season 1 = %+v", s1)
	}
	if s1.Episodes[0].Title != "Friendship is Magic, Part 1" {
		t.Fatalf("first title = %q", s1.Episodes[0].Title)
	}

	series := c.DB.Series
	if got, want := series.VideoURL(1, 1, "1080p"), "https://static.heartshine.gay/g4-fim/s01e01-1080p.mp4"; got != want {
		t.Fatalf("video url = %q, want %q", got, want)
	}
	if got, want := series.SubtitleURL(1, 1, "es"), "https://static.heartshine.gay/g4-fim/vtt/s01e01-es.vtt"; got != want {
		t.Fatalf("subtitle url = %q, want %q", got, want)
	}

	ref, _ := c.Ref(9, 26)
	next, ok := c.Next(ref)
	if !ok || next.Season != 10 || next.Number != 1 {
		t.Fatalf("next after S09E26 = %+v, ok=%v", next, ok)
	}
	if got := RefLabel(next); got != "Movies #1" {
		t.Fatalf("label = %q", got)
	}
	first, _ := c.Ref(1, 1)
	if _, ok := c.Prev(first); ok {
		t.Fatal("Prev on the first episode should not find anything")
	}
}

func TestRefreshAndConditionalGet(t *testing.T) {
	payload := []byte(`{
		"series": {
			"titleFull": "Test Show",
			"vidPath": "https://example.test/media/",
			"seasons": [
				{"seasNum": 1, "episodes": [
					{"epNum": 1, "epTitle": "Pilot", "epSubs": [{"lang":"en","label":"English"}]}
				]}
			]
		}
	}`)

	var requests, conditional int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("If-None-Match") == `"v1"` {
			conditional++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	old := RemoteURL
	RemoteURL = srv.URL
	defer func() { RemoteURL = old }()

	dir := t.TempDir()
	store := Store{Dir: dir}

	cat, updated, err := store.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !updated || cat.Origin != "network" || len(cat.Refs()) != 1 {
		t.Fatalf("first refresh: updated=%v origin=%q refs=%d", updated, cat.Origin, len(cat.Refs()))
	}
	if _, err := os.Stat(filepath.Join(dir, "db.json")); err != nil {
		t.Fatalf("cache was not written: %v", err)
	}

	cat, updated, err = store.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("second refresh should be a 304")
	}
	if conditional != 1 {
		t.Fatalf("conditional requests = %d, want 1", conditional)
	}
	if cat.Origin != "cache" {
		t.Fatalf("origin = %q, want cache", cat.Origin)
	}

	// Load must prefer the cache.
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Origin != "cache" || len(loaded.Refs()) != 1 {
		t.Fatalf("Load = %+v", loaded)
	}
	if requests < 2 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestMetaRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := Store{Dir: dir}
	m := meta{ETag: `"x"`, FetchedAt: time.Now(), URL: "https://example.test/db.json"}
	store.writeMeta(m)
	got := store.readMeta()
	if got.ETag != m.ETag || got.URL != m.URL || got.FetchedAt.IsZero() {
		t.Fatalf("meta = %+v", got)
	}
}

func TestLoadRecoversFromCorruptCache(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := Store{Dir: dir}
	cat, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cat.Origin != "embedded" {
		t.Fatalf("origin = %q", cat.Origin)
	}
	if _, err := os.Stat(filepath.Join(dir, "db.json")); !os.IsNotExist(err) {
		t.Fatalf("corrupt cache was not removed (err=%v)", err)
	}
}

func TestParseRejectsBadCatalog(t *testing.T) {
	if _, err := Parse([]byte(`{"series":{"seasons":[]}}`), "test"); err == nil {
		t.Fatal("expected an error for an empty catalog")
	}
	if _, err := Parse([]byte(`not json`), "test"); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}

func TestCatalogJSONShape(t *testing.T) {
	c, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(c.DB)
	if err != nil {
		t.Fatal(err)
	}
	var back DB
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(len(back.Series.Seasons)) != fmt.Sprint(len(c.DB.Series.Seasons)) {
		t.Fatal("catalog did not round-trip")
	}
}
