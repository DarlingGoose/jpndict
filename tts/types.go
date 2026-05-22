package tts

import (
	"context"
	"errors"
	"strings"

	"github.com/DarlingGoose/jpndict/audioplayer"
)

type Client interface {
	Synthesize(ctx context.Context, r *Request) (*Response, error)
	Close()
	SupportedLanguages() []Language
	IsLanguageSupported(Language) bool
	SupportedVoices() []Voice
	SupportedModels() []string
}

type Language string

const (
	LanguageEnglishUS Language = "en-us"
	LanguageEnglishGB Language = "en-gb"
	LanguageJapanese  Language = "ja"
	LanguageFrench    Language = "fr-fr"
	LanguageItalian   Language = "it"
	LanguageChinese   Language = "cmn"
)

type Voice string

const (
	KokoroVoiceAFAlloy      Voice = "af_alloy"
	KokoroVoiceAFAoede      Voice = "af_aoede"
	KokoroVoiceAFBella      Voice = "af_bella"
	KokoroVoiceAFHeart      Voice = "af_heart"
	KokoroVoiceAFJessica    Voice = "af_jessica"
	KokoroVoiceAFKore       Voice = "af_kore"
	KokoroVoiceAFNicole     Voice = "af_nicole"
	KokoroVoiceAFNova       Voice = "af_nova"
	KokoroVoiceAFRiver      Voice = "af_river"
	KokoroVoiceAFSarah      Voice = "af_sarah"
	KokoroVoiceAFSky        Voice = "af_sky"
	KokoroVoiceAMAdam       Voice = "am_adam"
	KokoroVoiceAMEcho       Voice = "am_echo"
	KokoroVoiceAMEric       Voice = "am_eric"
	KokoroVoiceAMFenrir     Voice = "am_fenrir"
	KokoroVoiceAMLiam       Voice = "am_liam"
	KokoroVoiceAMMichael    Voice = "am_michael"
	KokoroVoiceAMOnyx       Voice = "am_onyx"
	KokoroVoiceAMPuck       Voice = "am_puck"
	KokoroVoiceBFAlice      Voice = "bf_alice"
	KokoroVoiceBFEmma       Voice = "bf_emma"
	KokoroVoiceBFIsabella   Voice = "bf_isabella"
	KokoroVoiceBFLily       Voice = "bf_lily"
	KokoroVoiceBMDaniel     Voice = "bm_daniel"
	KokoroVoiceBMFable      Voice = "bm_fable"
	KokoroVoiceBMGeorge     Voice = "bm_george"
	KokoroVoiceBMLewis      Voice = "bm_lewis"
	KokoroVoiceFFSiwis      Voice = "ff_siwis"
	KokoroVoiceIFSara       Voice = "if_sara"
	KokoroVoiceIMNicola     Voice = "im_nicola"
	KokoroVoiceJFAlpha      Voice = "jf_alpha"
	KokoroVoiceJFGongitsune Voice = "jf_gongitsune"
	KokoroVoiceJFNezumi     Voice = "jf_nezumi"
	KokoroVoiceJFTebukuro   Voice = "jf_tebukuro"
	KokoroVoiceJMKumo       Voice = "jm_kumo"
	KokoroVoiceZFXiaobei    Voice = "zf_xiaobei"
	KokoroVoiceZFXiaoni     Voice = "zf_xiaoni"
	KokoroVoiceZFXiaoxiao   Voice = "zf_xiaoxiao"
	KokoroVoiceZFXiaoyi     Voice = "zf_xiaoyi"
	KokoroVoiceZMYunjian    Voice = "zm_yunjian"
	KokoroVoiceZMYunxi      Voice = "zm_yunxi"
	KokoroVoiceZMYunxia     Voice = "zm_yunxia"
	KokoroVoiceZMYunyang    Voice = "zm_yunyang"
)

const (
	KokoroLanguageEnglishUS = LanguageEnglishUS
	KokoroLanguageEnglishGB = LanguageEnglishGB
	KokoroLanguageJapanese  = LanguageJapanese
	KokoroLanguageFrench    = LanguageFrench
	KokoroLanguageItalian   = LanguageItalian
	KokoroLanguageChinese   = LanguageChinese
)

type Format string

const (
	FormatWAV Format = "wav"
	FormatMP3 Format = "mp3"
)

type KanaMode string

const (
	KanaNone     KanaMode = ""
	KanaHiragana KanaMode = "hiragana"
	KanaKatakana KanaMode = "katakana"
)

type Request struct {
	Text       string
	OutputPath string
	Language   Language
	Voice      Voice
	Format     Format
	Speed      float64
	KanaMode   KanaMode

	// Endpoint-specific options. Ollama/Orpheus-compatible servers commonly
	// support emotion tags in text and may accept extra JSON fields.
	Instructions string
	Options      map[string]any
}

type Response struct {
	Request    *Request
	Path       string
	Format     Format
	Language   Language
	Voice      Voice
	Model      string
	Source     string
	CacheHit   bool
	SampleRate int
}

var (
	ErrEmptyText             = errors.New("empty tts text")
	ErrUnsupportedLanguage   = errors.New("unsupported tts language")
	ErrUnsupportedVoice      = errors.New("unsupported tts voice")
	ErrUnsupportedFormat     = errors.New("unsupported tts format")
	ErrOutputPathRequired    = errors.New("tts output path is required")
	ErrNoAudioFileProvided   = errors.New("no tts audio file provided")
	ErrKokoroTTSNotInstalled = errors.New("kokoro-tts is not installed; install it with: yay -S kokoro-tts-git")
)

func (r *Response) HasAudio() bool {
	return r != nil && cleanText(r.Path) != ""
}

func (r *Response) PlayAudio(wait bool) (audioplayer.Player, error) {
	if !r.HasAudio() {
		return nil, ErrNoAudioFileProvided
	}
	return PlayAudio(context.Background(), r.Path, wait)
}

func PlayAudio(ctx context.Context, path string, wait bool) (audioplayer.Player, error) {
	path = cleanText(path)
	if path == "" {
		return nil, ErrNoAudioFileProvided
	}

	player, err := audioplayer.NewPlayer(audioplayer.Config{Backend: audioplayer.BackendAuto})
	if err != nil {
		return nil, err
	}
	if err := player.Open(ctx, path); err != nil {
		_ = player.Close()
		return nil, err
	}
	if err := player.Play(); err != nil {
		_ = player.Close()
		return nil, err
	}

	if wait {
		defer player.Close()
		return player, player.Wait()
	}

	go func() {
		_ = player.Wait()
		_ = player.Close()
	}()

	return player, nil
}

func cleanText(s string) string {
	return strings.TrimSpace(s)
}

func validateRequest(r *Request) error {
	if r == nil {
		return errors.New("nil tts request")
	}
	if cleanText(r.Text) == "" {
		return ErrEmptyText
	}
	return nil
}

func defaultString(v string, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func defaultVoice(v Voice, fallback Voice) Voice {
	if strings.TrimSpace(string(v)) == "" {
		return fallback
	}
	return v
}
