package tts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type OllamaConfig struct {
	BaseURL    string
	SpeechURL  string
	APIKey     string
	Model      string
	Models     []string
	HTTPClient *http.Client

	Managed     bool
	OllamaPath  string
	Host        string
	Port        string
	StartupWait time.Duration

	Voice       Voice
	Voices      []Voice
	Language    Language
	Format      Format
	Speed       float64
	Temperature *float64
	MaxTokens   int
}

type OllamaClient struct {
	baseURL    string
	speechURL  string
	apiKey     string
	model      string
	models     []string
	httpClient *http.Client

	managed bool
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	mu      sync.Mutex

	ollamaPath  string
	host        string
	port        string
	startupWait time.Duration

	voice       Voice
	voices      []Voice
	language    Language
	format      Format
	speed       float64
	temperature *float64
	maxTokens   int

	modelMu      sync.Mutex
	modelEnsured bool
}

type ollamaTagsResponse struct {
	Models []struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"models"`
}

type ollamaPullRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

type ollamaPullResponse struct {
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

type speechRequest struct {
	Model        string         `json:"model"`
	Input        string         `json:"input"`
	Voice        string         `json:"voice,omitempty"`
	ResponseType string         `json:"response_format,omitempty"`
	Speed        float64        `json:"speed,omitempty"`
	Instructions string         `json:"instructions,omitempty"`
	Temperature  *float64       `json:"temperature,omitempty"`
	MaxTokens    int            `json:"max_tokens,omitempty"`
	Options      map[string]any `json:"options,omitempty"`
}

func NewOllamaClient(cfg OllamaConfig) *OllamaClient {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	model := cfg.Model
	if model == "" {
		model = "legraphista/Orpheus"
	}

	language := cfg.Language
	if language == "" {
		language = LanguageEnglishUS
	}

	format := cfg.Format
	if format == "" {
		format = FormatWAV
	}

	voice := cfg.Voice
	if voice == "" {
		voice = Voice("tara")
	}

	return &OllamaClient{
		baseURL:     strings.TrimRight(baseURL, "/"),
		speechURL:   strings.TrimSpace(cfg.SpeechURL),
		apiKey:      cfg.APIKey,
		model:       model,
		models:      append([]string(nil), cfg.Models...),
		httpClient:  httpClient,
		ollamaPath:  defaultString(cfg.OllamaPath, "ollama"),
		host:        defaultString(cfg.Host, "127.0.0.1"),
		port:        defaultString(cfg.Port, "11434"),
		startupWait: defaultDuration(cfg.StartupWait, 20*time.Second),
		voice:       voice,
		voices:      append([]Voice(nil), cfg.Voices...),
		language:    language,
		format:      format,
		speed:       cfg.Speed,
		temperature: cfg.Temperature,
		maxTokens:   cfg.MaxTokens,
	}
}

func NewManagedOllamaClient(cfg OllamaConfig) *OllamaClient {
	cfg.Managed = true

	host := defaultString(cfg.Host, "127.0.0.1")
	port := defaultString(cfg.Port, "11434")
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://" + net.JoinHostPort(host, port)
	}

	c := NewOllamaClient(cfg)
	c.managed = true
	c.host = host
	c.port = port
	return c
}

func (c *OllamaClient) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.managed {
		return nil
	}
	if c.cmd != nil && c.cmd.Process != nil {
		return nil
	}
	if err := c.ping(ctx); err == nil {
		return c.ensureModel(ctx)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	cmd := exec.CommandContext(runCtx, c.ollamaPath, "serve")
	cmd.Env = append(cmd.Environ(), "OLLAMA_HOST="+net.JoinHostPort(c.host, c.port))

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start ollama: %w", err)
	}

	c.cmd = cmd
	go drainPipe(stdout)
	go drainPipe(stderr)

	deadline := time.Now().Add(c.startupWait)
	for time.Now().Before(deadline) {
		if err := c.ping(ctx); err == nil {
			return c.ensureModel(ctx)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}

	return fmt.Errorf("ollama did not become ready at %s", c.baseURL)
}

func (c *OllamaClient) Synthesize(ctx context.Context, r *Request) (*Response, error) {
	if err := validateRequest(r); err != nil {
		return nil, err
	}
	if c.managed {
		if err := c.Start(ctx); err != nil {
			return nil, err
		}
	}
	if err := c.ensureModel(ctx); err != nil {
		return nil, err
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

	outputPath, cleanup, err := outputPath(r.OutputPath, format)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	speed := r.Speed
	if speed == 0 {
		speed = c.speed
	}

	payload := speechRequest{
		Model:        c.model,
		Input:        cleanText(r.Text),
		Voice:        string(voice),
		ResponseType: string(format),
		Speed:        speed,
		Instructions: strings.TrimSpace(r.Instructions),
		Temperature:  c.temperature,
		MaxTokens:    c.maxTokens,
		Options:      r.Options,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.resolvedSpeechURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(c.apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama tts returned status %d: %s", resp.StatusCode, string(respBody))
	}

	if err := writeResponseBody(outputPath, resp.Body); err != nil {
		return nil, err
	}

	return &Response{
		Request:    r,
		Path:       outputPath,
		Format:     format,
		Language:   language,
		Voice:      voice,
		Model:      c.model,
		Source:     c.resolvedSpeechURL(),
		SampleRate: 24000,
	}, nil
}

func (c *OllamaClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
	c.cmd = nil
}

func (c *OllamaClient) SupportedLanguages() []Language {
	return []Language{LanguageEnglishUS}
}

func (c *OllamaClient) IsLanguageSupported(language Language) bool {
	return language == "" || language == LanguageEnglishUS
}

func (c *OllamaClient) SupportedVoices() []Voice {
	if len(c.voices) > 0 {
		return append([]Voice(nil), c.voices...)
	}
	return []Voice{"tara", "leah", "jess", "leo", "dan", "mia", "zac", "zoe"}
}

func (c *OllamaClient) SupportedModels() []string {
	if len(c.models) > 0 {
		return append([]string(nil), c.models...)
	}
	if c.model == "" {
		return nil
	}
	return []string{c.model}
}

func (c *OllamaClient) resolvedSpeechURL() string {
	if c.speechURL != "" {
		return c.speechURL
	}
	return c.baseURL + "/v1/audio/speech"
}

func (c *OllamaClient) isVoiceSupported(voice Voice) bool {
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

func (c *OllamaClient) ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ollama ping status %d", resp.StatusCode)
	}
	return nil
}

func (c *OllamaClient) ensureModel(ctx context.Context) error {
	c.modelMu.Lock()
	defer c.modelMu.Unlock()

	if c.modelEnsured {
		return nil
	}

	exists, err := c.hasModel(ctx)
	if err != nil {
		return err
	}
	if !exists {
		if err := c.pullModel(ctx); err != nil {
			return err
		}
	}

	c.modelEnsured = true
	return nil
}

func (c *OllamaClient) hasModel(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return false, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("ollama tags status %d: %s", resp.StatusCode, string(respBody))
	}

	var decoded ollamaTagsResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return false, fmt.Errorf("ollama decode tags response: %w: %s", err, string(respBody))
	}

	for _, model := range decoded.Models {
		if model.Name == c.model || model.Model == c.model {
			return true, nil
		}
	}
	return false, nil
}

func (c *OllamaClient) pullModel(ctx context.Context) error {
	payload := ollamaPullRequest{Model: c.model, Stream: false}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var decoded ollamaPullResponse
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &decoded); err != nil {
			return fmt.Errorf("ollama decode pull response: %w: %s", err, string(respBody))
		}
	}
	if decoded.Error != "" {
		return fmt.Errorf("ollama pull error: %s", decoded.Error)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ollama pull status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func outputPath(path string, format Format) (string, func(), error) {
	if strings.TrimSpace(path) != "" {
		return path, func() {}, nil
	}

	f, err := os.CreateTemp("", "jpndict-tts-*."+string(format))
	if err != nil {
		return "", nil, err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", nil, err
	}
	return name, func() {}, nil
}

func writeResponseBody(path string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, r)
	return err
}

func drainPipe(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		_ = scanner.Text()
	}
}

func defaultDuration(v time.Duration, fallback time.Duration) time.Duration {
	if v == 0 {
		return fallback
	}
	return v
}
