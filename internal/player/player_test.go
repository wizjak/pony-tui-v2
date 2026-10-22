package player

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestPlaybackIPC(t *testing.T) {
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("mpv not installed")
	}

	p, err := Start(context.Background(), Options{
		URL:    "av://lavfi:testsrc=size=320x240:rate=10:duration=8",
		Title:  "pony test",
		Volume: 50,
		ExtraArgs: []string{
			"--vo=null",
			"--ao=null",
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	waitFor(t, "duration", func() bool {
		st := p.Status()
		return st.Duration > 5
	})

	if err := p.SeekAbs(3); err != nil {
		t.Fatalf("SeekAbs: %v", err)
	}
	waitFor(t, "time-pos after seek", func() bool {
		return p.Status().TimePos >= 2.5
	})

	if err := p.SetPaused(true); err != nil {
		t.Fatalf("SetPaused: %v", err)
	}
	waitFor(t, "paused status", func() bool { return p.Status().Paused })

	if err := p.SetPaused(false); err != nil {
		t.Fatalf("resume: %v", err)
	}
	waitFor(t, "resumed status", func() bool { return !p.Status().Paused })

	p.Stop()
	select {
	case <-p.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("mpv did not exit after Stop")
	}
}
