package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"pony/internal/catalog"
)

// View implements tea.Model.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		m.width, m.height = 80, 24
	}
	if m.cat == nil {
		return "pony: catalog unavailable\n"
	}

	header := m.headerLines()
	footer := m.footerLines()
	bodyH := m.height - len(header) - len(footer) - 2
	if bodyH < 3 {
		bodyH = 3
	}

	var body []string
	switch {
	case m.errMsg != "":
		body = m.errorBody(bodyH)
	case m.loading:
		body = m.loadingBody(bodyH)
	case m.screen == screenHelp:
		body = m.helpBody(bodyH)
	case m.screen == screenPlayer:
		body = m.playerBody(bodyH)
	default:
		body = m.browseBody(bodyH)
	}

	lines := make([]string, 0, m.height)
	lines = append(lines, header...)
	lines = append(lines, "")
	lines = append(lines, body...)
	lines = append(lines, "")
	lines = append(lines, footer...)

	for len(lines) < m.height {
		lines = append(lines, "")
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	for i := range lines {
		lines[i] = truncateStyled(lines[i], m.width)
	}
	return strings.Join(lines, "\n")
}

func (m *Model) headerLines() []string {
	title := fmt.Sprintf(" ♥ pony  ·  %s", m.cat.DB.Series.TitleFull)
	if m.width < 64 {
		title = " ♥ pony  ·  MLP:FiM"
	}
	bar := " " + rainbowBar(clamp(m.width-2, 10, 72))
	return []string{
		stTitle.Render(truncate(title, m.width)),
		bar,
	}
}

func (m *Model) footerLines() []string {
	var keys string
	switch {
	case m.searching:
		keys = "type to filter · enter keep · esc clear · ctrl+f search all · ↑↓ move"
	case m.screen == screenHelp:
		keys = "any key to go back"
	case m.screen == screenPlayer:
		keys = "space pause · ←/→ seek · n/p episode · r quality · v subtitles · a autoplay · b back · ? help · q quit"
	case m.errMsg != "":
		keys = "press any key to continue"
	default:
		keys = "↑↓ move · tab pane · enter play · / search · ctrl+f all · r quality · v subs · a resume · R refresh · ? help · q quit"
	}

	status := m.statusLine()
	return []string{
		" " + stDim.Render(truncate(keys, m.width-2)),
		" " + status,
	}
}

func (m *Model) statusLine() string {
	if n := time.Since(m.noticeAt); m.notice != "" && n < 4*time.Second && m.screen == screenBrowse {
		return stSun.Render(truncate(m.notice, m.width-2))
	}
	if m.screen == screenPlayer {
		if n := time.Since(m.playNoteAt); m.playNote != "" && n < 5*time.Second {
			return stSun.Render(truncate(m.playNote, m.width-2))
		}
		pos := 0
		refs := m.playerRefs()
		for i, r := range refs {
			if r.Number == m.ref.Number {
				pos = i + 1
				break
			}
		}
		s := fmt.Sprintf("%s · #%d of %d · %s · subs %s · autoplay %s · catalog %s",
			catalog.RefLabel(m.ref), pos, len(refs), m.resolved.Quality,
			subtitleName(m.playSubs), onOff(m.autoplay), m.catalogAge())
		return stDim.Render(truncate(s, m.width-2))
	}

	cats := m.cat.Categories()
	cat := "?"
	if len(cats) > 0 {
		cat = cats[clamp(m.seasonIdx, 0, len(cats)-1)].Label
	}
	shown, total := len(m.visibleRefs()), len(m.seasonRefs())
	if m.global && strings.TrimSpace(m.search) != "" {
		cat = "search all"
		total = len(m.cat.Refs())
	}
	s := fmt.Sprintf("%s · %d/%d eps · %s · subs %s · autoplay %s · catalog %s",
		cat, shown, total, m.quality, subtitleName(m.subs), onOff(m.autoplay), m.catalogAge())
	return stDim.Render(truncate(s, m.width-2))
}

func (m *Model) catalogAge() string {
	switch m.cat.Origin {
	case "embedded":
		return "built-in"
	case "network":
		return "updated"
	}
	age := m.cat.Age
	switch {
	case age < time.Minute:
		return "cached now"
	case age < time.Hour:
		return fmt.Sprintf("cached %dm", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("cached %dh", int(age.Hours()))
	default:
		return fmt.Sprintf("cached %dd", int(age.Hours()/24))
	}
}

func (m *Model) browseBody(h int) []string {
	wide := m.width >= 78 && h >= 8
	if wide {
		leftW := clamp(m.width/4+2, 22, 28)
		gap := 2
		rightW := m.width - leftW - gap
		left := m.pane("Collections", fmt.Sprintf("%d", len(m.cat.Categories())),
			m.collectionItems(), m.seasonIdx, m.seasonOff, leftW, h, m.focus == 0)
		right := m.pane(m.episodeTitle(), m.episodeCount(),
			m.episodeItems(), m.epIdx, m.epOff, rightW, h, m.focus == 1)
		out := make([]string, h)
		for i := 0; i < h; i++ {
			out[i] = left[i] + strings.Repeat(" ", gap) + right[i]
		}
		return out
	}

	if m.focus == 0 {
		return m.pane("Collections", fmt.Sprintf("%d", len(m.cat.Categories())),
			m.collectionItems(), m.seasonIdx, m.seasonOff, m.width, h, true)
	}
	return m.pane(m.episodeTitle(), m.episodeCount(),
		m.episodeItems(), m.epIdx, m.epOff, m.width, h, true)
}

func (m *Model) collectionItems() []string {
	cats := m.cat.Categories()
	out := make([]string, 0, len(cats))
	for _, c := range cats {
		marker := "★"
		if c.Num > 9 {
			marker = "✦"
		}
		out = append(out, fmt.Sprintf("%s %s (%d)", marker, c.Label, len(c.Episodes)))
	}
	return out
}

func (m *Model) episodeTitle() string {
	q := strings.TrimSpace(m.search)
	switch {
	case m.searching && q == "":
		if m.global {
			return "Search all episodes"
		}
		return "Search this collection"
	case q != "" && m.global:
		return fmt.Sprintf("Search all · %q", q)
	case q != "":
		return fmt.Sprintf("Search · %q", q)
	}
	if cats := m.cat.Categories(); len(cats) > 0 {
		return cats[clamp(m.seasonIdx, 0, len(cats)-1)].Label
	}
	return "Episodes"
}

func (m *Model) episodeCount() string {
	shown := len(m.visibleRefs())
	if m.global && strings.TrimSpace(m.search) != "" {
		return fmt.Sprintf("%d/%d", shown, len(m.cat.Refs()))
	}
	total := len(m.seasonRefs())
	if shown == 0 && total > 0 {
		return "no matches"
	}
	return fmt.Sprintf("%d/%d", shown, total)
}

func (m *Model) episodeItems() []string {
	refs := m.visibleRefs()
	global := m.global && strings.TrimSpace(m.search) != ""
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		if global {
			out = append(out, catalog.RefLabel(r)+"  "+r.Title)
		} else {
			out = append(out, fmt.Sprintf("%02d  %s", r.Number, r.Title))
		}
	}
	if len(out) == 0 {
		out = append(out, "no matches")
	}
	return out
}

func (m *Model) pane(title, right string, items []string, sel, off, w, h int, focused bool) []string {
	h = maxInt(h, 3)
	w = maxInt(w, 10)
	ts := stAccent
	if focused {
		ts = stTitle
	}
	out := make([]string, h)
	out[0] = twoSides(ts.Render("▍ "+title), stDim.Render(right), w)
	out[1] = stDim.Render(strings.Repeat("─", w))
	listH := h - 2
	selArg := sel
	if len(items) == 1 && items[0] == "no matches" {
		selArg = -1
	}
	rows := renderList(items, selArg, off, w, listH, focused)
	copy(out[2:], rows)
	return out
}

func (m *Model) playerBody(h int) []string {
	boxW := minInt(m.width-4, 74)
	if boxW < 30 {
		boxW = maxInt(20, m.width-2)
	}
	innerW := boxW - 4 // border (2) + horizontal padding (2)
	pad := maxInt(0, (m.width-boxW)/2)

	title := catalog.RefLabel(m.ref) + " — " + m.ref.Title
	refs := m.playerRefs()
	pos := 0
	for i, r := range refs {
		if r.Number == m.ref.Number {
			pos = i + 1
			break
		}
	}

	barW := clamp(innerW-16, 8, 40)
	filled := 0
	if m.playDur > 0 {
		filled = clamp(int(m.playPos/m.playDur*float64(barW)), 0, barW)
	}
	bar := stMint.Render(strings.Repeat("█", filled)) +
		stDim.Render(strings.Repeat("░", barW-filled))

	times := stDim.Render("--:-- / --:--")
	if m.playDur > 0 {
		times = stWhite.Render(formatTime(m.playPos)) + stDim.Render(" / "+formatTime(m.playDur))
	}
	pauseBadge := ""
	if m.paused {
		pauseBadge = stSun.Render("⏸ ")
	}
	progress := pauseBadge + bar + "  " + times

	info := fmt.Sprintf("#%d of %d · %s · subs %s · autoplay %s",
		pos, len(refs), m.resolved.Quality, subtitleName(m.playSubs), onOff(m.autoplay))

	content := []string{
		stBold.Render(truncate(title, innerW)),
		"",
		truncate(progress, innerW),
		"",
		stDim.Render(truncate(info, innerW)),
	}
	if n := time.Since(m.playNoteAt); m.playNote != "" && n < 5*time.Second {
		content = append(content, "", stSun.Render(truncate(m.playNote, innerW)))
	}
	box := stBox.Width(maxInt(innerW, 10)).Padding(0, 1).Render(strings.Join(content, "\n"))

	lines := []string{
		strings.Repeat(" ", pad) + stMint.Render("▶ NOW PLAYING"),
		"",
	}
	boxLines := strings.Split(box, "\n")
	for _, l := range boxLines {
		lines = append(lines, strings.Repeat(" ", pad)+l)
	}

	out := make([]string, 0, h)
	top := 0
	if len(lines) < h {
		top = maxInt(0, (h-len(lines))/3)
	}
	for i := 0; i < top; i++ {
		out = append(out, "")
	}
	out = append(out, lines...)
	for len(out) < h {
		out = append(out, "")
	}
	if len(out) > h {
		out = out[:h]
	}
	return out
}

func (m *Model) loadingBody(h int) []string {
	frame := spinnerFrames[m.spinner%len(spinnerFrames)]
	lines := []string{
		stMint.Render(frame+" ") + stWhite.Render(m.loadMsg),
		"",
		stDim.Render("ctrl+c to quit"),
	}
	return centerLines(lines, m.width, h)
}

func (m *Model) errorBody(h int) []string {
	w := clamp(m.width-8, 20, 74)
	content := []string{stRed.Bold(true).Render("✖ something went wrong"), ""}
	for _, line := range strings.Split(m.errMsg, "\n") {
		content = append(content, stWhite.Render(truncate(line, w-2)))
	}
	content = append(content, "", stDim.Render("press any key to continue"))
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cRed).
		Padding(0, 1).
		Width(w).
		Render(strings.Join(content, "\n"))
	lines := strings.Split(box, "\n")
	return centerLines(lines, m.width, h)
}

func (m *Model) helpBody(h int) []string {
	nav := []string{
		"↑/↓ j/k    move",
		"tab ←/→    switch pane",
		"enter      play / open",
		"/          search collection",
		"ctrl+f     search everything",
		"0-9        jump to number",
		"r          quality",
		"v          subtitles",
		"a          resume last",
		"s          autoplay",
		"R          refresh catalog",
		"q          quit",
	}
	play := []string{
		"space      pause / resume",
		"←/→ h/l    seek 5 seconds",
		"H/L        seek 60 seconds",
		"n  ]       next episode",
		"p  [       previous episode",
		"r          change quality",
		"v          change subtitles",
		"a          autoplay",
		"0          restart episode",
		"b / esc    back to browse",
		"q          quit",
	}
	innerW := clamp(m.width-8, 30, 64)
	lines := []string{stTitle.Render("pony · keyboard"), ""}
	if innerW >= 58 {
		colW := innerW/2 - 1
		lines = append(lines, stAccent.Render("Browse")+"  "+strings.Repeat(" ", maxInt(1, colW-7))+stAccent.Render("Player"))
		rows := maxInt(len(nav), len(play))
		for i := 0; i < rows; i++ {
			l, r := "", ""
			if i < len(nav) {
				l = nav[i]
			}
			if i < len(play) {
				r = play[i]
			}
			lines = append(lines, padRight(l, colW)+" "+r)
		}
	} else {
		lines = append(lines, stAccent.Render("Browse"))
		lines = append(lines, nav...)
		lines = append(lines, "", stAccent.Render("Player"))
		lines = append(lines, play...)
	}
	box := stHelpBox.Padding(0, 1).Width(innerW).Render(strings.Join(lines, "\n"))
	out := strings.Split(box, "\n")
	if len(out) > h {
		out = out[:h]
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out
}
