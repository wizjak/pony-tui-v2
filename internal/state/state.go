// Package state persists watch history and preferences.
//
// State lives in $XDG_STATE_HOME/pony/state.json (usually
// ~/.local/state/pony/state.json), matching the XDG base directory spec.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Position is the last known playback position for one episode.
type Position struct {
	Seconds  float64 `json:"seconds"`
	Duration float64 `json:"duration,omitempty"`
}

// Last is the most recently played episode.
type Last struct {
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
	Title   string `json:"title"`
}

// State is the whole persisted document.
type State struct {
	Last      *Last               `json:"last,omitempty"`
	Positions map[string]Position `json:"positions,omitempty"`
	Quality   string              `json:"quality,omitempty"`
	Subtitles string              `json:"subtitles,omitempty"`
	Autoplay  *bool               `json:"autoplay,omitempty"`
	Volume    *int                `json:"volume,omitempty"`
}

// Store provides concurrency-safe access to the state file.
type Store struct {
	path string

	mu sync.Mutex
	s  State
}

// DefaultStore loads (or initializes) the state file.
func DefaultStore() (*Store, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot determine home directory: %w", err)
		}
		base = filepath.Join(home, ".local", "state")
	}
	st := &Store{path: filepath.Join(base, "pony", "state.json")}
	if data, err := os.ReadFile(st.path); err == nil {
		_ = json.Unmarshal(data, &st.s)
	}
	if st.s.Positions == nil {
		st.s.Positions = map[string]Position{}
	}
	return st, nil
}

// Path returns the state file location.
func (s *Store) Path() string { return s.path }

func key(season, episode int) string {
	return fmt.Sprintf("%d:%d", season, episode)
}

// Snapshot returns a copy of the current state.
func (s *Store) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.s
	out.Positions = make(map[string]Position, len(s.s.Positions))
	for k, v := range s.s.Positions {
		out.Positions[k] = v
	}
	if s.s.Last != nil {
		l := *s.s.Last
		out.Last = &l
	}
	return out
}

// Position returns the saved position for an episode.
func (s *Store) Position(season, episode int) (Position, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.s.Positions[key(season, episode)]
	return p, ok
}

// SetPosition saves a playback position and returns whether it changed.
func (s *Store) SetPosition(season, episode int, p Position) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.s.Positions == nil {
		s.s.Positions = map[string]Position{}
	}
	old, ok := s.s.Positions[key(season, episode)]
	if ok && nearlyEqual(old.Seconds, p.Seconds) && nearlyEqual(old.Duration, p.Duration) {
		return false
	}
	s.s.Positions[key(season, episode)] = p
	return true
}

// ClearPosition removes a saved position (e.g. after finishing an episode).
func (s *Store) ClearPosition(season, episode int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(season, episode)
	if _, ok := s.s.Positions[k]; !ok {
		return false
	}
	delete(s.s.Positions, k)
	return true
}

// SetLast remembers the most recently started episode.
func (s *Store) SetLast(l Last) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.s.Last = &l
}

// Preferences returns the persisted knobs.
func (s *Store) Preferences() (quality, subtitles string, autoplay bool, volume int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	quality, subtitles = s.s.Quality, s.s.Subtitles
	autoplay = true
	if s.s.Autoplay != nil {
		autoplay = *s.s.Autoplay
	}
	volume = 100
	if s.s.Volume != nil {
		volume = *s.s.Volume
	}
	return
}

// SetPreferences updates the persisted knobs.
func (s *Store) SetPreferences(quality, subtitles string, autoplay bool, volume int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.s.Quality = quality
	s.s.Subtitles = subtitles
	s.s.Autoplay = &autoplay
	s.s.Volume = &volume
}

// Save atomically writes the state document.
func (s *Store) Save() error {
	s.mu.Lock()
	data, err := json.MarshalIndent(s.s, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "state.*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}

func nearlyEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1.0
}
