package tts

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type KokoroConfig struct {
	Path         string
	Voice        Voice
	Voices       []Voice
	Language     Language
	Format       Format
	Speed        float64
	KanaMode     KanaMode
	CacheDir     string
	ModelPath    string
	VoicesPath   string
	ModelURL     string
	VoicesURL    string
	AutoDownload bool
	HTTPClient   *http.Client
}

type KokoroClient struct {
	path         string
	voice        Voice
	voices       []Voice
	language     Language
	format       Format
	speed        float64
	kanaMode     KanaMode
	cacheDir     string
	modelPath    string
	voicesPath   string
	modelURL     string
	voicesURL    string
	autoDownload bool
	httpClient   *http.Client
}

const (
	kokoroModelFile = "kokoro-v1.0.onnx"
	kokoroVoiceFile = "voices-v1.0.bin"
	kokoroModelURL  = "https://github.com/nazdridoy/kokoro-tts/releases/download/v1.0.0/" + kokoroModelFile
	kokoroVoicesURL = "https://github.com/nazdridoy/kokoro-tts/releases/download/v1.0.0/" + kokoroVoiceFile
)

func NewKokoroClient(cfg KokoroConfig) *KokoroClient {
	language := cfg.Language
	if language == "" {
		language = LanguageJapanese
	}
	format := cfg.Format
	if format == "" {
		format = FormatWAV
	}
	voice := cfg.Voice
	if voice == "" {
		voice = KokoroVoiceJFAlpha
	}
	kanaMode := cfg.KanaMode
	if kanaMode == "" && language == LanguageJapanese {
		kanaMode = KanaHiragana
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Minute}
	}

	return &KokoroClient{
		path:         defaultString(cfg.Path, "kokoro-tts"),
		voice:        voice,
		voices:       append([]Voice(nil), cfg.Voices...),
		language:     language,
		format:       format,
		speed:        cfg.Speed,
		kanaMode:     kanaMode,
		cacheDir:     strings.TrimSpace(cfg.CacheDir),
		modelPath:    strings.TrimSpace(cfg.ModelPath),
		voicesPath:   strings.TrimSpace(cfg.VoicesPath),
		modelURL:     defaultString(cfg.ModelURL, kokoroModelURL),
		voicesURL:    defaultString(cfg.VoicesURL, kokoroVoicesURL),
		autoDownload: cfg.AutoDownload,
		httpClient:   httpClient,
	}
}

func (c *KokoroClient) Synthesize(ctx context.Context, r *Request) (*Response, error) {
	if err := validateRequest(r); err != nil {
		return nil, err
	}
	if _, err := exec.LookPath(c.path); err != nil {
		return nil, ErrKokoroTTSNotInstalled
	}

	language := r.Language
	if language == "" {
		language = c.language
	}
	if !c.IsLanguageSupported(language) {
		return nil, ErrUnsupportedLanguage
	}

	voice := defaultVoice(r.Voice, c.voice)
	if !c.isVoiceSupported(voice) {
		return nil, ErrUnsupportedVoice
	}

	format := r.Format
	if format == "" {
		format = c.format
	}
	if format != FormatWAV && format != FormatMP3 {
		return nil, ErrUnsupportedFormat
	}

	audioPath, cleanupAudio, err := outputPath(r.OutputPath, format)
	if err != nil {
		return nil, err
	}
	defer cleanupAudio()

	speed := r.Speed
	if speed == 0 {
		speed = c.speed
	}
	text := cleanText(r.Text)
	kanaMode := r.KanaMode
	if kanaMode == "" {
		kanaMode = c.kanaMode
	}
	if language == LanguageJapanese && kanaMode != KanaNone {
		text, err = JapaneseToKana(text, kanaMode)
		if err != nil {
			return nil, err
		}
	}

	input, err := os.CreateTemp("", "jpndict-tts-input-*.txt")
	if err != nil {
		return nil, err
	}
	inputPath := input.Name()
	defer os.Remove(inputPath)

	if _, err := input.WriteString(text); err != nil {
		_ = input.Close()
		return nil, err
	}
	if err := input.Close(); err != nil {
		return nil, err
	}

	args := []string{inputPath, audioPath, "--lang", string(language), "--voice", string(voice), "--format", string(format)}
	if speed > 0 {
		args = append(args, "--speed", strconv.FormatFloat(speed, 'f', -1, 64))
	}
	modelPath, voicesPath, err := c.ensureModelFiles(ctx)
	if err != nil {
		return nil, err
	}
	if modelPath != "" {
		args = append(args, "--model", modelPath)
	}
	if voicesPath != "" {
		args = append(args, "--voices", voicesPath)
	}

	cmd := exec.CommandContext(ctx, c.path, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("kokoro-tts failed: %w: %s", err, strings.TrimSpace(string(output)))
	}

	return &Response{
		Request:  r,
		Path:     audioPath,
		Format:   format,
		Language: language,
		Voice:    voice,
		Source:   filepath.Base(c.path),
	}, nil
}

func (c *KokoroClient) Close() {}

func (c *KokoroClient) ModelPaths() (modelPath string, voicesPath string, err error) {
	if c.modelPath != "" && c.voicesPath != "" {
		return c.modelPath, c.voicesPath, nil
	}

	cacheDir, err := c.resolvedCacheDir()
	if err != nil {
		return "", "", err
	}

	modelPath = c.modelPath
	if modelPath == "" {
		modelPath = filepath.Join(cacheDir, kokoroModelFile)
	}
	voicesPath = c.voicesPath
	if voicesPath == "" {
		voicesPath = filepath.Join(cacheDir, kokoroVoiceFile)
	}
	return modelPath, voicesPath, nil
}

func (c *KokoroClient) InstallModelFiles(ctx context.Context) error {
	modelPath, voicesPath, err := c.ModelPaths()
	if err != nil {
		return err
	}
	if err := c.downloadFileIfMissing(ctx, modelPath, c.modelURL); err != nil {
		return err
	}
	return c.downloadFileIfMissing(ctx, voicesPath, c.voicesURL)
}

func (c *KokoroClient) SupportedLanguages() []Language {
	return []Language{
		LanguageEnglishUS,
		LanguageEnglishGB,
		LanguageJapanese,
		LanguageFrench,
		LanguageItalian,
		LanguageChinese,
	}
}

func (c *KokoroClient) IsLanguageSupported(language Language) bool {
	if language == "" {
		return true
	}
	for _, supported := range c.SupportedLanguages() {
		if language == supported {
			return true
		}
	}
	return false
}

func (c *KokoroClient) SupportedVoices() []Voice {
	if len(c.voices) > 0 {
		return append([]Voice(nil), c.voices...)
	}
	return []Voice{
		KokoroVoiceAFAlloy, KokoroVoiceAFAoede, KokoroVoiceAFBella, KokoroVoiceAFHeart, KokoroVoiceAFJessica, KokoroVoiceAFKore, KokoroVoiceAFNicole, KokoroVoiceAFNova, KokoroVoiceAFRiver, KokoroVoiceAFSarah, KokoroVoiceAFSky,
		KokoroVoiceAMAdam, KokoroVoiceAMEcho, KokoroVoiceAMEric, KokoroVoiceAMFenrir, KokoroVoiceAMLiam, KokoroVoiceAMMichael, KokoroVoiceAMOnyx, KokoroVoiceAMPuck,
		KokoroVoiceBFAlice, KokoroVoiceBFEmma, KokoroVoiceBFIsabella, KokoroVoiceBFLily, KokoroVoiceBMDaniel, KokoroVoiceBMFable, KokoroVoiceBMGeorge, KokoroVoiceBMLewis,
		KokoroVoiceFFSiwis,
		KokoroVoiceIFSara, KokoroVoiceIMNicola,
		KokoroVoiceJFAlpha, KokoroVoiceJFGongitsune, KokoroVoiceJFNezumi, KokoroVoiceJFTebukuro, KokoroVoiceJMKumo,
		KokoroVoiceZFXiaobei, KokoroVoiceZFXiaoni, KokoroVoiceZFXiaoxiao, KokoroVoiceZFXiaoyi, KokoroVoiceZMYunjian, KokoroVoiceZMYunxi, KokoroVoiceZMYunxia, KokoroVoiceZMYunyang,
	}
}

func (c *KokoroClient) SupportedModels() []string {
	return []string{"kokoro"}
}

func (c *KokoroClient) ensureModelFiles(ctx context.Context) (string, string, error) {
	modelPath, voicesPath, err := c.ModelPaths()
	if err != nil {
		return "", "", err
	}
	if fileExists(modelPath) && fileExists(voicesPath) {
		return modelPath, voicesPath, nil
	}
	if !c.autoDownload {
		if c.modelPath != "" || c.voicesPath != "" {
			return modelPath, voicesPath, nil
		}
		return "", "", nil
	}
	if err := c.InstallModelFiles(ctx); err != nil {
		return "", "", err
	}
	return modelPath, voicesPath, nil
}

func (c *KokoroClient) resolvedCacheDir() (string, error) {
	if c.cacheDir != "" {
		return c.cacheDir, nil
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, "jpndict", "kokoro-tts"), nil
}

func (c *KokoroClient) downloadFileIfMissing(ctx context.Context, path string, sourceURL string) error {
	if fileExists(path) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		_ = tmp.Close()
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = tmp.Close()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("download kokoro model file %s returned status %d: %s", sourceURL, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (c *KokoroClient) isVoiceSupported(voice Voice) bool {
	voice = Voice(strings.TrimSpace(string(voice)))
	if voice == "" {
		return true
	}
	for _, supported := range c.SupportedVoices() {
		if voice == supported {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
