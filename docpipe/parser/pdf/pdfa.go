package pdf

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

// PDFADetector is an io.Writer that scans a byte stream for the PDF/A
// identification in XMP metadata (pdfaid:part / pdfaid:conformance). PDF/A
// requires the metadata stream to be unfiltered, so a plain byte scan during
// upload finds it without a second read of the file (§5.8).
type PDFADetector struct {
	tail      []byte
	Part      int
	Conform   string
	isPDF     bool
	firstSeen bool
}

var (
	rePart    = regexp.MustCompile(`pdfaid:part\s*(?:=\s*["']|>)\s*([1-4])`)
	reConform = regexp.MustCompile(`pdfaid:conformance\s*(?:=\s*["']|>)\s*([A-Za-z])`)
)

const pdfaOverlap = 256

// Write implements io.Writer; it never fails.
func (d *PDFADetector) Write(p []byte) (int, error) {
	if !d.firstSeen {
		d.firstSeen = true
		d.isPDF = bytes.HasPrefix(bytes.TrimLeft(p, " \t\r\n"), []byte("%PDF-"))
	}
	if !d.isPDF || (d.Part != 0 && d.Conform != "") {
		return len(p), nil
	}
	buf := append(d.tail, p...)
	if bytes.Contains(buf, []byte("pdfaid")) {
		if d.Part == 0 {
			if m := rePart.FindSubmatch(buf); m != nil {
				d.Part, _ = strconv.Atoi(string(m[1]))
			}
		}
		if d.Conform == "" {
			if m := reConform.FindSubmatch(buf); m != nil {
				d.Conform = strings.ToLower(string(m[1]))
			}
		}
	}
	if len(buf) > pdfaOverlap {
		buf = buf[len(buf)-pdfaOverlap:]
	}
	d.tail = append(d.tail[:0], buf...)
	return len(p), nil
}

// IsPDFA reports whether a PDF/A part was found.
func (d *PDFADetector) IsPDFA() bool { return d.Part > 0 }

// Trusted reports whether the conformance level guarantees Unicode text
// (levels a and u).
func (d *PDFADetector) Trusted() bool { return d.Conform == "a" || d.Conform == "u" }
