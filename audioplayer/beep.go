package audioplayer

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/faiface/beep"
	"github.com/faiface/beep/effects"
	"github.com/faiface/beep/speaker"
)

var _ Player = (*BeepPlayer)(nil)

type BeepPlayer struct {
	mu sync.Mutex

	streamer beep.StreamSeekCloser
	format   beep.Format

	ctrl   *beep.Ctrl
	volume *effects.Volume
	done   chan struct{}

	queued bool

	state  State
	path   string
	closed bool

	muted      bool
	lastVolume float64
}

var (
	beepSpeakerMu         sync.Mutex
	beepSpeakerReady      bool
	beepSpeakerSampleRate beep.SampleRate
)

const (
	beepOutputSampleRate = beep.SampleRate(44100)

	ffmpegPCMPrecision = 2 // bytes per sample, int16
)

func NewBeepPlayer() (*BeepPlayer, error) {
	return &BeepPlayer{
		state:      StateIdle,
		lastVolume: 1.0,
	}, nil
}

func (p *BeepPlayer) Open(ctx context.Context, path string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}

	if err := p.closeOpenFileLocked(); err != nil {
		return err
	}

	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("audio path is empty")
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	if err := ensureBeepSpeaker(); err != nil {
		return err
	}

	streamer, format, err := decodeWithFFmpegPCM(ctx, abs)
	if err != nil {
		return err
	}

	p.streamer = streamer
	p.format = format
	p.path = abs
	p.state = StateReady
	p.done = make(chan struct{})
	p.queued = false

	p.rebuildChainLocked(true)

	fmt.Printf(
		"[audio] path=%s decoder=ffmpeg sample_rate=%d speaker_rate=%d channels=%d precision=%d len=%d duration=%s\n",
		abs,
		format.SampleRate,
		beepSpeakerSampleRate,
		format.NumChannels,
		format.Precision,
		streamer.Len(),
		format.SampleRate.D(streamer.Len()),
	)

	return nil
}

func (p *BeepPlayer) Play() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}
	if p.streamer == nil || p.ctrl == nil || p.volume == nil {
		return ErrNoFileOpen
	}

	// If Play is called after natural completion, rewind and queue again.
	if p.streamer.Len() > 0 && p.streamer.Position() >= p.streamer.Len() {
		if err := p.streamer.Seek(0); err != nil {
			return fmt.Errorf("beep seek: %w", err)
		}

		p.done = make(chan struct{})
		p.queued = false
		p.rebuildChainLocked(true)
	}

	done := p.done

	speaker.Lock()
	p.ctrl.Paused = false
	speaker.Unlock()

	if !p.queued {
		p.queued = true

		speaker.Play(beep.Seq(
			p.volume,
			beep.Callback(func() {
				p.finish(done)
			}),
		))
	}

	p.state = StatePlaying
	return nil
}

func (p *BeepPlayer) Pause() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}
	if p.streamer == nil || p.ctrl == nil {
		return ErrNoFileOpen
	}

	speaker.Lock()
	p.ctrl.Paused = true
	speaker.Unlock()

	p.state = StatePaused
	return nil
}

func (p *BeepPlayer) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}
	if p.streamer == nil {
		return nil
	}

	speaker.Lock()
	if p.ctrl != nil {
		p.ctrl.Paused = true
		p.ctrl.Streamer = nil
	}

	err := p.streamer.Seek(0)

	p.done = make(chan struct{})
	p.queued = false
	p.rebuildChainLocked(true)

	speaker.Unlock()

	if err != nil {
		return fmt.Errorf("beep seek: %w", err)
	}

	p.state = StateStopped
	return nil
}

func (p *BeepPlayer) Seek(pos time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}
	if p.streamer == nil {
		return ErrNoFileOpen
	}

	if pos < 0 {
		pos = 0
	}

	samples := p.format.SampleRate.N(pos)
	if p.streamer.Len() > 0 && samples > p.streamer.Len() {
		samples = p.streamer.Len()
	}

	wasPaused := true
	if p.ctrl != nil {
		wasPaused = p.ctrl.Paused
	}

	speaker.Lock()
	if p.ctrl != nil {
		p.ctrl.Streamer = nil
	}

	err := p.streamer.Seek(samples)

	p.rebuildChainLocked(wasPaused)
	speaker.Unlock()

	if err != nil {
		return fmt.Errorf("beep seek: %w", err)
	}

	// If the previous stream had completed, allow Play to queue again.
	if p.streamer.Len() == 0 || p.streamer.Position() < p.streamer.Len() {
		select {
		case <-p.done:
			p.done = make(chan struct{})
			p.queued = false
		default:
		}
	}

	return nil
}

func (p *BeepPlayer) Position() (time.Duration, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return 0, ErrClosed
	}
	if p.streamer == nil {
		return 0, ErrNoFileOpen
	}

	speaker.Lock()
	pos := p.streamer.Position()
	speaker.Unlock()

	return p.format.SampleRate.D(pos), nil
}

func (p *BeepPlayer) Duration() (time.Duration, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return 0, ErrClosed
	}
	if p.streamer == nil {
		return 0, ErrNoFileOpen
	}

	if p.streamer.Len() <= 0 {
		return 0, nil
	}

	return p.format.SampleRate.D(p.streamer.Len()), nil
}

func (p *BeepPlayer) SetVolume(volume float64) error {
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

	p.lastVolume = volume

	if p.volume != nil {
		speaker.Lock()
		p.volume.Volume = volumeToBeep(volume)
		p.volume.Silent = p.muted || volume == 0
		speaker.Unlock()
	}

	return nil
}

func (p *BeepPlayer) Volume() (float64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return 0, ErrClosed
	}

	return p.lastVolume, nil
}

func (p *BeepPlayer) SetMuted(muted bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}

	p.muted = muted

	if p.volume != nil {
		speaker.Lock()
		p.volume.Silent = muted || p.lastVolume == 0
		speaker.Unlock()
	}

	return nil
}

func (p *BeepPlayer) Muted() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return false, ErrClosed
	}

	return p.muted, nil
}

func (p *BeepPlayer) Wait() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrClosed
	}
	if p.streamer == nil {
		p.mu.Unlock()
		return ErrNoFileOpen
	}

	done := p.done
	p.mu.Unlock()

	if done == nil {
		return ErrNoFileOpen
	}

	<-done
	return nil
}

func (p *BeepPlayer) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.state
}

func (p *BeepPlayer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}

	_ = p.closeOpenFileLocked()

	p.closed = true
	p.state = StateClosed

	return nil
}

func (p *BeepPlayer) closeOpenFileLocked() error {
	if p.ctrl != nil {
		speaker.Lock()
		p.ctrl.Paused = true
		p.ctrl.Streamer = nil
		speaker.Unlock()
		p.ctrl = nil
	}

	p.volume = nil
	p.queued = false

	if p.done != nil {
		select {
		case <-p.done:
		default:
			close(p.done)
		}
		p.done = nil
	}

	var firstErr error

	if p.streamer != nil {
		if err := p.streamer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		p.streamer = nil
	}

	p.path = ""
	p.format = beep.Format{}

	return firstErr
}

func (p *BeepPlayer) finish(done chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.state != StateClosed {
		p.state = StateStopped
	}

	p.queued = false

	if done != nil {
		select {
		case <-done:
		default:
			close(done)
		}
	}
}

func (p *BeepPlayer) rebuildChainLocked(paused bool) {
	p.ctrl = &beep.Ctrl{
		Streamer: p.playbackStreamerLocked(),
		Paused:   paused,
	}

	p.volume = &effects.Volume{
		Streamer: p.ctrl,
		Base:     2,
		Volume:   volumeToBeep(p.lastVolume),
		Silent:   p.muted || p.lastVolume == 0,
	}
}

func (p *BeepPlayer) playbackStreamerLocked() beep.Streamer {
	if p.streamer == nil {
		return beep.Silence(-1)
	}

	if p.format.SampleRate == beepSpeakerSampleRate {
		return p.streamer
	}

	return beep.Resample(4, p.format.SampleRate, beepSpeakerSampleRate, p.streamer)
}

func ensureBeepSpeaker() error {
	beepSpeakerMu.Lock()
	defer beepSpeakerMu.Unlock()

	if beepSpeakerReady {
		return nil
	}

	if err := speaker.Init(beepOutputSampleRate, beepOutputSampleRate.N(time.Second/10)); err != nil {
		return fmt.Errorf("init speaker: %w", err)
	}

	beepSpeakerReady = true
	beepSpeakerSampleRate = beepOutputSampleRate

	return nil
}

// decodeWithFFmpegPCM decodes any ffmpeg-supported input into normalized PCM:
//
//   - sample rate: 44100 Hz
//   - channels: stereo
//   - format: signed 16-bit little-endian PCM
//
// Then it converts the PCM into Beep's [][2]float64 sample format.
type pcmMemoryStreamer struct {
	samples [][2]float64
	pos     int
	err     error
}

func (s *pcmMemoryStreamer) Stream(out [][2]float64) (n int, ok bool) {
	if s.pos >= len(s.samples) {
		return 0, false
	}

	n = copy(out, s.samples[s.pos:])
	s.pos += n

	return n, s.pos < len(s.samples)
}

func (s *pcmMemoryStreamer) Err() error {
	return s.err
}

func (s *pcmMemoryStreamer) Len() int {
	return len(s.samples)
}

func (s *pcmMemoryStreamer) Position() int {
	return s.pos
}

func (s *pcmMemoryStreamer) Seek(pos int) error {
	if pos < 0 {
		pos = 0
	}
	if pos > len(s.samples) {
		pos = len(s.samples)
	}

	s.pos = pos
	return nil
}

func (s *pcmMemoryStreamer) Close() error {
	s.samples = nil
	s.pos = 0
	s.err = nil
	return nil
}

// effects.Volume uses logarithmic-ish volume.
// 0.0 should be silent, 1.0 should be neutral.
func volumeToBeep(v float64) float64 {
	if v <= 0 {
		return -8
	}
	if v >= 1 {
		return 0
	}

	// Simple mapping:
	// 1.0 => 0
	// 0.5 => -1
	// 0.25 => -2
	// etc.
	x := 0.0
	for v < 1 {
		v *= 2
		x--
		if x <= -8 {
			return -8
		}
	}

	return x
}
