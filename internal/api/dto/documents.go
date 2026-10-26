package dto

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// InputDocument is a `document` or `image` content block of a request
// message, decoded. Only base64 sources are accepted.
type InputDocument struct {
	Kind      string // "document" | "image"
	Name      string // the block's `title`, if any
	MediaType string // source.media_type as declared
	Data      []byte
}

// inputBlock is the subset of an Anthropic content block that documents use.
type inputBlock struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Source *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
		URL       string `json:"url"`
	} `json:"source"`
}

// Documents returns the message's `document` and `image` blocks in order.
// Content given as a plain string has none.
func (m InputMessage) Documents() ([]InputDocument, error) {
	var blocks []inputBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return nil, nil // string content
	}
	var out []InputDocument
	for i, b := range blocks {
		if b.Type != "document" && b.Type != "image" {
			continue
		}
		if b.Source == nil || b.Source.Type != "base64" {
			return nil, fmt.Errorf("content[%d]: only base64 %s sources are supported", i, b.Type)
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b.Source.Data))
		if err != nil {
			return nil, fmt.Errorf("content[%d]: source.data is not valid base64: %w", i, err)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("content[%d]: source.data is empty", i)
		}
		out = append(out, InputDocument{Kind: b.Type, Name: b.Title, MediaType: b.Source.MediaType, Data: data})
	}
	return out, nil
}

// LastUserMessage returns the final user message of the request.
func (r MessagesRequest) LastUserMessage() (InputMessage, bool) {
	if i := r.lastUserIndex(); i >= 0 {
		return r.Messages[i], true
	}
	return InputMessage{}, false
}

func (r MessagesRequest) lastUserIndex() int {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role == "user" {
			return i
		}
	}
	return -1
}

// WithoutFileData returns the request as a generic JSON object with every
// base64 `source.data` removed. Blocks of the last user message (the ones
// that were read) get a `file_index` pointing at the stored file instead.
func (r MessagesRequest) WithoutFileData() map[string]any {
	raw, _ := json.Marshal(r)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	msgs, _ := out["messages"].([]any)
	last := r.lastUserIndex()
	for i, m := range msgs {
		mm, _ := m.(map[string]any)
		blocks, _ := mm["content"].([]any)
		n := 0
		for _, b := range blocks {
			bm, _ := b.(map[string]any)
			if bm["type"] != "document" && bm["type"] != "image" {
				continue
			}
			if src, ok := bm["source"].(map[string]any); ok {
				if d, ok := src["data"].(string); ok {
					delete(src, "data")
					src["data_bytes"] = base64.StdEncoding.DecodedLen(len(d))
				}
			}
			if i == last {
				bm["file_index"] = n
				n++
			}
		}
	}
	return out
}

// DocumentPage is one page read from an uploaded file.
type DocumentPage struct {
	PageNo   int    `json:"page_no" example:"1"`
	Markdown string `json:"markdown"`
	// Text is the plain OCR text of the page (only on /v1/documents/messages
	// records; the parse response leaves it out).
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"` // set when the page could not be read
}

// DocumentInfo describes one uploaded file and how it was read.
type DocumentInfo struct {
	Name        string `json:"name,omitempty" example:"contract.pdf"`
	MediaType   string `json:"media_type" example:"application/pdf"` // detected from the content
	SizeBytes   int    `json:"size_bytes" example:"482113"`
	Pages       int    `json:"pages" example:"3"`
	FailedPages int    `json:"failed_pages" example:"0"`
}

// ParseDocumentResponse is the body of POST /v1/documents/parse.
type ParseDocumentResponse struct {
	// RequestID is the stored document request (document_requests.id).
	RequestID string       `json:"request_id" example:"6f1c2b6e-0f6a-4b8e-9f0e-0d4d2b7f3a11"`
	Document  DocumentInfo `json:"document"`
	// Markdown is every page's markdown, separated by "\n\n---\n\n".
	Markdown string         `json:"markdown"`
	Pages    []DocumentPage `json:"pages"`
}
