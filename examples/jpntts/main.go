package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/DarlingGoose/jpndict/tts"
)

func main() {
	provider := flag.String("provider", "kokoro", "tts provider: kokoro or ollama")
	text := flag.String("text", "こんにちは。日本語の音声テストです。", "text to synthesize")
	output := flag.String("out", "jpntts.wav", "output audio path")
	voice := flag.String("voice", "", "voice name")
	language := flag.String("lang", "", "language code")
	format := flag.String("format", "wav", "audio format: wav or mp3")
	speed := flag.Float64("speed", 0, "speech speed")
	kanaMode := flag.String("kana", "", "Japanese kana normalization: hiragana, katakana, or none")
	play := flag.Bool("play", false, "play audio after synthesis")
	wait := flag.Bool("wait", true, "wait for playback to finish when -play is set")

	kokoroCacheDir := flag.String("kokoro-cache-dir", "", "Kokoro model cache directory")
	kokoroModelPath := flag.String("kokoro-model", "", "Kokoro ONNX model path")
	kokoroVoicesPath := flag.String("kokoro-voices", "", "Kokoro voices file path")
	kokoroModelURL := flag.String("kokoro-model-url", "", "Kokoro ONNX model download URL")
	kokoroVoicesURL := flag.String("kokoro-voices-url", "", "Kokoro voices download URL")
	kokoroAutoInstall := flag.Bool("kokoro-auto-install", false, "download missing Kokoro model files into the cache")

	ollamaBaseURL := flag.String("ollama-base-url", "http://localhost:11434", "Ollama base URL")
	ollamaSpeechURL := flag.String("ollama-speech-url", "", "OpenAI-compatible speech endpoint URL")
	ollamaModel := flag.String("ollama-model", "legraphista/Orpheus", "Ollama model")
	managedOllama := flag.Bool("managed-ollama", false, "start and manage ollama serve")

	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client, err := newClient(*provider, clientConfig{
		voice:             tts.Voice(*voice),
		language:          tts.Language(*language),
		format:            tts.Format(*format),
		speed:             *speed,
		kanaMode:          tts.KanaMode(*kanaMode),
		kokoroCacheDir:    *kokoroCacheDir,
		kokoroModelPath:   *kokoroModelPath,
		kokoroVoicesPath:  *kokoroVoicesPath,
		kokoroModelURL:    *kokoroModelURL,
		kokoroVoicesURL:   *kokoroVoicesURL,
		kokoroAutoInstall: *kokoroAutoInstall,
		ollamaBaseURL:     *ollamaBaseURL,
		ollamaSpeechURL:   *ollamaSpeechURL,
		ollamaModel:       *ollamaModel,
		managedOllama:     *managedOllama,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	resp, err := client.Synthesize(ctx, &tts.Request{
		Text:       *text,
		OutputPath: *output,
		Voice:      tts.Voice(*voice),
		Language:   tts.Language(*language),
		Format:     tts.Format(*format),
		Speed:      *speed,
		KanaMode:   tts.KanaMode(*kanaMode),
	})
	if err != nil {
		if errors.Is(err, tts.ErrKokoroTTSNotInstalled) {
			log.Fatal("kokoro-tts is not installed; install it with: yay -S kokoro-tts-git")
		}
		log.Fatal(err)
	}

	fmt.Printf("wrote %s using %s voice %s\n", resp.Path, *provider, resp.Voice)

	if *play {
		if _, err := resp.PlayAudio(*wait); err != nil {
			log.Fatal(err)
		}
	}
}

type clientConfig struct {
	voice             tts.Voice
	language          tts.Language
	format            tts.Format
	speed             float64
	kanaMode          tts.KanaMode
	kokoroCacheDir    string
	kokoroModelPath   string
	kokoroVoicesPath  string
	kokoroModelURL    string
	kokoroVoicesURL   string
	kokoroAutoInstall bool
	ollamaBaseURL     string
	ollamaSpeechURL   string
	ollamaModel       string
	managedOllama     bool
}

func newClient(provider string, cfg clientConfig) (tts.Client, error) {
	switch provider {
	case "kokoro":
		return tts.NewKokoroClient(tts.KokoroConfig{
			Voice:        cfg.voice,
			Language:     cfg.language,
			Format:       cfg.format,
			Speed:        cfg.speed,
			KanaMode:     cfg.kanaMode,
			CacheDir:     cfg.kokoroCacheDir,
			ModelPath:    cfg.kokoroModelPath,
			VoicesPath:   cfg.kokoroVoicesPath,
			ModelURL:     cfg.kokoroModelURL,
			VoicesURL:    cfg.kokoroVoicesURL,
			AutoDownload: cfg.kokoroAutoInstall,
		}), nil
	case "ollama":
		ollamaCfg := tts.OllamaConfig{
			BaseURL:   cfg.ollamaBaseURL,
			SpeechURL: cfg.ollamaSpeechURL,
			Model:     cfg.ollamaModel,
			Voice:     cfg.voice,
			Language:  cfg.language,
			Format:    cfg.format,
			Speed:     cfg.speed,
		}
		if cfg.managedOllama {
			return tts.NewManagedOllamaClient(ollamaCfg), nil
		}
		return tts.NewOllamaClient(ollamaCfg), nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", provider)
	}
}
