// Package assemble turns an engine's RawPage into a types.ParsedPage: it maps
// layout classes to block types, assigns lines to blocks, fixes the reading
// order, renders markdown and records rune offsets for every line (§5.2–§5.4).
//
// Build and Render are pure functions so the whole step is golden-testable.
package assemble

import (
	"sort"
	"strings"

	"github.com/thanhenti/bepaylot/docpipe/parser"
	"github.com/thanhenti/bepaylot/docpipe/types"
)

// Options tunes assembly.
type Options struct {
	PageNo int
	DPI    float64
	Engine string
	// ClassMap overrides the default class → BlockType mapping.
	ClassMap map[string]string
	// ReadingOrderFix moves blocks that lie fully above an overlapping block
	// in front of it (§5.2 note 3).
	ReadingOrderFix  bool
	LowConfThreshold float64
	// AssetKey names the stored crop of a figure block; empty disables links.
	AssetKey func(pageNo, blockNo int) string
}

var defaultClassMap = map[string]types.BlockType{
	"doc_title":       types.BlockTitle,
	"paragraph_title": types.BlockHeading,
	"text":            types.BlockParagraph,
	"abstract":        types.BlockParagraph,
	"content":         types.BlockParagraph,
	"reference":       types.BlockParagraph,
	"aside_text":      types.BlockParagraph,
	"algorithm":       types.BlockParagraph,
	"table":           types.BlockTable,
	"formula":         types.BlockFormula,
	"formula_number":  types.BlockFormula,
	"image":           types.BlockFigure,
	"figure":          types.BlockFigure,
	"chart":           types.BlockFigure,
	"seal":            types.BlockFigure,
	"header_image":    types.BlockFigure,
	"footer_image":    types.BlockFigure,
	"figure_title":    types.BlockCaption,
	"table_title":     types.BlockCaption,
	"chart_title":     types.BlockCaption,
	"header":          types.BlockHeader,
	"footer":          types.BlockFooter,
	"number":          types.BlockPageNumber,
	"footnote":        types.BlockFootnote,
}

// BlockTypeOf maps an engine class name to a BlockType.
func BlockTypeOf(class string, overrides map[string]string) types.BlockType {
	c := strings.ToLower(strings.TrimSpace(class))
	if v, ok := overrides[c]; ok {
		return types.BlockType(v)
	}
	if v, ok := defaultClassMap[c]; ok {
		return v
	}
	return types.BlockUnknown
}

type workBlock struct {
	block   types.ParsedBlock
	lines   []int // indexes into raw lines
	rank    float64
	refined string // region transcription, if any
}

// Build assembles a page without markdown; call Render afterwards (possibly
// after merging a text layer) to number lines and produce markdown.
func Build(raw *parser.RawPage, opt Options) *types.ParsedPage {
	page := &types.ParsedPage{
		PageNo: opt.PageNo, Width: raw.Width, Height: raw.Height, DPI: opt.DPI,
		Engine: opt.Engine, TextSource: types.TextSourceOCR, MarkdownVLM: raw.Markdown,
	}

	// 1. Regions → blocks.
	blocks := make([]*workBlock, 0, len(raw.Regions))
	byRegion := map[int]*workBlock{}
	for _, r := range raw.Regions {
		wb := &workBlock{block: types.ParsedBlock{
			SourceID: r.ID, Type: BlockTypeOf(r.Class, opt.ClassMap), RawClass: r.Class,
			Confidence: r.Confidence, BBox: r.Quad.BBox(), HTML: r.HTML, LaTeX: r.LaTeX,
		}, refined: r.Text}
		blocks = append(blocks, wb)
		byRegion[r.ID] = wb
	}

	// 2. Reading rank of every raw line.
	rank := LineRanks(raw)

	// 3. Lines → blocks: engine assignment, else smallest containing region.
	var orphans []int
	for i, l := range raw.Lines {
		if wb, ok := byRegion[l.LayoutID]; ok {
			wb.lines = append(wb.lines, i)
			continue
		}
		if wb := containing(blocks, l.Quad.BBox()); wb != nil {
			wb.lines = append(wb.lines, i)
			continue
		}
		orphans = append(orphans, i)
	}
	// Consecutive orphans (by rank) become synthetic paragraph blocks.
	sort.Slice(orphans, func(a, b int) bool { return rank[orphans[a]] < rank[orphans[b]] })
	for i := 0; i < len(orphans); {
		j := i + 1
		for j < len(orphans) && rank[orphans[j]] == rank[orphans[j-1]]+1 {
			j++
		}
		wb := &workBlock{block: types.ParsedBlock{SourceID: -1, Type: types.BlockParagraph, RawClass: "", Confidence: 1}}
		for _, li := range orphans[i:j] {
			wb.lines = append(wb.lines, li)
			wb.block.BBox = wb.block.BBox.Union(raw.Lines[li].Quad.BBox())
		}
		blocks = append(blocks, wb)
		i = j
	}

	// 4. Order lines inside blocks; rank blocks by their first line.
	for _, wb := range blocks {
		sort.SliceStable(wb.lines, func(a, b int) bool { return rank[wb.lines[a]] < rank[wb.lines[b]] })
		if len(wb.lines) > 0 {
			wb.rank = float64(rank[wb.lines[0]])
		}
	}
	placeLineless(blocks)
	sort.SliceStable(blocks, func(a, b int) bool { return blocks[a].rank < blocks[b].rank })
	if opt.ReadingOrderFix {
		blocks = fixOrder(blocks)
	}

	// 5. Emit blocks and lines.
	for bi, wb := range blocks {
		b := wb.block
		b.BlockNo = bi
		from := len(page.Lines)
		texts := make([]string, 0, len(wb.lines))
		for _, li := range wb.lines {
			l := raw.Lines[li]
			text := strings.TrimSpace(l.Text)
			page.Lines = append(page.Lines, types.ParsedLine{
				SourceID: l.ID, BlockNo: bi, Text: text, Confidence: l.Confidence,
				Quad: l.Quad, BBox: l.Quad.BBox(),
				InFigure:      b.Type == types.BlockFigure,
				LowConfidence: opt.LowConfThreshold > 0 && l.Confidence > 0 && l.Confidence < opt.LowConfThreshold,
				TextSource:    types.TextSourceOCR,
			})
			if text != "" {
				texts = append(texts, text)
			}
		}
		b.Text = strings.Join(texts, "\n")
		if b.Type == types.BlockTable && from == len(page.Lines) {
			// A table without OCR lines (whole-page VLM mode) gets one line
			// per row so search and locate reach its cells.
			page.Lines = append(page.Lines, tableRowLines(&b, cleanRefined(b.Type, wb.refined))...)
		}
		if wb.refined != "" {
			switch b.Type {
			case types.BlockTable: // lines keep OCR text (cells)
				if t := cleanRefined(b.Type, wb.refined); t != "" {
					b.Text, b.TextSource = t, types.TextSourceVLM
				}
			default:
				refineBlock(page, &b, from, len(page.Lines), wb.refined)
			}
		}
		if b.Type == types.BlockFigure && opt.AssetKey != nil {
			b.AssetKey = opt.AssetKey(opt.PageNo, bi)
		}
		if b.Type == types.BlockPageNumber {
			b.IsFurniture = true
		}
		page.Blocks = append(page.Blocks, b)
	}
	if Refined(page) {
		page.TextSource = types.TextSourceVLM
	}
	return page
}

// LineRanks returns each raw line's position in reading order. Lines missing
// from ReadingOrder are appended by geometry (top-to-bottom, left-to-right).
func LineRanks(raw *parser.RawPage) []int {
	idx := make(map[int]int, len(raw.Lines))
	for i, l := range raw.Lines {
		idx[l.ID] = i
	}
	rank := make([]int, len(raw.Lines))
	for i := range rank {
		rank[i] = -1
	}
	next := 0
	for _, id := range raw.ReadingOrder {
		if i, ok := idx[id]; ok && rank[i] < 0 {
			rank[i] = next
			next++
		}
	}
	var rest []int
	for i := range raw.Lines {
		if rank[i] < 0 {
			rest = append(rest, i)
		}
	}
	sort.SliceStable(rest, func(a, b int) bool {
		ba, bb := raw.Lines[rest[a]].Quad.BBox(), raw.Lines[rest[b]].Quad.BBox()
		if abs(ba.Y0-bb.Y0) > min(ba.Height(), bb.Height())/2 {
			return ba.Y0 < bb.Y0
		}
		return ba.X0 < bb.X0
	})
	for _, i := range rest {
		rank[i] = next
		next++
	}
	return rank
}

// containing returns the smallest block whose box contains the centre of b.
func containing(blocks []*workBlock, b types.BBox) *workBlock {
	cx, cy := (b.X0+b.X1)/2, (b.Y0+b.Y1)/2
	var best *workBlock
	var bestArea float64
	for _, wb := range blocks {
		bb := wb.block.BBox
		if !bb.Contains(cx, cy) {
			continue
		}
		area := bb.Width() * bb.Height()
		if best == nil || area < bestArea {
			best, bestArea = wb, area
		}
	}
	return best
}

// placeLineless ranks blocks without lines just before the first ranked block
// that starts below them in an overlapping column, or at the end.
func placeLineless(blocks []*workBlock) {
	maxRank := -1.0
	for _, wb := range blocks {
		if len(wb.lines) > 0 && wb.rank > maxRank {
			maxRank = wb.rank
		}
	}
	for _, wb := range blocks {
		if len(wb.lines) > 0 {
			continue
		}
		best := maxRank + 1 + wb.block.BBox.Y0/1e6 // stable by position at the end
		for _, o := range blocks {
			if len(o.lines) == 0 {
				continue
			}
			if o.block.BBox.Y0 >= wb.block.BBox.Y0 && hOverlap(o.block.BBox, wb.block.BBox) > 0 && o.rank-0.5 < best {
				best = o.rank - 0.5
			}
		}
		wb.rank = best
	}
}

// mustPrecede reports whether a lies entirely above b and they share at least
// 30% of the narrower block's width.
func mustPrecede(a, b types.BBox) bool {
	tol := min(a.Height(), b.Height()) * 0.2
	if a.Y1 > b.Y0+tol {
		return false
	}
	w := min(a.Width(), b.Width())
	return w > 0 && hOverlap(a, b) >= 0.3*w
}

// fixOrder moves every block in front of the earliest block it must precede.
func fixOrder(blocks []*workBlock) []*workBlock {
	for pass := 0; pass < len(blocks); pass++ {
		moved := false
		for i := 1; i < len(blocks); i++ {
			for j := 0; j < i; j++ {
				if mustPrecede(blocks[i].block.BBox, blocks[j].block.BBox) && !mustPrecede(blocks[j].block.BBox, blocks[i].block.BBox) {
					b := blocks[i]
					copy(blocks[j+1:i+1], blocks[j:i])
					blocks[j] = b
					moved = true
					break
				}
			}
		}
		if !moved {
			break
		}
	}
	return blocks
}

func hOverlap(a, b types.BBox) float64 {
	return max(0, min(a.X1, b.X1)-max(a.X0, b.X0))
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
