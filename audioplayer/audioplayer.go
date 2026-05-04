package audioplayer

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Player interface {
	Open(ctx context.Context, path string) error

	Play() error
	Pause() error
	Stop() error

	Seek(pos time.Duration) error
	Position() (time.Duration, error)
	Duration() (time.Duration, error)

	SetVolume(volume float64) error // 0.0 - 1.0
	Volume() (float64, error)

	SetMuted(muted bool) error
	Muted() (bool, error)
	Wait() error

	State() State
	Close() error
}

type Backend string

const (
	BackendAuto      Backend = "auto"
	BackendBeep      Backend = "beep"
	BackendGStreamer Backend = "gstreamer"
)

type Config struct {
	Backend Backend
}

func NewPlayer(cfg Config) (Player, error) {
	switch cfg.Backend {
	case "", BackendAuto:
		return newAutoPlayer()
	case BackendBeep:
		return NewBeepPlayer()
	case BackendGStreamer:
		return NewGStreamerPlayer()
	default:
		return nil, ErrUnsupportedBackend
	}
}

func newAutoPlayer() (Player, error) {
	// Default/preferred backend.
	if p, err := NewBeepPlayer(); err == nil {
		return p, nil
	}

	// Fallback for systems/files where you want a more capable Linux stack.
	if p, err := NewGStreamerPlayer(); err == nil {
		return p, nil
	}

	return nil, ErrUnsupportedBackend
}

func PlayAudioFile(player Player, path string, wait bool) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("no audio file provided")
	}

	if err := player.Open(context.Background(), path); err != nil {
		_ = player.Close()
		return err
	}

	if err := player.Play(); err != nil {
		_ = player.Close()
		return err
	}

	if wait {
		defer player.Close()
		return player.Wait()
	}

	go func() {
		_ = player.Wait()
		_ = player.Close()
	}()

	return nil
}
