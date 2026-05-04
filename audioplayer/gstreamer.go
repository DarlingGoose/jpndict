package audioplayer

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-gst/go-gst/gst"
)

type GStreamerPlayer struct {
	mu       sync.Mutex
	pipeline *gst.Element
	state    State
	path     string
	closed   bool
}

func (p *GStreamerPlayer) Wait() error {
	//TODO implement me
	panic("implement me")
}

var gstInitOnce sync.Once

func NewGStreamerPlayer() (*GStreamerPlayer, error) {
	gstInitOnce.Do(func() {
		gst.Init(nil)
	})

	playbin, err := gst.NewElement("playbin")
	if err != nil {
		return nil, fmt.Errorf("create gstreamer playbin: %w", err)
	}

	return &GStreamerPlayer{
		pipeline: playbin,
		state:    StateIdle,
	}, nil
}

func (p *GStreamerPlayer) Open(ctx context.Context, path string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	if _, err := os.Stat(abs); err != nil {
		return err
	}

	uri := fileURI(abs)

	if err := p.pipeline.SetProperty("uri", uri); err != nil {
		return fmt.Errorf("set gstreamer uri: %w", err)
	}

	p.path = abs
	p.state = StateReady
	return nil
}

func (p *GStreamerPlayer) Play() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}
	if p.path == "" {
		return ErrNoFileOpen
	}

	if err := p.pipeline.SetState(gst.StatePlaying); err != nil {
		return fmt.Errorf("gstreamer play: %w", err)
	}

	p.state = StatePlaying
	return nil
}

func (p *GStreamerPlayer) Pause() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}
	if p.path == "" {
		return ErrNoFileOpen
	}

	if err := p.pipeline.SetState(gst.StatePaused); err != nil {
		return fmt.Errorf("gstreamer pause: %w", err)
	}

	p.state = StatePaused
	return nil
}

func (p *GStreamerPlayer) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}

	if err := p.pipeline.SetState(gst.StateNull); err != nil {
		return fmt.Errorf("gstreamer stop: %w", err)
	}

	p.state = StateStopped
	return nil
}

func (p *GStreamerPlayer) Seek(pos time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}
	if p.path == "" {
		return ErrNoFileOpen
	}
	ok := p.pipeline.SeekSimple(
		int64(pos),
		gst.FormatTime,
		gst.SeekFlagFlush|gst.SeekFlagKeyUnit,
	)
	if !ok {
		return fmt.Errorf("gstreamer seek failed")
	}

	return nil
}

func (p *GStreamerPlayer) Position() (time.Duration, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return 0, ErrClosed
	}
	if p.path == "" {
		return 0, ErrNoFileOpen
	}

	ok, pos := p.pipeline.QueryPosition(gst.FormatTime)
	if !ok {
		return 0, fmt.Errorf("gstreamer query position failed")
	}

	return time.Duration(pos), nil
}

func (p *GStreamerPlayer) Duration() (time.Duration, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return 0, ErrClosed
	}
	if p.path == "" {
		return 0, ErrNoFileOpen
	}

	ok, dur := p.pipeline.QueryDuration(gst.FormatTime)
	if !ok {
		return 0, fmt.Errorf("gstreamer query duration failed")
	}

	return time.Duration(dur), nil
}

func (p *GStreamerPlayer) SetVolume(volume float64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}

	if volume < 0 {
		volume = 0
	}
	if volume > 1 {
		volume = 1
	}

	if err := p.pipeline.SetProperty("volume", volume); err != nil {
		return fmt.Errorf("gstreamer set volume: %w", err)
	}

	return nil
}

func (p *GStreamerPlayer) Volume() (float64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return 0, ErrClosed
	}

	v, err := p.pipeline.GetProperty("volume")
	if err != nil {
		return 0, fmt.Errorf("gstreamer get volume: %w", err)
	}

	volume, ok := v.(float64)
	if !ok {
		return 0, fmt.Errorf("unexpected volume type %T", v)
	}

	return volume, nil
}

func (p *GStreamerPlayer) SetMuted(muted bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}

	if err := p.pipeline.SetProperty("mute", muted); err != nil {
		return fmt.Errorf("gstreamer set mute: %w", err)
	}

	return nil
}

func (p *GStreamerPlayer) Muted() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return false, ErrClosed
	}

	v, err := p.pipeline.GetProperty("mute")
	if err != nil {
		return false, fmt.Errorf("gstreamer get mute: %w", err)
	}

	muted, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("unexpected mute type %T", v)
	}

	return muted, nil
}

func (p *GStreamerPlayer) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

func (p *GStreamerPlayer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}

	_ = p.pipeline.SetState(gst.StateNull)
	p.closed = true
	p.state = StateClosed
	return nil
}

func fileURI(path string) string {
	u := url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(path),
	}
	return u.String()
}
