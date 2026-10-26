package turboocr

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/thanhenti/bepaylot/docpipe/parser"
)

func TestDecodeSample(t *testing.T) {
	body, err := os.ReadFile("testdata/output_example.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Lines) != 54 || len(p.Regions) != 35 || len(p.ReadingOrder) != 54 {
		t.Fatalf("got %d lines, %d regions, %d order", len(p.Lines), len(p.Regions), len(p.ReadingOrder))
	}
	var tables int
	for _, r := range p.Regions {
		if r.HTML != "" {
			tables++
			if r.Class != "table" {
				t.Errorf("html attached to %q region", r.Class)
			}
		}
	}
	if tables != 1 {
		t.Fatalf("tables = %d, want 1", tables)
	}
	if p.Lines[0].Text != "UBND XÃ NHA BÍCH" || p.Lines[0].LayoutID != 2 {
		t.Fatalf("first line = %+v", p.Lines[0])
	}
	if b := p.Lines[0].Quad.BBox(); b.X0 != 691 || b.Y1 != 496 {
		t.Fatalf("bbox = %+v", b)
	}
}

func TestParsePageStreamsBodyAndQuery(t *testing.T) {
	sample, _ := os.ReadFile("testdata/output_example.json")
	var gotQuery, gotType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotType = r.URL.RawQuery, r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.Write(sample)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Timeout: 5 * time.Second})
	img := []byte("jpeg-bytes")
	p, err := c.ParsePage(context.Background(), parser.PageImage{Body: bytes.NewReader(img), Size: int64(len(img)), Width: 10, Height: 20},
		parser.PageOptions{Autorotate: true, Layout: true, ReadingOrder: true, Tables: true})
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "autorotate=1&layout=1&reading_order=1&tables=1" || gotType != "application/octet-stream" || string(gotBody) != "jpeg-bytes" {
		t.Fatalf("query=%q type=%q body=%q", gotQuery, gotType, gotBody)
	}
	if p.Width != 10 || len(p.Lines) != 54 {
		t.Fatalf("page = %dx%d lines=%d", p.Width, p.Height, len(p.Lines))
	}
}

func TestBreakerOpensAfterFailures(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Timeout: time.Second, BreakerFailures: 2, BreakerOpenFor: time.Minute})
	for i := 0; i < 2; i++ {
		if _, err := c.ParsePage(context.Background(), parser.PageImage{Body: bytes.NewReader(nil)}, parser.PageOptions{}); err == nil {
			t.Fatal("want error")
		}
	}
	_, err := c.ParsePage(context.Background(), parser.PageImage{Body: bytes.NewReader(nil)}, parser.PageOptions{})
	if err != ErrCircuitOpen || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
