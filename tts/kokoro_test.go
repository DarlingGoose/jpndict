package tts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestKokoroSynthesizeReportsInstallCommandWhenMissing(t *testing.T) {
	client := NewKokoroClient(KokoroConfig{Path: "definitely-not-kokoro-tts"})

	_, err := client.Synthesize(context.Background(), &Request{
		Text:       "こんにちは",
		OutputPath: t.TempDir() + "/speech.wav",
	})
	if !errors.Is(err, ErrKokoroTTSNotInstalled) {
		t.Fatalf("Synthesize() error = %v, want %v", err, ErrKokoroTTSNotInstalled)
	}
}

func TestKokoroModelPathsUsesCacheDir(t *testing.T) {
	cacheDir := t.TempDir()
	client := NewKokoroClient(KokoroConfig{CacheDir: cacheDir})

	modelPath, voicesPath, err := client.ModelPaths()
	if err != nil {
		t.Fatalf("ModelPaths() error = %v", err)
	}
	if modelPath != filepath.Join(cacheDir, kokoroModelFile) {
		t.Fatalf("model path = %q", modelPath)
	}
	if voicesPath != filepath.Join(cacheDir, kokoroVoiceFile) {
		t.Fatalf("voices path = %q", voicesPath)
	}
}

func TestKokoroInstallModelFilesDownloadsMissingFiles(t *testing.T) {
	var modelDownloads int32
	var voicesDownloads int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/model.onnx":
			atomic.AddInt32(&modelDownloads, 1)
			_, _ = w.Write([]byte("model"))
		case "/voices.bin":
			atomic.AddInt32(&voicesDownloads, 1)
			_, _ = w.Write([]byte("voices"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cacheDir := t.TempDir()
	client := NewKokoroClient(KokoroConfig{
		CacheDir:  cacheDir,
		ModelURL:  server.URL + "/model.onnx",
		VoicesURL: server.URL + "/voices.bin",
	})

	if err := client.InstallModelFiles(context.Background()); err != nil {
		t.Fatalf("InstallModelFiles() error = %v", err)
	}
	if err := client.InstallModelFiles(context.Background()); err != nil {
		t.Fatalf("second InstallModelFiles() error = %v", err)
	}

	model, err := os.ReadFile(filepath.Join(cacheDir, kokoroModelFile))
	if err != nil {
		t.Fatalf("read model: %v", err)
	}
	voices, err := os.ReadFile(filepath.Join(cacheDir, kokoroVoiceFile))
	if err != nil {
		t.Fatalf("read voices: %v", err)
	}
	if string(model) != "model" {
		t.Fatalf("model contents = %q", string(model))
	}
	if string(voices) != "voices" {
		t.Fatalf("voices contents = %q", string(voices))
	}
	if got := atomic.LoadInt32(&modelDownloads); got != 1 {
		t.Fatalf("model downloads = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&voicesDownloads); got != 1 {
		t.Fatalf("voices downloads = %d, want 1", got)
	}
}

func TestKokoroSynthesizeUsesCachedModelFiles(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	modelPath := filepath.Join(cacheDir, kokoroModelFile)
	voicesPath := filepath.Join(cacheDir, kokoroVoiceFile)
	if err := os.WriteFile(modelPath, []byte("model"), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	if err := os.WriteFile(voicesPath, []byte("voices"), 0o644); err != nil {
		t.Fatalf("write voices: %v", err)
	}

	argsPath := filepath.Join(dir, "args.txt")
	fakeKokoro := filepath.Join(dir, "kokoro-tts")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsPath + "\nprintf audio > \"$2\"\n"
	if err := os.WriteFile(fakeKokoro, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake kokoro: %v", err)
	}

	client := NewKokoroClient(KokoroConfig{
		Path:     fakeKokoro,
		CacheDir: cacheDir,
	})
	output := filepath.Join(dir, "speech.wav")

	_, err := client.Synthesize(context.Background(), &Request{
		Text:       "こんにちは",
		OutputPath: output,
	})
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}

	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	argsText := string(args)
	if !strings.Contains(argsText, "--model\n"+modelPath+"\n") {
		t.Fatalf("args do not include cached model path:\n%s", argsText)
	}
	if !strings.Contains(argsText, "--voices\n"+voicesPath+"\n") {
		t.Fatalf("args do not include cached voices path:\n%s", argsText)
	}
}

func TestJapaneseToKana(t *testing.T) {
	got, err := JapaneseToKana("日本語の音声テストです。", KanaHiragana)
	if err != nil {
		t.Fatalf("JapaneseToKana() error = %v", err)
	}
	want := "にほんごのおんせいてすとです。"
	if got != want {
		t.Fatalf("JapaneseToKana() = %q, want %q", got, want)
	}

	got, err = JapaneseToKana("日本語", KanaKatakana)
	if err != nil {
		t.Fatalf("JapaneseToKana() katakana error = %v", err)
	}
	if got != "ニホンゴ" {
		t.Fatalf("JapaneseToKana() katakana = %q, want %q", got, "ニホンゴ")
	}
}

func TestKokoroSynthesizeConvertsJapaneseToKana(t *testing.T) {
	dir := t.TempDir()

	inputPath := filepath.Join(dir, "input.txt")
	fakeKokoro := filepath.Join(dir, "kokoro-tts")
	script := "#!/bin/sh\ncp \"$1\" " + inputPath + "\nprintf audio > \"$2\"\n"
	if err := os.WriteFile(fakeKokoro, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake kokoro: %v", err)
	}

	client := NewKokoroClient(KokoroConfig{
		Path:     fakeKokoro,
		Language: LanguageJapanese,
	})
	output := filepath.Join(dir, "speech.wav")

	_, err := client.Synthesize(context.Background(), &Request{
		Text:       "日本語です。",
		OutputPath: output,
	})
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}

	input, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatalf("read input: %v", err)
	}
	if string(input) != "にほんごです。" {
		t.Fatalf("kokoro input = %q, want %q", string(input), "にほんごです。")
	}
}
