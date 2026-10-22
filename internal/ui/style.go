package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Pastel palette, loosely matching the original pony-tui.
var (
	cPink  = lipgloss.Color("211")
	cLav   = lipgloss.Color("183")
	cMint  = lipgloss.Color("121")
	cSky   = lipgloss.Color("117")
	cSun   = lipgloss.Color("222")
	cRed   = lipgloss.Color("203")
	cGray  = lipgloss.Color("245")
	cWhite = lipgloss.Color("255")
	cSelBg = lipgloss.Color("97")
)

var (
	stTitle    = lipgloss.NewStyle().Bold(true).Foreground(cPink)
	stAccent   = lipgloss.NewStyle().Foreground(cLav)
	stMint     = lipgloss.NewStyle().Foreground(cMint)
	stSky      = lipgloss.NewStyle().Foreground(cSky)
	stSun      = lipgloss.NewStyle().Foreground(cSun)
	stRed      = lipgloss.NewStyle().Foreground(cRed)
	stDim      = lipgloss.NewStyle().Foreground(cGray)
	stWhite    = lipgloss.NewStyle().Foreground(cWhite)
	stBold     = lipgloss.NewStyle().Bold(true)
	stSelected = lipgloss.NewStyle().Background(cSelBg).Foreground(lipgloss.Color("231")).Bold(true)
	stBox      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cLav)
	stHelpBox  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cSky)
)

var rainbow = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.Color("204")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("215")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("228")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("120")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("117")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("141")),
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return ansi.Truncate(s, w, "…")
}

func padRight(s string, w int) string {
	n := w - ansi.StringWidth(s)
	if n <= 0 {
		return truncate(s, w)
	}
	return s + strings.Repeat(" ", n)
}

// truncateStyled clips text and closes any dangling ANSI style.
func truncateStyled(s string, w int) string {
	clipped := truncate(s, w)
	if clipped != s && strings.Contains(s, "\x1b") {
		clipped += "\x1b[0m"
	}
	return clipped
}

// twoSides lays out styled left/right text on one w-wide line.
func twoSides(left, right string, w int) string {
	left = truncateStyled(left, w)
	lw := ansi.StringWidth(left)
	rw := ansi.StringWidth(right)
	if lw+rw+1 > w {
		right = truncateStyled(right, maxInt(0, w-lw-1))
		rw = ansi.StringWidth(right)
	}
	gap := w - lw - rw
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func rainbowBar(w int) string {
	if w < 1 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < w; i++ {
		b.WriteString(rainbow[i%len(rainbow)].Render("━"))
	}
	return b.String()
}

func formatTime(seconds float64) string {
	if seconds < 0 || seconds != seconds { // NaN
		seconds = 0
	}
	t := int(seconds + 0.5)
	h := t / 3600
	m := (t % 3600) / 60
	s := t % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// renderList renders exactly h rows of width w, including a one-column
// scrollbar. off is adjusted so the selection stays visible.
func renderList(items []string, sel, off, w, h int, focused bool) []string {
	if h <= 0 {
		return nil
	}
	if w < 4 {
		w = 4
	}
	total := len(items)
	if sel >= total {
		sel = total - 1
	}
	if sel < 0 {
		sel = -1
	}
	off = clamp(off, 0, maxInt(0, total-h))
	if sel >= 0 && sel < off {
		off = sel
	}
	if sel >= 0 && sel >= off+h {
		off = sel - h + 1
	}
	off = clamp(off, 0, maxInt(0, total-h))

	thumbN, thumbS := 0, 0
	if total > h {
		thumbN = h * h / total
		if thumbN < 1 {
			thumbN = 1
		}
		if total-h > 0 {
			thumbS = off * (h - thumbN) / (total - h)
		}
	}

	out := make([]string, 0, h)
	textW := w - 1
	for row := 0; row < h; row++ {
		i := off + row
		var line string
		if i < total {
			text := truncate(items[i], textW-2)
			if i == sel {
				if focused {
					line = stSelected.Render(padRight("▸ "+text, textW))
				} else {
					line = stAccent.Render("▸") + padRight(" "+text, textW-1)
				}
			} else {
				line = padRight("  "+text, textW)
			}
		} else {
			line = strings.Repeat(" ", textW)
		}

		sb := " "
		if total > h {
			if row >= thumbS && row < thumbS+thumbN {
				sb = stAccent.Render("┃")
			} else {
				sb = stDim.Render("│")
			}
		}
		out = append(out, line+sb)
	}
	return out
}

func centerLines(lines []string, w, h int) []string {
	out := make([]string, 0, h)
	top := maxInt(0, (h-len(lines))/3)
	for i := 0; i < top; i++ {
		out = append(out, "")
	}
	for _, l := range lines {
		if len(out) >= h {
			break
		}
		pad := maxInt(0, (w-ansi.StringWidth(l))/2)
		out = append(out, strings.Repeat(" ", pad)+truncate(l, w-pad))
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out
}
