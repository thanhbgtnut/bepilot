// Package pdftest builds small, valid PDFs for tests: text pages in
// Helvetica (WinAnsi), an optional outline and optional PDF/A XMP metadata.
package pdftest

import (
	"bytes"
	"fmt"
	"strings"
)

// Page is the text of one page, one entry per line.
type Page []string

// Options tunes the document.
type Options struct {
	// PDFAPart/PDFAConformance add an XMP packet with pdfaid when set.
	PDFAPart        int
	PDFAConformance string
	// Outline adds one bookmark per page titled with the page's first line.
	Outline bool
	Title   string
}

// Build returns the PDF bytes. Pages are US Letter (612x792 pt).
func Build(pages []Page, opt Options) []byte {
	var objs []string // 1-based object bodies
	add := func(body string) int {
		objs = append(objs, body)
		return len(objs)
	}
	catalog := add("") // placeholder
	pagesID := add("")
	font := add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")

	var pageIDs []int
	for _, pg := range pages {
		var cs strings.Builder
		cs.WriteString("BT /F1 14 Tf 72 720 Td 18 TL\n")
		for i, line := range pg {
			if i > 0 {
				cs.WriteString("T*\n")
			}
			fmt.Fprintf(&cs, "(%s) Tj\n", escape(line))
		}
		cs.WriteString("ET")
		content := add(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", cs.Len(), cs.String()))
		pageIDs = append(pageIDs, add(fmt.Sprintf(
			"<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>",
			pagesID, font, content)))
	}
	kids := make([]string, len(pageIDs))
	for i, id := range pageIDs {
		kids[i] = fmt.Sprintf("%d 0 R", id)
	}
	objs[pagesID-1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pageIDs))

	extra := ""
	if opt.Outline && len(pages) > 0 {
		outlines := add("")
		var items []int
		for i := range pages {
			items = append(items, add(""))
			_ = i
		}
		for i, id := range items {
			title := fmt.Sprintf("Page %d", i+1)
			if len(pages[i]) > 0 {
				title = pages[i][0]
			}
			body := fmt.Sprintf("<< /Title (%s) /Parent %d 0 R /Dest [%d 0 R /Fit]", escape(title), outlines, pageIDs[i])
			if i > 0 {
				body += fmt.Sprintf(" /Prev %d 0 R", items[i-1])
			}
			if i < len(items)-1 {
				body += fmt.Sprintf(" /Next %d 0 R", items[i+1])
			}
			objs[id-1] = body + " >>"
		}
		objs[outlines-1] = fmt.Sprintf("<< /Type /Outlines /First %d 0 R /Last %d 0 R /Count %d >>", items[0], items[len(items)-1], len(items))
		extra += fmt.Sprintf(" /Outlines %d 0 R", outlines)
	}
	if opt.PDFAPart > 0 {
		xmp := fmt.Sprintf(`<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
<rdf:Description rdf:about="" xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/" pdfaid:part="%d" pdfaid:conformance="%s"/>
</rdf:RDF></x:xmpmeta>
<?xpacket end="w"?>`, opt.PDFAPart, strings.ToUpper(opt.PDFAConformance))
		meta := add(fmt.Sprintf("<< /Type /Metadata /Subtype /XML /Length %d >>\nstream\n%s\nendstream", len(xmp), xmp))
		extra += fmt.Sprintf(" /Metadata %d 0 R", meta)
	}
	objs[catalog-1] = fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R%s >>", pagesID, extra)

	var info int
	if opt.Title != "" {
		info = add(fmt.Sprintf("<< /Title (%s) /Producer (pdftest) >>", escape(opt.Title)))
	}

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objs))
	for i, body := range objs {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	trailer := fmt.Sprintf("<< /Size %d /Root %d 0 R", len(objs)+1, catalog)
	if info > 0 {
		trailer += fmt.Sprintf(" /Info %d 0 R", info)
	}
	fmt.Fprintf(&buf, "trailer\n%s >>\nstartxref\n%d\n%%%%EOF\n", trailer, xref)
	return buf.Bytes()
}

// escape encodes s as a WinAnsi (Latin-1 subset) PDF literal string; runes
// outside Latin-1 become '?'.
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\\' || r == '(' || r == ')':
			b.WriteByte('\\')
			b.WriteByte(byte(r))
		case r < 0x80:
			b.WriteByte(byte(r))
		case r <= 0xFF:
			fmt.Fprintf(&b, "\\%03o", r)
		default:
			b.WriteByte('?')
		}
	}
	return b.String()
}
