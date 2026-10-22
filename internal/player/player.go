// Package player runs mpv and talks to it over its JSON IPC socket.
//
// mpv is started without a terminal so the TUI keeps the keyboard. Playback
// state (position, duration, pause) comes back through the IPC socket, which
// gives accurate times and makes resuming work across quality changes.
package player

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Options configures one playback session.
type Options struct {
	MpvPath   string
	URL       string
	Title     string
	StartAt   float64
	SubFile   string
	Volume    int
	Paused    bool
	ExtraArgs []string
}

// Status is a snapshot of mpv's playback state.
type Status struct {
	TimePos   float64
	Duration  float64
	Paused    bool
	Volume    int
	Ended     bool
	EndReason string
}

// Player wraps a running mpv process.
type Player struct {
	cmd    *exec.Cmd
	dir    string
	sock   string
	log    string
	volume int

	conn net.Conn
	enc  *json.Encoder

	writeMu sync.Mutex
	reqID   atomic.Int64

	mu     sync.Mutex
	status Status
	err    error
	stop   bool

	done     chan struct{}
	closeOne sync.Once
}

type ipcMessage struct {
	Event     string          `json:"event"`
	Name      string          `json:"name"`
	Reason    string          `json:"reason"`
	Data      json.RawMessage `json:"data"`
	RequestID int64           `json:"request_id"`
	Error     string          `json:"error"`
}

// Start launches mpv and connects to its IPC socket.
func Start(ctx context.Context, o Options) (*Player, error) {
	mpv := o.MpvPath
	if mpv == "" {
		mpv = "mpv"
	}
	if !strings.Contains(mpv, "/") {
		found, err := exec.LookPath(mpv)
		if err != nil {
			return nil, errors.New("mpv is not installed (it is required to play episodes)")
		}
		mpv = found
	}
	if o.URL == "" {
		return nil, errors.New("empty media URL")
	}

	dir, err := os.MkdirTemp("", "pony-")
	if err != nil {
		return nil, fmt.Errorf("cannot create temporary directory: %w", err)
	}
	p := &Player{
		dir:    dir,
		sock:   filepath.Join(dir, "mpv.sock"),
		log:    filepath.Join(dir, "mpv.log"),
		volume: o.Volume,
		done:   make(chan struct{}),
	}
	if p.volume <= 0 {
		p.volume = 100
	}

	logFile, err := os.Create(p.log)
	if err != nil {
		p.cleanup()
		return nil, err
	}

	args := []string{
		"--no-terminal",
		"--input-ipc-server=" + p.sock,
		"--force-media-title=" + o.Title,
		"--idle=no",
		"--keep-open=no",
		"--no-save-position-on-quit",
		"--volume=" + fmt.Sprint(p.volume),
	}
	if o.StartAt > 0.5 {
		args = append(args, fmt.Sprintf("--start=%.3f", o.StartAt))
	}
	if o.SubFile != "" {
		args = append(args, "--sub-auto=no", "--sub-file="+o.SubFile)
	}
	if o.Paused {
		args = append(args, "--pause=yes")
	}
	args = append(args, o.ExtraArgs...)
	args = append(args, o.URL)

	p.cmd = exec.Command(mpv, args...)
	p.cmd.Stdout = io.Discard
	p.cmd.Stderr = logFile
	setProcAttr(p.cmd)

	if err := p.cmd.Start(); err != nil {
		logFile.Close()
		p.cleanup()
		return nil, fmt.Errorf("cannot start mpv: %w", err)
	}
	logFile.Close()

	go func() {
		err := p.cmd.Wait()
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		if p.conn != nil {
			p.conn.Close()
		}
		p.closeOne.Do(func() { close(p.done) })
	}()

	if err := p.connect(ctx); err != nil {
		p.Kill()
		return nil, err
	}

	p.request("observe_property", 1, "time-pos")
	p.request("observe_property", 2, "duration")
	p.request("observe_property", 3, "pause")
	p.request("observe_property", 4, "volume")

	return p, nil
}

func (p *Player) connect(ctx context.Context) error {
	deadline := time.Now().Add(6 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.done:
			return fmt.Errorf("mpv exited before it was ready%s", p.tailSuffix())
		default:
		}

		conn, err := net.DialTimeout("unix", p.sock, 500*time.Millisecond)
		if err == nil {
			p.conn = conn
			p.enc = json.NewEncoder(conn)
			go p.readLoop(conn)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for mpv%s", p.tailSuffix())
		}
		time.Sleep(40 * time.Millisecond)
	}
}

func (p *Player) readLoop(conn net.Conn) {
	dec := json.NewDecoder(bufio.NewReader(conn))
	for {
		var m ipcMessage
		if err := dec.Decode(&m); err != nil {
			return
		}
		if m.Event != "" {
			p.handleEvent(m)
		}
	}
}

func (p *Player) handleEvent(m ipcMessage) {
	switch m.Event {
	case "property-change":
		p.mu.Lock()
		switch m.Name {
		case "time-pos":
			var v *float64
			if json.Unmarshal(m.Data, &v) == nil && v != nil {
				p.status.TimePos = *v
			}
		case "duration":
			var v *float64
			if json.Unmarshal(m.Data, &v) == nil && v != nil {
				p.status.Duration = *v
			}
		case "pause":
			var v bool
			if json.Unmarshal(m.Data, &v) == nil {
				p.status.Paused = v
			}
		case "volume":
			var v float64
			if json.Unmarshal(m.Data, &v) == nil {
				p.status.Volume = int(v)
			}
		}
		p.mu.Unlock()
	case "end-file":
		p.mu.Lock()
		p.status.Ended = true
		p.status.EndReason = m.Reason
		p.mu.Unlock()
	}
}

func (p *Player) request(args ...any) {
	if p.enc == nil {
		return
	}
	req := map[string]any{
		"command":    args,
		"request_id": p.reqID.Add(1),
	}
	p.writeMu.Lock()
	_ = p.enc.Encode(req)
	p.writeMu.Unlock()
}

// Command sends a raw mpv command.
func (p *Player) Command(args ...any) error {
	if p.enc == nil {
		return errors.New("mpv IPC is not connected")
	}
	return p.sendCommand(args)
}

func (p *Player) sendCommand(args []any) error {
	if p.enc == nil {
		return errors.New("mpv IPC is not connected")
	}
	req := map[string]any{
		"command":    args,
		"request_id": p.reqID.Add(1),
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return p.enc.Encode(req)
}

// Status returns the latest playback snapshot.
func (p *Player) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// Done is closed when mpv exits.
func (p *Player) Done() <-chan struct{} { return p.done }

// Err returns mpv's exit error, if any, once Done is closed.
func (p *Player) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// RequestedStop reports whether Stop was called explicitly.
func (p *Player) RequestedStop() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stop
}

// SeekAbs jumps to an absolute position.
func (p *Player) SeekAbs(seconds float64) error {
	if seconds < 0 {
		seconds = 0
	}
	return p.sendCommand([]any{"seek", seconds, "absolute", "exact"})
}

// SeekRel moves relative to the current position.
func (p *Player) SeekRel(delta float64) error {
	return p.sendCommand([]any{"seek", delta, "relative"})
}

// SetPaused pauses or resumes playback.
func (p *Player) SetPaused(paused bool) error {
	return p.sendCommand([]any{"set_property", "pause", paused})
}

// TogglePause flips the pause state.
func (p *Player) TogglePause() error {
	return p.SetPaused(!p.Status().Paused)
}

// AddVolume changes the volume by delta, clamped by mpv.
func (p *Player) AddVolume(delta int) error {
	return p.sendCommand([]any{"add", "volume", delta})
}

// SetVolume sets an absolute volume.
func (p *Player) SetVolume(vol int) error {
	return p.sendCommand([]any{"set_property", "volume", vol})
}

// Quit asks mpv to exit.
func (p *Player) Quit() error {
	return p.sendCommand([]any{"quit"})
}

// Stop quits mpv, forcing it down if it does not exit promptly, then removes
// the temporary directory. It is safe to call more than once.
func (p *Player) Stop() {
	p.mu.Lock()
	p.stop = true
	p.mu.Unlock()

	select {
	case <-p.done:
		p.cleanup()
		return
	default:
	}

	_ = p.Quit()
	select {
	case <-p.done:
	case <-time.After(1500 * time.Millisecond):
		p.Kill()
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
		}
	}
	p.cleanup()
}

// Kill terminates the mpv process immediately.
func (p *Player) Kill() {
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// StderrTail returns the last lines mpv wrote to stderr (for error messages).
func (p *Player) StderrTail() string {
	data, err := os.ReadFile(p.log)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (p *Player) tailSuffix() string {
	if tail := p.StderrTail(); tail != "" {
		return ": " + tail
	}
	return ""
}

func (p *Player) cleanup() {
	if p.conn != nil {
		p.conn.Close()
		p.conn = nil
	}
	if p.dir != "" {
		_ = os.RemoveAll(p.dir)
	}
}
