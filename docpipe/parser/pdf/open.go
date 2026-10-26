package pdf

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/responses"
)

// openDocument opens path on p. The returned release must be called after the
// document is closed. go-pdfium's WebAssembly runtime only sees POSIX paths
// under its mounted root, so a path carrying a Windows volume ("D:\...") is
// streamed through a file reader instead of being opened by name. Native
// (multi_threaded) PDFium and Linux/macOS paths use FilePath directly.
func (r *Renderer) openDocument(p pdfium.Pdfium, path string) (*responses.OpenDocument, func(), error) {
	noop := func() {}
	if r.cfg.Mode == "multi_threaded" {
		doc, err := p.OpenDocument(&requests.OpenDocument{FilePath: &path})
		return doc, noop, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, noop, err
	}
	if filepath.VolumeName(abs) == "" {
		doc, err := p.OpenDocument(&requests.OpenDocument{FilePath: &abs})
		return doc, noop, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, noop, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, noop, err
	}
	if st.Size() == 0 {
		f.Close()
		return nil, noop, fmt.Errorf("%s: empty file", abs)
	}
	doc, err := p.OpenDocument(&requests.OpenDocument{FileReader: f, FileReaderSize: st.Size()})
	if err != nil {
		f.Close()
		return nil, noop, err
	}
	return doc, func() { f.Close() }, nil
}
