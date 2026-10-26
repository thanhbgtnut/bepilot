// Package decision is the client of the Decision API, Ollama's System One
// endpoint (POST /v1/systemone, https://docs.ollama.com/api/systemone): a
// typed question about an image (and its OCR text), answered with a choice
// and its probabilities. bepaylot uses it to read the document type of a
// page (§6.9.4).
package decision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // page images are JPEG; PNG is accepted too
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/image/draw"

	"github.com/thanhenti/bepaylot/docpipe/types"
)

// Config is the classify.decision block of the configuration.
type Config struct {
	BaseURL      string
	Model        string
	Instructions string
	Timeout      time.Duration
	MaxSide      int
}

// Client calls the Decision API.
type Client struct {
	cfg  Config
	http *http.Client
	// dataURL is set once the server refused a raw base64 image and took a
	// data URL: Ollama wants raw base64, LM Studio wants a data URL.
	dataURL atomic.Bool
	// split is set once the server refused the two questions in one request
	// and answered them one by one (a local server with a small batch size).
	split atomic.Bool
}

// New builds a client.
func New(cfg Config) *Client {
	cfg.BaseURL = strings.TrimSuffix(strings.TrimRight(cfg.BaseURL, "/"), "/v1/systemone")
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}
}

const question = "document_type"

// firstPageQuestion asks whether the page is the first page of a document:
// the pages of a file are cut into documents at every page answered
// firstPageYes (§6.9.4).
const (
	firstPageQuestion     = "is_first_page"
	firstPageYes          = "YES"
	firstPageNo           = "NO"
	firstPageInstructions = "Trang này có phải là trang đầu tiên của một tài liệu không?"
)

var firstPageCriteria = map[string]string{
	firstPageYes: "Trang đầu của tài liệu, có tiêu đề",
	firstPageNo:  "Trang tiếp theo",
}

type request struct {
	Model     string                    `json:"model"`
	State     string                    `json:"state"`
	Images    []string                  `json:"images"`
	Questions map[string]choiceQuestion `json:"questions"`
}

type choiceQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type response struct {
	Answers map[string]struct {
		Choice     string  `json:"choice"`
		Confidence float64 `json:"confidence"`
	} `json:"answers"`
}

// maxText is the most OCR text of a page sent with its image, in runes: the
// title is at the top of the page, and a longer state can go past the
// model's batch size.
const maxText = 1500

// state is the context of the questions: the page image, and the OCR text
// of the page when there is one, so that two types whose names differ by
// one word ("tài sản" / "tài sản khác") are told apart by the exact words
// of the title rather than by how the page looks.
func state(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "Ảnh chứng từ:"
	}
	if r := []rune(text); len(r) > maxText {
		text = string(r[:maxText])
	}
	return "Ảnh chứng từ, kèm văn bản OCR của trang. Đọc đúng từng chữ của tiêu đề trong văn bản: " +
		"hai loại giấy tờ có thể chỉ khác nhau một từ.\n\nVăn bản OCR:\n" + text
}

// ClassifyPage implements interfaces.PageClassifier: it asks which of labels
// the page is, from its image and its OCR text (text, "" when there is none),
// and in the same call whether the page is the first page of a document
// (is_first_page). The image goes inline as raw base64, which
// is what Ollama takes (it refuses a "data:" prefix as illegal base64). A
// server that answers 400 to that (LM Studio takes only data URLs) is asked
// again with a data URL, and gets data URLs from then on. A server that
// fails on the two questions together (LM Studio: "input is too large",
// they exceed the model's batch size) is asked them one by one, and from
// then on. An answer without is_first_page is not a first page.
func (c *Client) ClassifyPage(ctx context.Context, labels []types.ClassLabel, img []byte, text string) (string, float64, bool, error) {
	criteria := make(map[string]string, len(labels))
	for _, l := range labels {
		criteria[l.Name] = l.Title
		if l.Description != "" {
			criteria[l.Name] = l.Title + " — " + l.Description
		}
	}
	jpg := downscale(img, c.cfg.MaxSide)
	b64 := base64.StdEncoding.EncodeToString(jpg)
	post := func(asDataURL bool, questions map[string]choiceQuestion) (int, []byte, error) {
		image := b64
		if asDataURL {
			image = "data:" + http.DetectContentType(jpg) + ";base64," + b64
		}
		body, err := json.Marshal(request{Model: c.cfg.Model, State: state(text), Images: []string{image}, Questions: questions})
		if err != nil {
			return 0, nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/v1/systemone", bytes.NewReader(body))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			return 0, nil, fmt.Errorf("decision api: %w", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, raw, nil
	}
	// ask sends questions and merges the answers into out.
	var out response
	ask := func(questions map[string]choiceQuestion) error {
		asDataURL := c.dataURL.Load()
		status, raw, err := post(asDataURL, questions)
		if err == nil && status == http.StatusBadRequest && !asDataURL {
			if status, raw, err = post(true, questions); err == nil && status == http.StatusOK {
				c.dataURL.Store(true)
			}
		}
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("decision api: HTTP %d: %s", status, strings.TrimSpace(string(raw[:min(len(raw), 300)])))
		}
		var got response
		if err := json.Unmarshal(raw, &got); err != nil {
			return fmt.Errorf("decision api: %w", err)
		}
		if out.Answers == nil {
			out.Answers = got.Answers
		}
		for k, v := range got.Answers {
			out.Answers[k] = v
		}
		return nil
	}
	docQ := map[string]choiceQuestion{question: {Type: "choice", Instructions: c.cfg.Instructions, Criteria: criteria}}
	firstQ := map[string]choiceQuestion{firstPageQuestion: {Type: "choice", Instructions: firstPageInstructions, Criteria: firstPageCriteria}}
	together := !c.split.Load()
	var err error
	if together {
		err = ask(map[string]choiceQuestion{question: docQ[question], firstPageQuestion: firstQ[firstPageQuestion]})
	}
	if !together || err != nil {
		if err = ask(docQ); err == nil {
			if err = ask(firstQ); err == nil {
				c.split.Store(true)
			}
		}
	}
	if err != nil {
		return "", 0, false, err
	}
	a, ok := out.Answers[question]
	if _, known := criteria[a.Choice]; !ok || !known {
		return "", 0, false, fmt.Errorf("decision api: no valid choice in the answer (%q)", a.Choice)
	}
	return a.Choice, a.Confidence, out.Answers[firstPageQuestion].Choice == firstPageYes, nil
}

// downscale re-encodes an image whose long side is above maxSide; any other
// image (or one that does not decode) is sent as it is.
func downscale(b []byte, maxSide int) []byte {
	if maxSide <= 0 {
		return b
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || max(cfg.Width, cfg.Height) <= maxSide {
		return b
	}
	src, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return b
	}
	f := float64(maxSide) / float64(max(cfg.Width, cfg.Height))
	dst := image.NewRGBA(image.Rect(0, 0, max(1, int(float64(cfg.Width)*f+0.5)), max(1, int(float64(cfg.Height)*f+0.5))))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 90}); err != nil {
		return b
	}
	return buf.Bytes()
}
