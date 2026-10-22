package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestResolveFallbackAndCache(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/s01e02-720p.mp4" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	r := New()
	res, err := r.Resolve(context.Background(), srv.URL+"/", 1, 2, Qualities("1080p"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Quality != "720p" || !res.Fallback {
		t.Fatalf("res = %+v", res)
	}
	if !strings.HasSuffix(res.URL, "/s01e02-720p.mp4") {
		t.Fatalf("url = %q", res.URL)
	}

	before := hits.Load()
	if _, err := r.Resolve(context.Background(), srv.URL+"/", 1, 2, Qualities("1080p")); err != nil {
		t.Fatal(err)
	}
	if after := hits.Load(); after != before {
		t.Fatalf("cache miss: %d new requests", after-before)
	}
}

func TestResolveHeadRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Range") != "bytes=0-0" {
			t.Errorf("missing range header: %q", r.Header.Get("Range"))
		}
		if r.URL.Path == "/s02e03-1080p.mp4" {
			w.WriteHeader(http.StatusPartialContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	res, err := New().Resolve(context.Background(), srv.URL+"/", 2, 3, []string{"1080p"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Quality != "1080p" {
		t.Fatalf("quality = %q", res.Quality)
	}
}

func TestResolveNothing(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	if _, err := New().Resolve(context.Background(), srv.URL+"/", 9, 9, []string{"1080p", "720p"}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestQualityCycle(t *testing.T) {
	if got := NextQuality("1080p"); got != "720p" {
		t.Fatalf("1080p -> %q", got)
	}
	if got := NextQuality("720p"); got != "480p" {
		t.Fatalf("720p -> %q", got)
	}
	if got := NextQuality("480p"); got != "1080p" {
		t.Fatalf("480p -> %q", got)
	}
	if got := NextQuality("360p"); got != "1080p" {
		t.Fatalf("360p -> %q", got)
	}
	q := Qualities("720p")
	want := []string{"720p", "480p", "360p", "1080p"}
	if len(q) != len(want) {
		t.Fatalf("Qualities = %v", q)
	}
	for i := range want {
		if q[i] != want[i] {
			t.Fatalf("Qualities = %v, want %v", q, want)
		}
	}
	if q := Qualities("1080p"); q[1] != "720p" {
		t.Fatalf("1080p fallback order = %v", q)
	}
	if q := Qualities("480p"); q[1] != "360p" || q[2] != "720p" {
		t.Fatalf("480p fallback order = %v", q)
	}
}
