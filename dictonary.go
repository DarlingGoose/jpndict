package jpndict

import (
	"context"
	"errors"
	"time"

	"github.com/DarlingGoose/jpndict/audioplayer"
)

// todo add a cache dir to load data from
type Dictonary interface {
	Download() error
	Search(data Search) (*Response, error)
	SearchAll(data Search) ([]*Response, error)
}

type Search struct {
	Text      string
	WithAudio bool
}

type Response struct {
	Query string `json:"query"`
	Key   string `json:"key"`

	HTML string `json:"html,omitempty"`
	Text string `json:"text,omitempty"`
	Raw  []byte `json:"raw,omitempty"`

	Entry *Entry `json:"entry,omitempty"`
}

var (
	ErrNoAudioFileProvided = errors.New("no audio file provided")
)

func (r *Response) HasAudio() bool {
	if r.Entry == nil {
		return false
	}
	if r.Entry.Pronunciation == nil {
		return false
	}
	if r.Entry.Pronunciation.Audio == "" {
		return false
	}
	return true
}

func (r *Response) PlayAudio(wait bool) (*audioplayer.Player, error) {
	if r.Entry == nil {
		return nil, ErrNoAudioFileProvided
	}
	if r.Entry.Pronunciation == nil {
		return nil, ErrNoAudioFileProvided
	}
	if r.Entry.Pronunciation.Audio == "" {
		return nil, ErrNoAudioFileProvided
	}
	p, err := audioplayer.NewPlayer(audioplayer.Config{Backend: audioplayer.BackendAuto})
	if err != nil {
		return nil, err
	}
	err = p.Open(context.Background(), r.Entry.Pronunciation.Audio)
	if err != nil {
		_ = p.Close()
		return nil, err
	}
	duration, err := p.Duration()
	if err != nil {
		_ = p.Close()
		return nil, err
	}
	err = p.Play()
	if err != nil {
		_ = p.Close()
		return nil, err
	}
	if wait {
		time.Sleep(duration)
		_ = p.Close()
	} else {
		go func() {
			time.Sleep(duration)
			_ = p.Close()
		}()
	}
	return nil, nil
}

type Entry struct {
	Headword      string          `json:"headword,omitempty"`
	Reading       string          `json:"reading,omitempty"`
	IsPriority    bool            `json:"isPriority,omitempty"`
	Pronunciation *Pronunciation  `json:"pronunciation,omitempty"`
	Senses        []Sense         `json:"senses,omitempty"`
	Links         []ReferenceLink `json:"links,omitempty"`
}

type Pronunciation struct {
	Text      string `json:"text,omitempty"`
	Audio     string `json:"audio,omitempty"`
	Pitch     string `json:"pitch,omitempty"`
	LocalPath string `json:"localPath,omitempty"`
}

type Sense struct {
	Number        string    `json:"number,omitempty"`
	PartsOfSpeech []string  `json:"partsOfSpeech,omitempty"`
	Glosses       []string  `json:"glosses,omitempty"`
	Notes         []string  `json:"notes,omitempty"`
	Examples      []Example `json:"examples,omitempty"`
	XRefs         []XRef    `json:"xrefs,omitempty"`
}

type Example struct {
	Japanese string `json:"japanese,omitempty"`
	English  string `json:"english,omitempty"`
}

type XRef struct {
	Word     string `json:"word,omitempty"`
	Reading  string `json:"reading,omitempty"`
	Glossary string `json:"glossary,omitempty"`
	Href     string `json:"href,omitempty"`
	TargetID string `json:"targetId,omitempty"`
}

type ReferenceLink struct {
	Label string `json:"label,omitempty"`
	Href  string `json:"href,omitempty"`
}
