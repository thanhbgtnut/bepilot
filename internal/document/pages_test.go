package document

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/thanhenti/bepaylot/docpipe/parser/pdf/pdftest"
)

func threePagePDF() []byte {
	return pdftest.Build([]pdftest.Page{{"ONE"}, {"TWO"}, {"THREE"}}, pdftest.Options{})
}

func TestParsePagesStreamsEveryPage(t *testing.T) {
	ocr, calls := fakeOCR(t, 0)
	svc := newService(t, ocr.URL)
	total := -1
	var got []int
	mt, err := svc.ParsePages(context.Background(), File{Data: threePagePDF()}, nil, PageSink{
		Total: func(n int) error { total = n; return nil },
		Page: func(p Page) error {
			if p.Error != "" || p.Markdown == "" {
				t.Errorf("page %d: error %q, %d chars", p.PageNo, p.Error, len(p.Markdown))
			}
			got = append(got, p.PageNo)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Ints(got)
	if mt != TypePDF || total != 3 || len(got) != 3 || got[0] != 1 || got[2] != 3 || calls.Load() != 3 {
		t.Fatalf("media %q, total %d, pages %v, %d OCR calls", mt, total, got, calls.Load())
	}
}

// A resumed run reads only the pages it does not have yet.
func TestParsePagesSkipsDonePages(t *testing.T) {
	ocr, calls := fakeOCR(t, 0)
	svc := newService(t, ocr.URL)
	var got []int
	_, err := svc.ParsePages(context.Background(), File{Data: threePagePDF()}, map[int]bool{1: true, 3: true}, PageSink{
		Total: func(int) error { return nil },
		Page:  func(p Page) error { got = append(got, p.PageNo); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 2 || calls.Load() != 1 {
		t.Fatalf("pages %v, %d OCR calls; want page 2 only", got, calls.Load())
	}
}

func TestParsePagesStopsOnSinkError(t *testing.T) {
	ocr, _ := fakeOCR(t, 0)
	svc := newService(t, ocr.URL)
	stop := errors.New("document deleted")
	_, err := svc.ParsePages(context.Background(), File{Data: threePagePDF()}, nil, PageSink{
		Total: func(int) error { return nil },
		Page:  func(Page) error { return stop },
	})
	if !errors.Is(err, stop) {
		t.Fatalf("err = %v, want the sink's error", err)
	}
}

func TestParsePagesRejectsUnreadable(t *testing.T) {
	svc := newService(t, "http://127.0.0.1:1")
	sink := PageSink{Total: func(int) error { return nil }, Page: func(Page) error { return nil }}
	if _, err := svc.ParsePages(context.Background(), File{Data: []byte("%PDF-1.4 broken")}, nil, sink); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("err = %v, want ErrUnreadable", err)
	}
	if _, err := svc.ParsePages(context.Background(), File{Data: []byte("text")}, nil, sink); err != ErrUnsupported {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}
