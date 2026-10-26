package decision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thanhenti/bepaylot/docpipe/types"
)

func jpegOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestClassifyPage(t *testing.T) {
	var got request
	answer := `{"answers":{"document_type":{"type":"choice","choice":"uy_nhiem_chi","probabilities":{"uy_nhiem_chi":0.99,"hop_dong":0.01},"confidence":0.98},"is_first_page":{"type":"choice","choice":"YES","probabilities":{"YES":0.9,"NO":0.1},"confidence":0.53}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" || r.Method != http.MethodPost {
			t.Errorf("request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.Write([]byte(answer))
	}))
	defer srv.Close()

	labels := []types.ClassLabel{{Name: "uy_nhiem_chi", Title: "Ủy nhiệm chi", Description: "Payment order"}, {Name: "hop_dong", Title: "Hợp đồng"}}
	c := New(Config{BaseURL: srv.URL + "/", Model: "m1", Instructions: "Đây là loại chứng từ nào?", MaxSide: 100})
	label, conf, isFirstPage, err := c.ClassifyPage(context.Background(), labels, jpegOf(t, 400, 200), "GIẤY CHỨNG NHẬN QUYỀN SỬ DỤNG ĐẤT VÀ TÀI SẢN KHÁC GẮN LIỀN VỚI ĐẤT")
	if err != nil || label != "uy_nhiem_chi" || conf != 0.98 || !isFirstPage {
		t.Fatalf("got %q %v %v %v", label, conf, isFirstPage, err)
	}
	// is_first_page goes in the same request: yes or no.
	if b := got.Questions["is_first_page"]; b.Type != "choice" || len(b.Criteria) != 2 || b.Criteria["YES"] == "" || b.Criteria["NO"] == "" {
		t.Errorf("is_first_page question: %+v", b)
	}
	// The OCR text of the page goes in the state with the image.
	if !strings.Contains(got.State, "TÀI SẢN KHÁC GẮN LIỀN") {
		t.Errorf("state without the OCR text: %q", got.State)
	}
	q := got.Questions["document_type"]
	if got.Model != "m1" || q.Type != "choice" || q.Criteria["uy_nhiem_chi"] != "Ủy nhiệm chi — Payment order" || q.Criteria["hop_dong"] != "Hợp đồng" {
		t.Errorf("payload: %+v", got)
	}
	// The image goes inline as raw base64 (no data URL prefix), downscaled to
	// max_side.
	raw, err := base64.StdEncoding.DecodeString(got.Images[0])
	if err != nil || strings.HasPrefix(got.Images[0], "data:") {
		t.Fatalf("image is not raw base64: %v", err)
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(raw)); err != nil || cfg.Width != 100 || cfg.Height != 50 {
		t.Errorf("image size: %+v %v", cfg, err)
	}

	// A page answered NO, or an answer without is_first_page, is not a first page.
	for _, b := range []string{`,"is_first_page":{"choice":"NO","confidence":0.8}`, ``} {
		answer = `{"answers":{"document_type":{"choice":"hop_dong","confidence":0.9}` + b + `}}`
		if label, _, isFirstPage, err := c.ClassifyPage(context.Background(), labels, jpegOf(t, 10, 10), ""); err != nil || label != "hop_dong" || isFirstPage {
			t.Errorf("is_first_page %q: got %q %v %v", b, label, isFirstPage, err)
		}
	}

	// A choice outside the labels, or an HTTP error, is an error.
	answer = `{"answers":{"document_type":{"choice":"la","confidence":0.9}}}`
	if _, _, _, err := c.ClassifyPage(context.Background(), labels, jpegOf(t, 10, 10), ""); err == nil {
		t.Error("an unknown choice must fail")
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", 500) }))
	defer bad.Close()
	if _, _, _, err := New(Config{BaseURL: bad.URL}).ClassifyPage(context.Background(), labels, jpegOf(t, 10, 10), ""); err == nil {
		t.Error("HTTP 500 must fail")
	}
}

// A server that takes only data URLs (LM Studio) answers 400 to raw base64:
// the page is asked again with a data URL, and later pages go as data URLs
// at once.
func TestClassifyPageDataURLFallback(t *testing.T) {
	var images []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got request
		json.NewDecoder(r.Body).Decode(&got)
		images = append(images, got.Images[0][:min(len(got.Images[0]), 23)])
		if !strings.HasPrefix(got.Images[0], "data:image/jpeg;base64,") {
			http.Error(w, `{"error":{"message":"Images must be inline base64 image data URLs"}}`, http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"answers":{"document_type":{"choice":"hop_dong","confidence":0.9},"is_first_page":{"choice":"YES"}}}`))
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL})
	labels := []types.ClassLabel{{Name: "hop_dong", Title: "Hợp đồng"}}
	for range 2 {
		if label, _, first, err := c.ClassifyPage(context.Background(), labels, jpegOf(t, 10, 10), ""); err != nil || label != "hop_dong" || !first {
			t.Fatalf("got %q %v %v", label, first, err)
		}
	}
	if len(images) != 3 || strings.HasPrefix(images[0], "data:") || images[1] != "data:image/jpeg;base64," || images[2] != "data:image/jpeg;base64," {
		t.Errorf("requests = %q", images)
	}
}

// A server that cannot take the two questions in one request (a small batch
// size) answers them one by one; later pages are asked one by one at once.
func TestClassifyPageSplitsQuestions(t *testing.T) {
	var asked []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got request
		json.NewDecoder(r.Body).Decode(&got)
		asked = append(asked, len(got.Questions))
		switch _, doc := got.Questions["document_type"]; {
		case len(got.Questions) > 1:
			http.Error(w, `{"error":{"message":"input (545 tokens) is too large to process"}}`, http.StatusInternalServerError)
		case doc:
			w.Write([]byte(`{"answers":{"document_type":{"choice":"hop_dong","confidence":0.9}}}`))
		default:
			w.Write([]byte(`{"answers":{"is_first_page":{"choice":"YES"}}}`))
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL})
	labels := []types.ClassLabel{{Name: "hop_dong", Title: "Hợp đồng"}}
	for range 2 {
		if label, conf, first, err := c.ClassifyPage(context.Background(), labels, jpegOf(t, 10, 10), ""); err != nil || label != "hop_dong" || conf != 0.9 || !first {
			t.Fatalf("got %q %v %v %v", label, conf, first, err)
		}
	}
	if fmt.Sprint(asked) != "[2 1 1 1 1]" {
		t.Errorf("questions per request = %v", asked)
	}
}

func TestState(t *testing.T) {
	if s := state("  "); s != "Ảnh chứng từ:" {
		t.Errorf("no text: %q", s)
	}
	long := strings.Repeat("ỹ", maxText+10)
	if s := state(long); strings.Count(s, "ỹ") != maxText {
		t.Errorf("text is not cut at %d runes", maxText)
	}
}
