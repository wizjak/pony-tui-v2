// Package ui implements the interactive terminal interface.
package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pony/internal/catalog"
	"pony/internal/player"
	"pony/internal/source"
	"pony/internal/state"
)

// Deps are the long-lived services shared by the UI.
type Deps struct {
	Catalog      *catalog.Catalog
	CatalogStore catalog.Store
	State        *state.Store
	Resolver     *source.Resolver
	MpvPath      string
}

// Options configures the initial UI state.
type Options struct {
	Direct       *catalog.Ref
	Season       int // open this collection on startup
	Quality      string
	Subs         string
	Autoplay     *bool
	Volume       *int
	ExitOnBack   bool
	PlayPosition float64
}

type screen int

const (
	screenBrowse screen = iota
	screenPlayer
	screenHelp
)

type intent int

const (
	intentPlay intent = iota
	intentRestart
)

type tickMsg struct{}

type catalogMsg struct {
	cat     *catalog.Catalog
	updated bool
	err     error
}

type resolveMsg struct {
	it      intent
	ref     catalog.Ref
	res     source.Result
	startAt float64
	err     error
}

type startMsg struct {
	ref     catalog.Ref
	res     source.Result
	pl      *player.Player
	subs    string
	startAt float64
	paused  bool
	err     error
}

// Model is the bubbletea model.
type Model struct {
	deps   Deps
	cat    *catalog.Catalog
	st     *state.Store
	res    *source.Resolver
	mpv    string
	width  int
	height int

	screen   screen
	helpFrom screen

	// Browse state.
	focus     int // 0 = collections, 1 = episodes
	seasonIdx int
	epIdx     int
	seasonOff int
	epOff     int
	search    string
	searching bool
	global    bool
	digitBuf  string
	digitAt   time.Time

	// Preferences.
	quality  string
	subs     string
	autoplay bool
	volume   int

	// Playback state.
	pl         *player.Player
	ref        catalog.Ref
	resolved   source.Result
	playSubs   string
	playPos    float64
	playDur    float64
	paused     bool
	switching  bool
	lastSaved  float64
	lastSaveAt time.Time
	playNote   string
	playNoteAt time.Time

	// Transient UI state.
	notice     string
	noticeAt   time.Time
	loading    bool
	loadMsg    string
	spinner    int
	errMsg     string
	exitOnBack bool
	quit       bool

	directPos float64
}

// New builds the UI model.
func New(d Deps, o Options) *Model {
	m := &Model{
		deps:       d,
		cat:        d.Catalog,
		st:         d.State,
		res:        d.Resolver,
		mpv:        d.MpvPath,
		width:      80,
		height:     24,
		quality:    "1080p",
		subs:       "",
		autoplay:   true,
		volume:     100,
		exitOnBack: o.ExitOnBack,
		directPos:  o.PlayPosition,
	}

	if q, s, a, v := d.State.Preferences(); q != "" {
		m.quality, m.subs, m.autoplay, m.volume = q, s, a, v
	} else {
		m.subs, m.autoplay, m.volume = s, a, v
	}
	if o.Quality != "" {
		m.quality = o.Quality
	}
	if o.Subs != "" {
		if strings.EqualFold(o.Subs, "off") {
			m.subs = ""
		} else {
			m.subs = o.Subs
		}
	}
	if o.Autoplay != nil {
		m.autoplay = *o.Autoplay
	}
	if o.Volume != nil {
		m.volume = *o.Volume
	}

	// Start on the last watched collection, when there is one.
	if last := d.State.Snapshot().Last; last != nil {
		if i := findSeasonIndex(m.cat, last.Season); i >= 0 {
			m.seasonIdx = i
			if refs := m.seasonRefs(); len(refs) > 0 {
				m.epIdx = findEpisodeIndex(refs, last.Episode)
			}
		}
	}
	if o.Season > 0 {
		if i := findSeasonIndex(m.cat, o.Season); i >= 0 {
			m.seasonIdx = i
			m.focus = 1
		}
	}
	if o.Direct != nil {
		m.ref = *o.Direct
		m.selectRef(*o.Direct)
	}

	return m
}

func findSeasonIndex(c *catalog.Catalog, num int) int {
	cats := c.Categories()
	for i, cat := range cats {
		if cat.Num == num {
			return i
		}
	}
	return -1
}

func findEpisodeIndex(refs []catalog.Ref, number int) int {
	for i, r := range refs {
		if r.Number == number {
			return i
		}
	}
	return 0
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tickCmd()}
	if m.deps.CatalogStore.Dir != "" && m.deps.CatalogStore.Age() > 24*time.Hour {
		cmds = append(cmds, m.refreshCatalogCmd())
	}
	if m.ref.Season != 0 {
		cmds = append(cmds, m.beginPlay(m.ref, m.initialStartAt(m.ref)))
	}
	return tea.Batch(cmds...)
}

func tickCmd() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *Model) refreshCatalogCmd() tea.Cmd {
	store := m.deps.CatalogStore
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		cat, updated, err := store.Refresh(ctx)
		return catalogMsg{cat: cat, updated: updated, err: err}
	}
}

func (m *Model) resolveCmd(ref catalog.Ref, quality string, startAt float64, it intent) tea.Cmd {
	resolver := m.res
	base := m.cat.DB.Series.VideoBase()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := resolver.Resolve(ctx, base, ref.Season, ref.Number, source.Qualities(quality))
		return resolveMsg{it: it, ref: ref, res: res, startAt: startAt, err: err}
	}
}

func (m *Model) startCmd(ref catalog.Ref, res source.Result, startAt float64, subs string) tea.Cmd {
	title := catalog.RefLabel(ref) + " — " + ref.Title
	subFile := ""
	if subs != "" {
		subFile = m.cat.DB.Series.SubtitleURL(ref.Season, ref.Number, subs)
	}
	mpv := m.mpv
	vol := m.volume
	paused := m.paused
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		pl, err := player.Start(ctx, player.Options{
			MpvPath: mpv,
			URL:     res.URL,
			Title:   title,
			StartAt: startAt,
			SubFile: subFile,
			Volume:  vol,
			Paused:  paused,
		})
		return startMsg{ref: ref, res: res, pl: pl, subs: subs, startAt: startAt, paused: paused, err: err}
	}
}

func (m *Model) beginPlay(ref catalog.Ref, startAt float64) tea.Cmd {
	m.loading = true
	m.loadMsg = fmt.Sprintf("Resolving %s · %s…", catalog.RefLabel(ref), m.quality)
	m.switching = false
	m.paused = false
	return m.resolveCmd(ref, m.quality, startAt, intentPlay)
}

func (m *Model) initialStartAt(ref catalog.Ref) float64 {
	if m.directPos > 0 {
		return m.directPos
	}
	return m.resumeTarget(ref)
}

func (m *Model) resumeTarget(ref catalog.Ref) float64 {
	p, ok := m.st.Position(ref.Season, ref.Number)
	if !ok || p.Seconds < 15 {
		return 0
	}
	if p.Duration > 0 && p.Seconds > p.Duration-30 {
		return 0
	}
	return p.Seconds
}

func (m *Model) savePrefs() {
	m.st.SetPreferences(m.quality, m.subs, m.autoplay, m.volume)
	_ = m.st.Save()
}

func (m *Model) setNotice(format string, args ...any) {
	m.notice = fmt.Sprintf(format, args...)
	m.noticeAt = time.Now()
}

func (m *Model) stopPlayer(save bool) {
	if m.pl == nil {
		return
	}
	if save {
		dur := m.playDur
		if dur <= 0 {
			dur = m.pl.Status().Duration
		}
		// Do not clobber a good resume point while mpv is still buffering.
		if m.playPos > 1 {
			m.st.SetPosition(m.ref.Season, m.ref.Number, state.Position{Seconds: m.playPos, Duration: dur})
			_ = m.st.Save()
		}
	}
	m.pl.Stop()
	m.pl = nil
}

// Close saves state and makes sure mpv is gone.
func (m *Model) Close() {
	m.stopPlayer(true)
	m.savePrefs()
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		return m.handleTick()
	case catalogMsg:
		return m.handleCatalog(msg)
	case resolveMsg:
		return m.handleResolve(msg)
	case startMsg:
		return m.handleStart(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleTick() (tea.Model, tea.Cmd) {
	m.spinner++
	if m.digitBuf != "" && time.Since(m.digitAt) > 1200*time.Millisecond {
		m.digitBuf = ""
	}
	if m.screen == screenPlayer && m.pl != nil {
		st := m.pl.Status()
		if st.Duration > 0 || st.TimePos > 0 {
			m.playPos = st.TimePos
		}
		if st.Duration > 0 {
			m.playDur = st.Duration
		}
		m.paused = st.Paused
		if st.Volume > 0 {
			m.volume = st.Volume
		}
		if m.playDur > 0 && time.Since(m.lastSaveAt) > 3*time.Second {
			if d := m.playPos - m.lastSaved; d > 3 || d < -3 {
				m.st.SetPosition(m.ref.Season, m.ref.Number, state.Position{Seconds: m.playPos, Duration: m.playDur})
				_ = m.st.Save()
				m.lastSaved = m.playPos
				m.lastSaveAt = time.Now()
			}
		}
		select {
		case <-m.pl.Done():
			return m.afterPlayerExit()
		default:
		}
	}
	return m, tickCmd()
}

func (m *Model) handleCatalog(msg catalogMsg) (tea.Model, tea.Cmd) {
	if msg.cat != nil {
		old := m.ref
		m.cat = msg.cat
		if old.Season > 0 {
			m.selectRef(old)
		} else {
			m.seasonIdx = clamp(m.seasonIdx, 0, len(m.cat.Categories())-1)
			m.epIdx = clamp(m.epIdx, 0, maxInt(0, len(m.seasonRefs())-1))
		}
	}
	switch {
	case msg.err != nil:
		m.setNotice("Catalog update failed: %v", msg.err)
	case msg.updated:
		m.setNotice("Catalog updated")
	default:
		m.setNotice("Catalog is up to date")
	}
	return m, nil
}

func (m *Model) handleResolve(msg resolveMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.loading = false
		m.switching = false
		if msg.it == intentRestart {
			m.setNotice("%v", msg.err)
			return m, nil
		}
		m.errMsg = msg.err.Error()
		return m, nil
	}
	if msg.it == intentRestart {
		pos := m.playPos
		m.stopPlayer(true)
		m.loading = true
		m.loadMsg = "Switching quality…"
		return m, m.startCmd(msg.ref, msg.res, pos, m.playSubs)
	}
	m.loadMsg = "Starting mpv…"
	return m, m.startCmd(msg.ref, msg.res, msg.startAt, m.subtitleFor(msg.ref))
}

// subtitleFor filters the preferred subtitle language against what the
// episode actually offers, so mpv never receives a URL for a missing track.
func (m *Model) subtitleFor(ref catalog.Ref) string {
	if m.subs == "" || strings.EqualFold(m.subs, "off") {
		return ""
	}
	if len(ref.Subs) == 0 {
		return m.subs
	}
	for _, s := range ref.Subs {
		if s.Lang == m.subs {
			return m.subs
		}
	}
	return ""
}

func (m *Model) handleStart(msg startMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	m.switching = false
	if msg.err != nil {
		m.errMsg = msg.err.Error()
		m.pl = nil
		m.screen = screenBrowse
		return m, nil
	}
	if m.pl != nil {
		m.pl.Stop()
	}
	m.pl = msg.pl
	m.ref = msg.ref
	m.resolved = msg.res
	m.playSubs = msg.subs
	m.playPos = msg.startAt
	m.playDur = 0
	m.paused = msg.paused
	m.lastSaved = msg.startAt
	m.lastSaveAt = time.Now()
	m.screen = screenPlayer
	m.loading = false

	m.st.SetLast(state.Last{Season: msg.ref.Season, Episode: msg.ref.Number, Title: msg.ref.Title})
	m.savePrefs()

	if msg.startAt >= 15 {
		m.playNote = "resumed at " + formatTime(msg.startAt) + " — press 0 to restart"
		m.playNoteAt = time.Now()
	} else if msg.res.Fallback {
		m.playNote = "preferred quality unavailable — playing " + msg.res.Quality
		m.playNoteAt = time.Now()
	}
	return m, nil
}

func (m *Model) afterPlayerExit() (tea.Model, tea.Cmd) {
	pl := m.pl
	if pl == nil {
		return m, nil
	}
	st := pl.Status()
	m.playPos = st.TimePos
	m.playDur = st.Duration
	err := pl.Err()
	requested := pl.RequestedStop()
	tail := pl.StderrTail()
	eof := st.EndReason == "eof" ||
		(st.Duration > 0 && st.TimePos >= st.Duration-1.5)
	pl.Stop()
	m.pl = nil
	m.screen = screenBrowse
	m.selectRef(m.ref)

	if eof {
		m.st.ClearPosition(m.ref.Season, m.ref.Number)
		_ = m.st.Save()
		if m.autoplay {
			if next, ok := m.cat.Next(m.ref); ok {
				return m, m.beginPlay(next, 0)
			}
			m.setNotice("That was the last episode in the catalog")
		}
		return m, nil
	}
	if !requested {
		if m.playPos > 1 {
			m.st.SetPosition(m.ref.Season, m.ref.Number, state.Position{Seconds: m.playPos, Duration: m.playDur})
			_ = m.st.Save()
		}
		msg := "mpv exited unexpectedly"
		if err != nil {
			msg += ": " + err.Error()
		}
		if tail != "" {
			msg += "\n" + tail
		}
		m.errMsg = msg
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		m.quit = true
		return m, tea.Quit
	}
	if m.errMsg != "" {
		m.errMsg = ""
		return m, nil
	}
	if m.loading {
		if k == "q" {
			m.quit = true
			m.stopPlayer(true)
			return m, tea.Quit
		}
		return m, nil
	}
	if m.screen == screenHelp {
		m.screen = m.helpFrom
		return m, nil
	}
	switch m.screen {
	case screenBrowse:
		return m.keyBrowse(msg)
	case screenPlayer:
		return m.keyPlayer(msg)
	}
	return m, nil
}

func (m *Model) keyBrowse(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()

	if m.searching {
		switch msg.Type {
		case tea.KeyRunes:
			m.search += string(msg.Runes)
			m.epIdx, m.epOff = 0, 0
			return m, nil
		}
		switch k {
		case "esc":
			m.searching = false
			m.search = ""
			m.global = false
			m.epIdx, m.epOff = 0, 0
		case "enter":
			m.searching = false
		case "backspace":
			if m.search != "" {
				r := []rune(m.search)
				m.search = string(r[:len(r)-1])
				m.epIdx, m.epOff = 0, 0
			}
		case "ctrl+f":
			m.global = !m.global
			m.epIdx, m.epOff = 0, 0
		case "up":
			m.moveEp(-1)
		case "down":
			m.moveEp(1)
		case "tab":
			m.focus = 1
		case "ctrl+c":
			m.quit = true
			return m, tea.Quit
		}
		return m, nil
	}

	switch k {
	case "q":
		m.quit = true
		return m, tea.Quit
	case "?":
		m.helpFrom = screenBrowse
		m.screen = screenHelp
		return m, nil
	case "/":
		m.focus = 1
		m.searching = true
		m.search = ""
		m.global = false
		m.epIdx, m.epOff = 0, 0
		return m, nil
	case "ctrl+f":
		m.focus = 1
		m.searching = true
		m.search = ""
		m.global = true
		m.epIdx, m.epOff = 0, 0
		return m, nil
	case "tab", "left", "right":
		m.focus = 1 - m.focus
		return m, nil
	case "h":
		m.focus = 0
		return m, nil
	case "l":
		m.focus = 1
		return m, nil
	case "up", "k":
		if m.focus == 0 {
			m.moveSeason(-1)
		} else {
			m.moveEp(-1)
		}
		return m, nil
	case "down", "j":
		if m.focus == 0 {
			m.moveSeason(1)
		} else {
			m.moveEp(1)
		}
		return m, nil
	case "pgup":
		if m.focus == 1 {
			m.moveEp(-10)
		}
		return m, nil
	case "pgdown":
		if m.focus == 1 {
			m.moveEp(10)
		}
		return m, nil
	case "home", "g":
		if m.focus == 0 {
			m.seasonIdx, m.seasonOff = 0, 0
			m.epIdx, m.epOff = 0, 0
		} else {
			m.epIdx, m.epOff = 0, 0
		}
		return m, nil
	case "end", "G":
		if m.focus == 0 {
			m.seasonIdx = len(m.cat.Categories()) - 1
			m.epIdx, m.epOff = 0, 0
		} else {
			m.epIdx = maxInt(0, len(m.visibleRefs())-1)
		}
		return m, nil
	case "enter":
		if m.focus == 0 {
			m.focus = 1
			return m, nil
		}
		return m.playSelected()
	case "a":
		last := m.st.Snapshot().Last
		if last == nil {
			m.setNotice("No episode watched yet")
			return m, nil
		}
		ref, ok := m.cat.Ref(last.Season, last.Episode)
		if !ok {
			m.setNotice("Last episode is no longer in the catalog")
			return m, nil
		}
		m.selectRef(ref)
		return m, m.beginPlay(ref, m.resumeTarget(ref))
	case "r":
		m.quality = source.NextQuality(m.quality)
		m.savePrefs()
		m.setNotice("Quality preference: %s", m.quality)
		return m, nil
	case "v":
		m.subs = nextSubtitlePref(m.subs)
		m.savePrefs()
		m.setNotice("Subtitles: %s", subtitleName(m.subs))
		return m, nil
	case "s":
		m.autoplay = !m.autoplay
		m.savePrefs()
		m.setNotice("Autoplay: %s", onOff(m.autoplay))
		return m, nil
	case "R":
		m.setNotice("Updating catalog…")
		return m, m.refreshCatalogCmd()
	case "esc":
		if m.search != "" {
			m.search, m.global = "", false
			m.epIdx, m.epOff = 0, 0
		}
		return m, nil
	}

	if len(k) == 1 && k[0] >= '0' && k[0] <= '9' {
		m.jumpDigit(k)
	}
	return m, nil
}

func (m *Model) keyPlayer(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pl == nil {
		m.screen = screenBrowse
		return m, nil
	}
	k := msg.String()
	if m.switching && k != "q" {
		return m, nil
	}
	switch k {
	case "q":
		m.quit = true
		m.stopPlayer(true)
		return m, tea.Quit
	case "b", "esc":
		m.stopPlayer(true)
		m.screen = screenBrowse
		m.selectRef(m.ref)
		if m.exitOnBack {
			m.quit = true
			return m, tea.Quit
		}
		return m, nil
	case "?":
		m.helpFrom = screenPlayer
		m.screen = screenHelp
		return m, nil
	case " ":
		_ = m.pl.TogglePause()
		return m, nil
	case "left", "h":
		_ = m.pl.SeekRel(-5)
		return m, nil
	case "right", "l":
		_ = m.pl.SeekRel(5)
		return m, nil
	case "H":
		_ = m.pl.SeekRel(-60)
		return m, nil
	case "L":
		_ = m.pl.SeekRel(60)
		return m, nil
	case "0":
		_ = m.pl.SeekAbs(0)
		return m, nil
	case "up", "+":
		_ = m.pl.AddVolume(5)
		return m, nil
	case "down", "-":
		_ = m.pl.AddVolume(-5)
		return m, nil
	case "n", "]":
		return m.nextEpisode()
	case "p", "[":
		return m.prevEpisode()
	case "a":
		m.autoplay = !m.autoplay
		m.savePrefs()
		m.playNote = "autoplay " + onOff(m.autoplay)
		m.playNoteAt = time.Now()
		return m, nil
	case "r":
		return m.changeQuality()
	case "v":
		return m.changeSubtitles()
	}
	return m, nil
}

func (m *Model) nextEpisode() (tea.Model, tea.Cmd) {
	next, ok := m.cat.Next(m.ref)
	if !ok {
		m.playNote = "Already at the last episode"
		m.playNoteAt = time.Now()
		return m, nil
	}
	m.stopPlayer(true)
	return m, m.beginPlay(next, 0)
}

func (m *Model) prevEpisode() (tea.Model, tea.Cmd) {
	prev, ok := m.cat.Prev(m.ref)
	if !ok {
		m.playNote = "Already at the first episode"
		m.playNoteAt = time.Now()
		return m, nil
	}
	m.stopPlayer(true)
	return m, m.beginPlay(prev, 0)
}

func (m *Model) changeQuality() (tea.Model, tea.Cmd) {
	q := source.NextQuality(m.quality)
	m.quality = q
	m.savePrefs()
	m.switching = true
	m.playNote = "checking " + q + "…"
	m.playNoteAt = time.Now()
	return m, m.resolveCmd(m.ref, q, m.playPos, intentRestart)
}

func (m *Model) changeSubtitles() (tea.Model, tea.Cmd) {
	next := m.nextEpisodeSubtitle()
	if next == m.playSubs {
		m.playNote = "No other subtitles for this episode"
		m.playNoteAt = time.Now()
		return m, nil
	}
	pos := m.playPos
	paused := m.paused
	m.playSubs = next
	m.subs = next
	m.savePrefs()
	m.stopPlayer(true)
	m.loading = true
	m.loadMsg = "Switching subtitles…"
	m.paused = paused
	return m, m.startCmd(m.ref, m.resolved, pos, next)
}

func (m *Model) nextEpisodeSubtitle() string {
	opts := []string{""}
	seen := map[string]bool{"": true}
	for _, s := range m.ref.Subs {
		if s.Lang != "" && !seen[s.Lang] {
			seen[s.Lang] = true
			opts = append(opts, s.Lang)
		}
	}
	cur := 0
	for i, o := range opts {
		if o == m.playSubs {
			cur = i
		}
	}
	return opts[(cur+1)%len(opts)]
}

func (m *Model) playSelected() (tea.Model, tea.Cmd) {
	refs := m.visibleRefs()
	if len(refs) == 0 {
		m.setNotice("No episodes match")
		return m, nil
	}
	m.epIdx = clamp(m.epIdx, 0, len(refs)-1)
	ref := refs[m.epIdx]
	m.ref = ref
	return m, m.beginPlay(ref, m.resumeTarget(ref))
}

func (m *Model) moveSeason(delta int) {
	n := len(m.cat.Categories())
	if n == 0 {
		return
	}
	m.seasonIdx = clamp(m.seasonIdx+delta, 0, n-1)
	m.epIdx, m.epOff = 0, 0
	m.search, m.searching, m.global = "", false, false
}

func (m *Model) moveEp(delta int) {
	refs := m.visibleRefs()
	if len(refs) == 0 {
		m.epIdx = 0
		return
	}
	m.epIdx = clamp(m.epIdx+delta, 0, len(refs)-1)
}

func (m *Model) jumpDigit(d string) {
	if m.digitBuf != "" && time.Since(m.digitAt) > 1200*time.Millisecond {
		m.digitBuf = ""
	}
	m.digitBuf += d
	m.digitAt = time.Now()
	n, err := strconv.Atoi(m.digitBuf)
	if err != nil {
		return
	}
	if m.focus == 0 {
		cats := m.cat.Categories()
		if n >= 1 && n <= len(cats) {
			m.seasonIdx = n - 1
			m.epIdx, m.epOff = 0, 0
		}
		return
	}
	for i, r := range m.visibleRefs() {
		if r.Number == n {
			m.epIdx = i
			return
		}
	}
	// Prefix match for multi-digit input still being typed.
	for i, r := range m.visibleRefs() {
		if strings.HasPrefix(strconv.Itoa(r.Number), m.digitBuf) {
			m.epIdx = i
			return
		}
	}
}

func (m *Model) seasonRefs() []catalog.Ref {
	cats := m.cat.Categories()
	if len(cats) == 0 {
		return nil
	}
	m.seasonIdx = clamp(m.seasonIdx, 0, len(cats)-1)
	return refsForSeason(m.cat, cats[m.seasonIdx].Num)
}

func refsForSeason(cat *catalog.Catalog, num int) []catalog.Ref {
	season, ok := cat.Season(num)
	if !ok {
		return nil
	}
	refs := make([]catalog.Ref, 0, len(season.Episodes))
	for _, e := range season.Episodes {
		refs = append(refs, catalog.Ref{Season: num, Number: e.Num, Title: e.Title, Subs: e.Subs})
	}
	return refs
}

func (m *Model) playerRefs() []catalog.Ref {
	return refsForSeason(m.cat, m.ref.Season)
}

func (m *Model) visibleRefs() []catalog.Ref {
	refs := m.seasonRefs()
	q := strings.ToLower(strings.TrimSpace(m.search))
	if q == "" {
		return refs
	}
	pool := refs
	if m.global {
		pool = m.cat.Refs()
	}
	out := make([]catalog.Ref, 0, len(pool))
	for _, r := range pool {
		hay := strings.ToLower(r.Title + " " + catalog.RefLabel(r))
		if m.global {
			hay += " " + strings.ToLower(catalog.CollectionLabel(r.Season))
		}
		if strings.Contains(hay, q) {
			out = append(out, r)
		}
	}
	return out
}

func (m *Model) selectRef(r catalog.Ref) {
	m.search, m.searching, m.global = "", false, false
	if i := findSeasonIndex(m.cat, r.Season); i >= 0 {
		m.seasonIdx = i
	}
	refs := m.seasonRefs()
	m.epIdx = findEpisodeIndex(refs, r.Number)
	m.focus = 1
}

func nextSubtitlePref(cur string) string {
	order := []string{"", "en", "es", "fr"}
	for i, s := range order {
		if s == cur {
			return order[(i+1)%len(order)]
		}
	}
	return ""
}

func subtitleName(s string) string {
	if s == "" {
		return "off"
	}
	return s
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
