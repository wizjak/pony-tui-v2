# pony-tui-v2

remade from scratch to golang and shi

A terminal browser and player for *My Little Pony: Friendship is Magic*, built as
a single Go binary. It uses the same catalog and media layout as
[pony-tui](../README.md) but fixes the rough edges: no `jq`, no `curl`, no
hardcoded seasons, real playback position tracking, subtitles and proper
resume.

> [!NOTE]
> Unofficial fan project. It hosts no media: the catalog and videos come from
> [Ember's MLP:FiM Archive](https://fim.heartshine.gay/). My Little Pony is a
> trademark of Hasbro.

## Features

- **Works offline out of the box.** The catalog is embedded in the binary; a
  cached copy is refreshed automatically (ETag/Last-Modified, 24-hour TTL).
- **Catalog-driven.** Seasons, movies, specials, shorts and claymation are read
  from `db.json`; nothing is hardcoded.
- **Fast quality resolution.** Probes candidates with `HEAD` (and a one-byte
  `Range` request when needed), in parallel, and remembers answers. Falls back
  down the quality ladder instead of jumping back to 1080p.
- **Accurate playback state.** mpv is controlled over its JSON IPC socket:
  real position/duration, pause, seek and volume. Resuming starts at the exact
  second, not just the same episode.
- **Subtitles.** English, Spanish and French tracks are offered when the
  episode has them; `v` cycles off/en/es/fr and restarts at the same position.
- **Comfortable navigation.** Local and global search, numeric jumps, page
  scrolling, cross-collection next/previous and autoplay.
- **No runtime dependencies** besides `mpv` (the binary embeds HTTP, JSON and
  TUI code).

## Requirements

| Dependency | Needed for |
| --- | --- |
| Go 1.24+ | building |
| `mpv` | playing episodes |

Linux and macOS are supported. On Linux, mpv is tied to pony's lifetime
(`Pdeathsig`), so it cannot be left orphaned.

## Build and install

```bash
make                 # builds ./pony
make test            # unit + mpv IPC tests
sudo make install    # installs to /usr/local/bin (override PREFIX=...)
```

Without Make:

```bash
go build -trimpath -o pony .
install -Dm755 pony ~/.local/bin/pony
```

## Usage

```bash
pony                       # open the browser
pony 4 5                   # open collection 4 and play episode 5
pony s04e05                # same thing
pony --list all            # print the whole catalog
pony --list movies         # print one collection
pony --search rainbow      # search every episode
pony --print-url 4 5       # resolved video URL, for scripts
pony --refresh             # update the cached catalog and exit
```

Useful flags:

```
-q, --quality Q    preferred quality: 1080p, 720p, 480p, 360p
    --subs LANG    subtitles: off, en, es, fr (default: off)
    --no-autoplay  stop after each episode
    --pos SECONDS  start at this position
    --mpv PATH     use a specific mpv binary
    --exit-on-back quit when leaving the player
```

### Keys

Browsing:

| Key | Action |
| --- | --- |
| `↑`/`↓`, `j`/`k` | move |
| `tab`, `←`/`→`, `h`/`l` | switch pane |
| `enter` | open collection / play episode |
| `/` | search this collection |
| `ctrl+f` | search the whole catalog |
| `0-9` | jump to collection or episode number |
| `pgup`/`pgdn`, `g`/`G` | page / first / last |
| `r` | cycle quality |
| `v` | cycle subtitles |
| `s` | toggle autoplay |
| `a` | resume the last watched episode |
| `R` | refresh the catalog |
| `?` | help |
| `q` | quit |

Playing:

| Key | Action |
| --- | --- |
| `space` | pause / resume |
| `←`/`→`, `h`/`l` | seek 5 seconds |
| `H`/`L` | seek 60 seconds |
| `n`/`]`, `p`/`[` | next / previous episode |
| `r` | change quality (restarts at the same position) |
| `v` | change subtitles |
| `a` | toggle autoplay |
| `0` | restart episode |
| `b`, `esc` | back to browsing (position is saved) |
| `q` | quit |

## How it works

1. **Catalog.** `db.json` is embedded at build time. On startup the cached copy
   (if any) is used and, when it is older than 24 hours, refreshed in the
   background with a conditional request. Collections are ordered by `seasNum`;
   episodes by `epNum`. Only the title list comes from the catalog.
2. **Media URLs.** The archive follows a predictable scheme:
   `<vidPath>/s01e01-1080p.mp4`, where `vidPath` comes from the catalog. For each
   candidate quality pony issues a `HEAD`; if a server rejects `HEAD` it retries
   with `Range: bytes=0-0` and drops the body immediately. The first available
   quality in preference order wins. Positive and negative answers are cached
   (negatives for 10 minutes).
3. **Playback.** mpv runs with `--no-terminal` and
   `--input-ipc-server=<socket>`. pony subscribes to `time-pos`, `duration`,
   `pause` and `volume`, and sends `seek`, `set_property` and `quit` commands.
   This is what makes the progress bar honest and resume work.
4. **State.** Watch history and preferences live in
   `$XDG_STATE_HOME/pony/state.json` (usually `~/.local/state/pony/state.json`).
   Positions are saved every few seconds and when leaving the player.

## Files

| Path | Contents |
| --- | --- |
| `$XDG_CACHE_HOME/pony/db.json` | refreshed catalog |
| `$XDG_CACHE_HOME/pony/db.meta.json` | ETag / Last-Modified / fetch time |
| `$XDG_STATE_HOME/pony/state.json` | last episode, positions, preferences |

Set `NO_COLOR=1` to disable color. The UI adapts to narrow terminals by showing
one pane at a time.

## Development

```bash
go test ./...
go test -race ./...
go vet ./...
PONY_PREVIEW=1 go test ./internal/ui -run TestPreview -v
```

Layout:

- `main.go` — CLI parsing and entry point
- `internal/catalog` — catalog types, embedded copy, cache and refresh
- `internal/source` — URL construction, quality probing and caching
- `internal/player` — mpv process + JSON IPC client
- `internal/state` — XDG state persistence
- `internal/ui` — Bubble Tea interface

## License

MIT, same as the original pony-tui.
