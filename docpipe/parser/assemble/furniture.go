package assemble

import (
	"strings"
	"unicode"

	"github.com/thanhenti/bepaylot/docpipe/textutil"
	"github.com/thanhenti/bepaylot/docpipe/types"
)

// MarkFurniture flags header/footer blocks whose text repeats on at least
// ratio of the pages as page furniture (running headers/footers), re-renders
// affected pages and returns their indexes. A header that appears once — such
// as a certificate's issuing authority — stays content (§5.2 note 6).
func MarkFurniture(pages []*types.ParsedPage, ratio float64) []int {
	if len(pages) < 2 || ratio <= 0 {
		return nil
	}
	seen := map[string]map[int]bool{}
	for pi, p := range pages {
		for _, b := range p.Blocks {
			if !isRunning(b.Type) {
				continue
			}
			k := furnitureKey(b.Text)
			if k == "" {
				continue
			}
			if seen[k] == nil {
				seen[k] = map[int]bool{}
			}
			seen[k][pi] = true
		}
	}
	need := int(float64(len(pages))*ratio + 0.999)
	need = max(need, 2)
	var changed []int
	for pi, p := range pages {
		dirty := false
		for bi := range p.Blocks {
			b := &p.Blocks[bi]
			if !isRunning(b.Type) || b.IsFurniture {
				continue
			}
			if len(seen[furnitureKey(b.Text)]) >= need {
				b.IsFurniture = true
				dirty = true
			}
		}
		if dirty {
			Render(p)
			changed = append(changed, pi)
		}
	}
	return changed
}

func isRunning(t types.BlockType) bool {
	return t == types.BlockHeader || t == types.BlockFooter
}

// furnitureKey folds accents and replaces digits so "Trang 3" == "Trang 4".
func furnitureKey(s string) string {
	s = textutil.Normalize(s)
	return strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			return '#'
		}
		return r
	}, s)
}
