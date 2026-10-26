// Package turboocr is the built-in default OCR engine: a client for
// TurboOCR's `POST /ocr/raw` (raw image body, JSON lines + layout + tables).
package turboocr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/thanhenti/bepaylot/docpipe/parser"
	"github.com/thanhenti/bepaylot/docpipe/types"
)

// Name is the engine name used in config and the registry.
const Name = "turboocr"

// ErrCircuitOpen is returned while the breaker is open.
var ErrCircuitOpen = errors.New("turboocr: circuit open, OCR service unhealthy")

// Config configures the client.
type Config struct {
	BaseURL         string
	Timeout         time.Duration
	BreakerFailures int
	BreakerOpenFor  time.Duration
	HTTPClient      *http.Client
}

// Client implements parser.Engine.
type Client struct {
	base string
	hc   *http.Client

	mu        sync.Mutex
	failures  int
	threshold int
	openFor   time.Duration
	openUntil time.Time
}

// New returns a TurboOCR client.
func New(cfg Config) *Client {
	hc := cfg.HTTPClient
	if hc == nil {
		// A short dial timeout: an unreachable service must fail fast (and
		// open the breaker) instead of holding every page for Timeout.
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
		hc = &http.Client{Timeout: cfg.Timeout, Transport: tr}
	}
	if cfg.BreakerFailures <= 0 {
		cfg.BreakerFailures = 5
	}
	if cfg.BreakerOpenFor <= 0 {
		cfg.BreakerOpenFor = 30 * time.Second
	}
	return &Client{
		base:      strings.TrimRight(cfg.BaseURL, "/"),
		hc:        hc,
		threshold: cfg.BreakerFailures,
		openFor:   cfg.BreakerOpenFor,
	}
}

// Name implements parser.Engine.
func (c *Client) Name() string { return Name }

// Health reports configuration problems and an open breaker.
func (c *Client) Health(context.Context) error {
	if c.base == "" {
		return errors.New("turboocr: base_url is not configured")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.openUntil) {
		return ErrCircuitOpen
	}
	return nil
}

// ParsePage streams the page image to /ocr/raw and decodes the response.
func (c *Client) ParsePage(ctx context.Context, in parser.PageImage, opt parser.PageOptions) (*parser.RawPage, error) {
	if c.base == "" {
		return nil, errors.New("turboocr: base_url is not configured")
	}
	if err := c.allow(); err != nil {
		return nil, err
	}
	q := url.Values{}
	flag := func(k string, on bool) {
		if on {
			q.Set(k, "1")
		}
	}
	flag("autorotate", opt.Autorotate)
	flag("layout", opt.Layout)
	flag("reading_order", opt.ReadingOrder)
	flag("tables", opt.Tables)
	flag("formulas", opt.Formulas)
	u := c.base + "/ocr/raw"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, in.Body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if in.Size > 0 {
		req.ContentLength = in.Size
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.record(ctx.Err() == nil) // a cancelled task is not the service's fault
		return nil, fmt.Errorf("turboocr: request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		c.record(true)
		return nil, fmt.Errorf("turboocr: read response: %w", err)
	}
	if resp.StatusCode >= 500 {
		c.record(true)
		return nil, fmt.Errorf("turboocr: status %d: %s", resp.StatusCode, truncate(body, 300))
	}
	c.record(false)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("turboocr: status %d: %s", resp.StatusCode, truncate(body, 300))
	}
	page, err := Decode(body)
	if err != nil {
		return nil, err
	}
	page.Width, page.Height = in.Width, in.Height
	return page, nil
}

func (c *Client) allow() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.openUntil) {
		return ErrCircuitOpen
	}
	return nil
}

func (c *Client) record(failed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !failed {
		c.failures = 0
		return
	}
	c.failures++
	if c.failures >= c.threshold {
		c.openUntil = time.Now().Add(c.openFor)
		c.failures = 0
	}
}

// response mirrors the /ocr/raw JSON.
type response struct {
	Results []struct {
		ID          int          `json:"id"`
		Text        string       `json:"text"`
		Confidence  float64      `json:"confidence"`
		BoundingBox [][2]float64 `json:"bounding_box"`
		LayoutID    *int         `json:"layout_id"`
	} `json:"results"`
	Layout []struct {
		ID          int          `json:"id"`
		Class       string       `json:"class"`
		Confidence  float64      `json:"confidence"`
		BoundingBox [][2]float64 `json:"bounding_box"`
	} `json:"layout"`
	ReadingOrder []int `json:"reading_order"`
	Tables       []struct {
		LayoutID int    `json:"layout_id"`
		HTML     string `json:"html"`
	} `json:"tables"`
	Formulas []struct {
		LayoutID int    `json:"layout_id"`
		LaTeX    string `json:"latex"`
	} `json:"formulas"`
}

// Decode converts an /ocr/raw JSON body into a RawPage.
func Decode(body []byte) (*parser.RawPage, error) {
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("turboocr: decode response: %w", err)
	}
	page := &parser.RawPage{ReadingOrder: r.ReadingOrder, Raw: body}
	for _, l := range r.Results {
		lid := -1
		if l.LayoutID != nil {
			lid = *l.LayoutID
		}
		page.Lines = append(page.Lines, parser.RawLine{
			ID: l.ID, Text: l.Text, Confidence: l.Confidence, Quad: quad(l.BoundingBox), LayoutID: lid,
		})
	}
	regions := map[int]int{}
	for _, g := range r.Layout {
		regions[g.ID] = len(page.Regions)
		page.Regions = append(page.Regions, parser.RawRegion{
			ID: g.ID, Class: g.Class, Confidence: g.Confidence, Quad: quad(g.BoundingBox),
		})
	}
	for _, t := range r.Tables {
		if i, ok := regions[t.LayoutID]; ok {
			page.Regions[i].HTML = t.HTML
		}
	}
	for _, f := range r.Formulas {
		if i, ok := regions[f.LayoutID]; ok {
			page.Regions[i].LaTeX = f.LaTeX
		}
	}
	return page, nil
}

func quad(pts [][2]float64) types.Quad {
	var q types.Quad
	switch {
	case len(pts) >= 4:
		copy(q[:], pts[:4])
	case len(pts) == 2: // [TL, BR]
		q = types.QuadFromBBox(types.BBox{X0: pts[0][0], Y0: pts[0][1], X1: pts[1][0], Y1: pts[1][1]})
	}
	return q
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
