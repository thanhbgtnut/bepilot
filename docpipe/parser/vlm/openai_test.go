package vlm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAITranscribe(t *testing.T) {
	status, answer := http.StatusOK, `{"choices":[{"message":{"content":"---\nprimary_language: vi\n---\n# Tiêu đề"},"finish_reason":"length"}],"usage":{"prompt_tokens":12,"completion_tokens":5}}`
	var got struct {
		Model     string
		MaxTokens int `json:"max_tokens"`
		Messages  []struct {
			Role    string
			Content []struct {
				Type     string
				Text     string
				ImageURL struct{ URL string } `json:"image_url"`
			}
		}
	}
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
		w.Write([]byte(answer))
	}))
	defer srv.Close()

	c := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1/", APIKey: "k", Model: "olmocr"})
	if err := c.Health(context.Background()); err != nil || c.Model() != "olmocr" {
		t.Fatalf("health %v model %q", err, c.Model())
	}
	tr, err := c.Transcribe(context.Background(), Request{Prompt: "đọc trang", JPEG: []byte("jpg")})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Text != "# Tiêu đề" || tr.Meta["primary_language"] != "vi" || !tr.Truncated || tr.PromptTokens != 12 || tr.CompletionTokens != 5 {
		t.Errorf("transcription = %+v", tr)
	}
	m := got.Messages[0]
	if auth != "Bearer k" || got.Model != "olmocr" || got.MaxTokens != 4096 || m.Role != "user" || m.Content[0].Text != "đọc trang" ||
		m.Content[1].Type != "image_url" || m.Content[1].ImageURL.URL != "data:image/jpeg;base64,anBn" {
		t.Errorf("request = %+v (auth %q)", got, auth)
	}

	// A 4xx other than 429 is permanent; 429 and 5xx are retried.
	for code, retry := range map[int]bool{http.StatusBadRequest: false, http.StatusTooManyRequests: true, http.StatusBadGateway: true} {
		status, answer = code, `{"error":"x"}`
		if _, err := c.Transcribe(context.Background(), Request{}); err == nil || Retryable(err) != retry || !strings.Contains(err.Error(), "HTTP") {
			t.Errorf("HTTP %d: err %v, retryable %v", code, err, Retryable(err))
		}
	}
	if err := NewOpenAI(OpenAIConfig{}).Health(context.Background()); err == nil {
		t.Error("an unconfigured client must not be healthy")
	}
}
