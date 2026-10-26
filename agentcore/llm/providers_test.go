package llm

import "testing"

func TestNormalizeOpenAIBaseURL(t *testing.T) {
	cases := map[string]string{
		"":                          "",
		"http://127.0.0.1:1234/v1":  "http://127.0.0.1:1234/v1",
		"http://127.0.0.1:1234/v1/": "http://127.0.0.1:1234/v1",
		"http://127.0.0.1:1234/v1/chat/completions":  "http://127.0.0.1:1234/v1",
		"https://api.openai.com/v1/chat/completions": "https://api.openai.com/v1",
		" http://host/v1/embeddings ":                "http://host/v1",
	}
	for in, want := range cases {
		if got := normalizeOpenAIBaseURL(in); got != want {
			t.Errorf("normalizeOpenAIBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}
