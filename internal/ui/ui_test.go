package ui

import (
	"fmt"
	"os"
	"testing"

	"pony/internal/catalog"
	"pony/internal/source"
	"pony/internal/state"
)

func testDeps(t *testing.T) Deps {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cat, err := catalog.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	cs, err := catalog.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	return Deps{Catalog: cat, CatalogStore: cs, State: st, Resolver: source.New()}
}

func TestPreviewBrowse(t *testing.T) {
	if os.Getenv("PONY_PREVIEW") == "" {
		t.Skip("set PONY_PREVIEW=1 to print the rendered UI")
	}
	m := New(testDeps(t), Options{Quality: "1080p"})
	m.width, m.height = 100, 30
	fmt.Println(m.View())
}

func TestPreviewCompact(t *testing.T) {
	if os.Getenv("PONY_PREVIEW") == "" {
		t.Skip("set PONY_PREVIEW=1 to print the rendered UI")
	}
	m := New(testDeps(t), Options{Quality: "1080p"})
	m.width, m.height = 58, 20
	m.focus = 1
	fmt.Println(m.View())
}

func TestPreviewPlayer(t *testing.T) {
	if os.Getenv("PONY_PREVIEW") == "" {
		t.Skip("set PONY_PREVIEW=1 to print the rendered UI")
	}
	m := New(testDeps(t), Options{Quality: "1080p"})
	m.width, m.height = 100, 30
	ref, _ := m.cat.Ref(1, 1)
	m.ref = ref
	m.resolved = source.Result{URL: "x", Quality: "1080p"}
	m.playDur = 1300
	m.playPos = 312
	m.playSubs = "en"
	m.screen = screenPlayer
	fmt.Println(m.View())
}

func TestNavigationAndSearch(t *testing.T) {
	m := New(testDeps(t), Options{Quality: "1080p"})
	m.width, m.height = 100, 30

	// Move down in the collection list.
	m.moveSeason(1)
	if got := m.cat.Categories()[m.seasonIdx].Num; got != 2 {
		t.Fatalf("season = %d, want 2", got)
	}

	// Local search only sees the current collection.
	m.search = "return"
	got := m.visibleRefs()
	if len(got) != 2 || got[0].Season != 2 {
		t.Fatalf("local search = %+v", got)
	}

	// Global search sees the whole catalog.
	m.global = true
	m.search = "rainbow"
	got = m.visibleRefs()
	if len(got) < 3 {
		t.Fatalf("global search returned %d refs", len(got))
	}
	for _, r := range got {
		if r.Season == 2 && r.Number == 1 {
			t.Fatalf("unexpected match: %+v", r)
		}
	}
}

func TestDigitJump(t *testing.T) {
	m := New(testDeps(t), Options{Quality: "1080p"})
	m.focus = 1
	m.jumpDigit("1")
	m.jumpDigit("3")
	if refs := m.visibleRefs(); refs[m.epIdx].Number != 13 {
		t.Fatalf("jumped to %d, want 13", refs[m.epIdx].Number)
	}
}
