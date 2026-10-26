package assemble

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/thanhenti/bepaylot/docpipe/types"
)

// Render renumbers blocks and lines in slice order, renders the page markdown
// and records rune offsets of every block and line in it. Lines keep their
// relative order; they are grouped by block.
func Render(page *types.ParsedPage) {
	renumber(page)

	var sb strings.Builder
	pos := 0 // rune offset of sb
	write := func(s string) {
		sb.WriteString(s)
		pos += utf8.RuneCountInString(s)
	}

	byBlock := make(map[int][]int, len(page.Blocks))
	for i := range page.Lines {
		page.Lines[i].MdStart, page.Lines[i].MdEnd = -1, -1
		byBlock[page.Lines[i].BlockNo] = append(byBlock[page.Lines[i].BlockNo], i)
	}

	// A page the VLM read shows the VLM's text only: a block it left
	// without text keeps its OCR lines as reference data (§5.9).
	vlmPage := page.TextSource == types.TextSourceVLM

	first := true
	sep := func() {
		if !first {
			write("\n\n")
		}
		first = false
	}

	for bi := range page.Blocks {
		b := &page.Blocks[bi]
		b.MdStart, b.MdEnd = -1, -1
		lines := byBlock[b.BlockNo]
		if b.IsFurniture {
			continue
		}
		// A VLM-refined block renders its transcription verbatim; its lines
		// are located inside it (§5.9).
		refined := b.TextSource == types.TextSourceVLM && strings.TrimSpace(b.Text) != ""
		if vlmPage && !refined {
			if b.Type != types.BlockFigure || b.AssetKey == "" {
				continue
			}
			lines = nil // the figure's image alone
		}
		// writeLines emits one markdown line per text line, with a prefix,
		// and records the line offsets (after the prefix).
		writeLines := func(prefix string) {
			n := 0
			for _, li := range lines {
				l := &page.Lines[li]
				if l.Text == "" {
					continue
				}
				if n > 0 {
					write("\n")
				}
				write(prefix)
				l.MdStart = pos
				write(l.Text)
				l.MdEnd = pos
				n++
			}
		}
		hasText := false
		for _, li := range lines {
			if page.Lines[li].Text != "" {
				hasText = true
				break
			}
		}
		writeRefined := func() {
			base := pos
			write(b.Text)
			locateLines(page, lines, b.Text, base)
		}

		switch b.Type {
		case types.BlockTable:
			md := tableMarkdown(b.HTML)
			if md == "" && !refined && !hasText {
				continue
			}
			sep()
			b.MdStart = pos
			switch {
			case md != "":
				base := pos
				write(md)
				locateLines(page, lines, md, base) // synthetic row lines
			case refined:
				base := pos
				write(b.Text)
				locateLines(page, lines, b.Text, base)
			default:
				writeLines("")
			}
			b.MdEnd = pos
		case types.BlockFigure:
			if b.AssetKey == "" && !hasText && !refined {
				continue
			}
			sep()
			b.MdStart = pos
			if b.AssetKey != "" {
				write(fmt.Sprintf("![%s p%d-b%d](%s)", figureLabel(b.RawClass), page.PageNo, b.BlockNo, b.AssetKey))
				if hasText || refined {
					write("\n")
				}
			}
			if refined {
				writeRefined()
			} else {
				writeLines("> ")
			}
			b.MdEnd = pos
		case types.BlockFormula:
			if b.LaTeX == "" && !hasText {
				continue
			}
			sep()
			b.MdStart = pos
			if b.LaTeX != "" {
				write("$$\n" + strings.TrimSpace(b.LaTeX) + "\n$$")
			} else {
				writeLines("")
			}
			b.MdEnd = pos
		case types.BlockTitle, types.BlockHeading:
			if !hasText && !refined {
				continue
			}
			sep()
			b.MdStart = pos
			prefix := "# "
			if b.Type == types.BlockHeading {
				prefix = headingPrefix(b.Text)
			}
			// A multi-line heading is one markdown heading line.
			write(prefix)
			if refined {
				writeRefined()
				b.MdEnd = pos
				break
			}
			n := 0
			for _, li := range lines {
				l := &page.Lines[li]
				if l.Text == "" {
					continue
				}
				if n > 0 {
					write(" ")
				}
				l.MdStart = pos
				write(l.Text)
				l.MdEnd = pos
				n++
			}
			b.MdEnd = pos
		default:
			if !hasText && !refined {
				continue
			}
			sep()
			b.MdStart = pos
			if refined {
				writeRefined()
			} else {
				writeLines("")
			}
			b.MdEnd = pos
		}
	}
	page.Markdown = sb.String()

	blank := true
	for _, l := range page.Lines {
		if strings.TrimSpace(l.Text) != "" {
			blank = false
			break
		}
	}
	for _, b := range page.Blocks {
		if b.HTML != "" || b.LaTeX != "" || (b.TextSource == types.TextSourceVLM && strings.TrimSpace(b.Text) != "") {
			blank = false
		}
	}
	page.IsBlank = blank
}

var (
	reNumbered2 = regexp.MustCompile(`^\d+\.\d+(\.\d+)*[.)]?\s`)
	reRoman     = regexp.MustCompile(`^[IVXLC]+[.)]\s`)
)

func headingPrefix(text string) string {
	switch {
	case reNumbered2.MatchString(text):
		return "### "
	case reRoman.MatchString(text):
		return "## "
	default:
		return "## "
	}
}

// renumber assigns BlockNo by slice order, remaps line block references and
// sorts lines by block (stable), then assigns LineNo.
func renumber(page *types.ParsedPage) {
	remap := make(map[int]int, len(page.Blocks))
	for i := range page.Blocks {
		remap[page.Blocks[i].BlockNo] = i
		page.Blocks[i].BlockNo = i
	}
	for i := range page.Lines {
		if nb, ok := remap[page.Lines[i].BlockNo]; ok {
			page.Lines[i].BlockNo = nb
		}
	}
	sort.SliceStable(page.Lines, func(a, b int) bool { return page.Lines[a].BlockNo < page.Lines[b].BlockNo })
	for i := range page.Lines {
		page.Lines[i].LineNo = i
	}
}

// PlainText returns the page text (non-furniture lines joined by newlines),
// used for page-level full-text search.
// A VLM-refined block contributes its whole transcription, which may hold
// words no OCR line aligned to.
func PlainText(page *types.ParsedPage) string {
	skip := map[int]bool{}
	for _, b := range page.Blocks {
		if b.IsFurniture {
			skip[b.BlockNo] = true
		}
	}
	var parts []string
	emitted := map[int]bool{}
	for _, l := range page.Lines {
		if skip[l.BlockNo] {
			continue
		}
		if l.BlockNo >= 0 && l.BlockNo < len(page.Blocks) {
			b := page.Blocks[l.BlockNo]
			if b.BlockNo == l.BlockNo && b.TextSource == types.TextSourceVLM && b.Type != types.BlockTable {
				if !emitted[l.BlockNo] {
					emitted[l.BlockNo] = true
					parts = append(parts, b.Text)
				}
				continue
			}
		}
		if l.Text != "" {
			parts = append(parts, l.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// figureLabel is the alt text of a figure: seals are tagged from the layout
// class alone (no VLM call, §5.9), so a search for "con dấu" finds them.
func figureLabel(rawClass string) string {
	if strings.EqualFold(rawClass, "seal") {
		return "con dấu"
	}
	return "figure"
}
