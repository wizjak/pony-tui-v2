// Command pony is a terminal browser and player for My Little Pony:
// Friendship is Magic, using the Ember's MLP:FiM Archive catalog.
package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pony/internal/catalog"
	"pony/internal/source"
	"pony/internal/state"
	"pony/internal/ui"
)

var version = "0.1.0"

const usage = `pony — browse and watch My Little Pony: Friendship is Magic

Usage:
  pony                        open the browser
  pony SEASON [EPISODE]       open a collection, or play an episode directly
  pony s04e05                 same, with SxxExx syntax
  pony --list all             print the whole catalog
  pony --list 4               print one collection
  pony --search rainbow       search every episode title
  pony --print-url 4 5        print the resolved video URL
  pony --refresh              update the cached catalog and exit

Options:
  -q, --quality Q    preferred quality: 1080p, 720p, 480p, 360p
      --subs LANG    subtitles: off, en, es, fr (default: off)
      --no-autoplay  do not continue to the next episode automatically
      --autoplay     force autoplay on
      --pos SECONDS  start at this position
      --mpv PATH     mpv binary to use
      --exit-on-back quit when leaving the player
  -h, --help         show this help
  -v, --version      show the version

Keys while browsing:
  ↑/↓ j/k  move        tab ←/→  switch pane     enter  play
  /        search      ctrl+f   search all       0-9    jump
  r        quality     v        subtitles        a      resume last
  s        autoplay    R        refresh catalog  ?      help    q  quit

Keys while playing:
  space pause   ←/→ seek 5s   H/L seek 60s   n/p episode
  r quality     v subtitles   a autoplay     0 restart   b back

State: $XDG_STATE_HOME/pony/state.json
Cache: $XDG_CACHE_HOME/pony/db.json
`

type options struct {
	quality    string
	subs       string
	mpv        string
	list       string
	search     string
	printURL   bool
	refresh    bool
	noAutoplay bool
	autoplay   bool
	pos        float64
	exitOnBack bool
	version    bool
	help       bool
	args       []string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "pony:", err)
		os.Exit(1)
	}
}

func run(argv []string) error {
	o, err := parseArgs(argv)
	if err != nil {
		return err
	}
	if o.help {
		fmt.Print(usage)
		return nil
	}
	if o.version {
		fmt.Println("pony", version)
		return nil
	}

	store, err := catalog.DefaultStore()
	if err != nil {
		return err
	}
	cat, err := store.Load()
	if err != nil {
		return err
	}
	st, err := state.DefaultStore()
	if err != nil {
		return err
	}

	if o.refresh {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		fresh, updated, err := store.Refresh(ctx)
		if err != nil {
			return err
		}
		if updated {
			fmt.Printf("catalog updated (%d collections, %d episodes)\n", len(fresh.Categories()), len(fresh.Refs()))
		} else {
			fmt.Println("catalog is already up to date")
		}
		return nil
	}

	if o.list != "" {
		return listCatalog(cat, o.list)
	}
	if o.search != "" {
		return searchCatalog(cat, o.search)
	}

	quality, subs, autoplayDefault, volume := st.Preferences()
	if o.quality == "" {
		o.quality = quality
	}
	if o.subs == "" {
		o.subs = subs
	}
	autoplay := autoplayDefault
	if o.noAutoplay {
		autoplay = false
	}
	if o.autoplay {
		autoplay = true
	}

	// Work out what the positional arguments mean.
	var direct *catalog.Ref
	season := 0
	args := o.args
	if len(args) > 0 && args[0] == "play" {
		args = args[1:]
	}
	switch len(args) {
	case 0:
	case 1:
		if s, e, ok := parseRefSpec(args[0]); ok {
			ref, found := cat.Ref(s, e)
			if !found {
				return fmt.Errorf("episode %s is not in the catalog", args[0])
			}
			direct = &ref
		} else {
			num, ok := findCollection(cat, args[0])
			if !ok {
				return fmt.Errorf("unknown collection %q", args[0])
			}
			season = num
		}
	case 2:
		s, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid collection %q", args[0])
		}
		e, err := strconv.Atoi(args[1])
		if err != nil || e < 1 {
			return fmt.Errorf("invalid episode %q", args[1])
		}
		ref, found := cat.Ref(s, e)
		if !found {
			return fmt.Errorf("episode %d of collection %d is not in the catalog", e, s)
		}
		direct = &ref
	default:
		return fmt.Errorf("too many arguments (try --help)")
	}

	if o.printURL {
		ref := direct
		if ref == nil {
			if len(args) >= 1 {
				if s, e, ok := parseRefSpec(args[0]); ok {
					r, found := cat.Ref(s, e)
					if !found {
						return fmt.Errorf("episode %s is not in the catalog", args[0])
					}
					ref = &r
				}
			}
		}
		if ref == nil {
			return fmt.Errorf("--print-url needs a season and episode")
		}
		res, err := resolveURL(cat, *ref, o.quality)
		if err != nil {
			return err
		}
		fmt.Println(res.URL)
		return nil
	}

	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		return fmt.Errorf("no interactive terminal; try --list, --search or --print-url")
	}

	mpv := o.mpv
	if mpv == "" {
		mpv = os.Getenv("PONY_MPV")
	}
	model := ui.New(ui.Deps{
		Catalog:      cat,
		CatalogStore: store,
		State:        st,
		Resolver:     source.New(),
		MpvPath:      mpv,
	}, ui.Options{
		Direct:       direct,
		Season:       season,
		Quality:      o.quality,
		Subs:         o.subs,
		Autoplay:     &autoplay,
		Volume:       &volume,
		ExitOnBack:   o.exitOnBack,
		PlayPosition: o.pos,
	})
	defer model.Close()

	program := tea.NewProgram(model, tea.WithAltScreen())
	_, err = program.Run()
	return err
}

func parseArgs(argv []string) (options, error) {
	var o options
	need := func(i int, name string) (string, error) {
		if i+1 >= len(argv) {
			return "", fmt.Errorf("%s needs a value", name)
		}
		return argv[i+1], nil
	}

	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--":
			o.args = append(o.args, argv[i+1:]...)
			return o, nil
		case a == "-h" || a == "--help":
			o.help = true
		case a == "-v" || a == "--version":
			o.version = true
		case a == "-q" || a == "--quality":
			v, err := need(i, a)
			if err != nil {
				return o, err
			}
			o.quality = v
			i++
		case strings.HasPrefix(a, "--quality="):
			o.quality = strings.TrimPrefix(a, "--quality=")
		case a == "--subs":
			v, err := need(i, a)
			if err != nil {
				return o, err
			}
			o.subs = v
			i++
		case strings.HasPrefix(a, "--subs="):
			o.subs = strings.TrimPrefix(a, "--subs=")
		case a == "--mpv":
			v, err := need(i, a)
			if err != nil {
				return o, err
			}
			o.mpv = v
			i++
		case strings.HasPrefix(a, "--mpv="):
			o.mpv = strings.TrimPrefix(a, "--mpv=")
		case a == "--list":
			v, err := need(i, a)
			if err != nil {
				return o, err
			}
			o.list = v
			i++
		case strings.HasPrefix(a, "--list="):
			o.list = strings.TrimPrefix(a, "--list=")
		case a == "--search":
			v, err := need(i, a)
			if err != nil {
				return o, err
			}
			o.search = v
			i++
		case strings.HasPrefix(a, "--search="):
			o.search = strings.TrimPrefix(a, "--search=")
		case a == "--print-url":
			o.printURL = true
		case a == "--refresh":
			o.refresh = true
		case a == "--no-autoplay":
			o.noAutoplay = true
		case a == "--autoplay":
			o.autoplay = true
		case a == "--exit-on-back":
			o.exitOnBack = true
		case a == "--pos":
			v, err := need(i, a)
			if err != nil {
				return o, err
			}
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 {
				return o, fmt.Errorf("invalid --pos value %q", v)
			}
			o.pos = f
			i++
		case strings.HasPrefix(a, "--pos="):
			f, err := strconv.ParseFloat(strings.TrimPrefix(a, "--pos="), 64)
			if err != nil || f < 0 {
				return o, fmt.Errorf("invalid --pos value %q", strings.TrimPrefix(a, "--pos="))
			}
			o.pos = f
		case strings.HasPrefix(a, "-") && a != "-":
			return o, fmt.Errorf("unknown option %q (try --help)", a)
		default:
			o.args = append(o.args, a)
		}
	}
	return o, nil
}

var refSpec = regexp.MustCompile(`(?i)^s?(\d{1,2})[ex](\d{1,3})$`)

func parseRefSpec(s string) (season, episode int, ok bool) {
	m := refSpec.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, 0, false
	}
	season, _ = strconv.Atoi(m[1])
	episode, _ = strconv.Atoi(m[2])
	return season, episode, true
}

func findCollection(cat *catalog.Catalog, spec string) (int, bool) {
	if n, err := strconv.Atoi(spec); err == nil {
		if _, ok := cat.Season(n); ok {
			return n, true
		}
		return 0, false
	}
	norm := func(s string) string {
		s = strings.ToLower(s)
		return strings.Map(func(r rune) rune {
			if r == ' ' || r == '-' || r == '_' {
				return -1
			}
			return r
		}, s)
	}
	want := norm(spec)
	for _, c := range cat.Categories() {
		if norm(c.Label) == want {
			return c.Num, true
		}
	}
	return 0, false
}

func listCatalog(cat *catalog.Catalog, spec string) error {
	if strings.EqualFold(spec, "all") || spec == "*" {
		for _, ref := range cat.Refs() {
			fmt.Printf("%-14s %s\n", catalog.RefLabel(ref), ref.Title)
		}
		return nil
	}
	num, ok := findCollection(cat, spec)
	if !ok {
		return fmt.Errorf("unknown collection %q", spec)
	}
	season, _ := cat.Season(num)
	for _, e := range season.Episodes {
		ref := catalog.Ref{Season: num, Number: e.Num, Title: e.Title}
		fmt.Printf("%-14s %s\n", catalog.RefLabel(ref), e.Title)
	}
	return nil
}

func searchCatalog(cat *catalog.Catalog, query string) error {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return fmt.Errorf("empty search query")
	}
	found := 0
	for _, ref := range cat.Refs() {
		if strings.Contains(strings.ToLower(ref.Title), q) {
			fmt.Printf("%-14s %s\n", catalog.RefLabel(ref), ref.Title)
			found++
		}
	}
	if found == 0 {
		return fmt.Errorf("no episodes match %q", query)
	}
	return nil
}

func resolveURL(cat *catalog.Catalog, ref catalog.Ref, quality string) (source.Result, error) {
	if quality == "" {
		quality = "1080p"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	return source.New().Resolve(ctx, cat.DB.Series.VideoBase(), ref.Season, ref.Number, source.Qualities(quality))
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
