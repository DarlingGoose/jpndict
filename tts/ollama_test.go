package tts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
)

func TestOllamaSynthesizePullsMissingModelAndCallsSpeechEndpoint(t *testing.T) {
	var tagsCalls int32
	var pullCalls int32
	var speechCalls int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			atomic.AddInt32(&tagsCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[]}`))
		case "/api/pull":
			atomic.AddInt32(&pullCalls, 1)

			var req ollamaPullRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode pull request: %v", err)
			}
			if req.Model != "legraphista/Orpheus" {
				t.Fatalf("pull model = %q, want %q", req.Model, "legraphista/Orpheus")
			}
			if req.Stream {
				t.Fatal("pull request should disable streaming")
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success"}`))
		case "/v1/audio/speech":
			atomic.AddInt32(&speechCalls, 1)

			var req speechRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode speech request: %v", err)
			}
			if req.Model != "legraphista/Orpheus" {
				t.Fatalf("speech model = %q, want %q", req.Model, "legraphista/Orpheus")
			}
			if req.Input != "hello" {
				t.Fatalf("speech input = %q, want %q", req.Input, "hello")
			}
			if req.Voice != "tara" {
				t.Fatalf("speech voice = %q, want %q", req.Voice, "tara")
			}
			if req.ResponseType != "wav" {
				t.Fatalf("speech response_format = %q, want %q", req.ResponseType, "wav")
			}

			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write([]byte("WAVDATA"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	output := t.TempDir() + "/speech.wav"
	client := NewOllamaClient(OllamaConfig{
		BaseURL: server.URL,
		Model:   "legraphista/Orpheus",
	})

	resp, err := client.Synthesize(context.Background(), &Request{
		Text:       "hello",
		OutputPath: output,
	})
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if resp.Path != output {
		t.Fatalf("path = %q, want %q", resp.Path, output)
	}
	gotAudio, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(gotAudio) != "WAVDATA" {
		t.Fatalf("audio = %q, want %q", string(gotAudio), "WAVDATA")
	}
	if got := atomic.LoadInt32(&tagsCalls); got != 1 {
		t.Fatalf("tags calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&pullCalls); got != 1 {
		t.Fatalf("pull calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&speechCalls); got != 1 {
		t.Fatalf("speech calls = %d, want 1", got)
	}
}

func TestOllamaSynthesizeSkipsPullWhenModelExists(t *testing.T) {
	var pullCalls int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"legraphista/Orpheus","model":"legraphista/Orpheus"}]}`))
		case "/api/pull":
			atomic.AddInt32(&pullCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success"}`))
		case "/v1/audio/speech":
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write([]byte("WAVDATA"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewOllamaClient(OllamaConfig{
		BaseURL: server.URL,
		Model:   "legraphista/Orpheus",
	})

	_, err := client.Synthesize(context.Background(), &Request{
		Text:       "hello",
		OutputPath: t.TempDir() + "/speech.wav",
	})
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if got := atomic.LoadInt32(&pullCalls); got != 0 {
		t.Fatalf("pull calls = %d, want 0", got)
	}
}
