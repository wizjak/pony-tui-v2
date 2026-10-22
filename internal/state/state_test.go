package state

import (
	"testing"
)

func TestPersistenceRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	s, err := DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	if !s.SetPosition(4, 5, Position{Seconds: 123.5, Duration: 1300}) {
		t.Fatal("SetPosition reported no change")
	}
	s.SetLast(Last{Season: 4, Episode: 5, Title: "Testing"})
	s.SetPreferences("720p", "es", false, 80)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	again, err := DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	p, ok := again.Position(4, 5)
	if !ok || p.Seconds != 123.5 || p.Duration != 1300 {
		t.Fatalf("position = %+v, ok=%v", p, ok)
	}
	last := again.Snapshot().Last
	if last == nil || last.Season != 4 || last.Episode != 5 || last.Title != "Testing" {
		t.Fatalf("last = %+v", last)
	}
	q, subs, autoplay, vol := again.Preferences()
	if q != "720p" || subs != "es" || autoplay || vol != 80 {
		t.Fatalf("prefs = %q %q %v %d", q, subs, autoplay, vol)
	}

	if !again.ClearPosition(4, 5) {
		t.Fatal("ClearPosition reported no change")
	}
	if _, ok := again.Position(4, 5); ok {
		t.Fatal("position was not cleared")
	}
}

func TestDefaults(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s, err := DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	_, _, autoplay, vol := s.Preferences()
	if !autoplay || vol != 100 {
		t.Fatalf("defaults: autoplay=%v volume=%d", autoplay, vol)
	}
}
