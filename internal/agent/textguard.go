package agent

import "strings"

// textGuard sits between the model's streamed text and everything that leaves
// the server (SSE deltas and the persisted message). Prose and ordinary code
// blocks pass straight through; a JSON region — a ```json fence, or a bare
// object/array at the start of a line — is held until it is complete and then
// released with any arithmetic in value position evaluated (see
// repairJSONExpressions). That is what guarantees a client never receives
// `"expected": a + b + c`, whatever the model wrote.
//
// It works line by line: a line that could still turn out to open a region is
// held until its newline; any other line is released as it arrives. Output is
// identical however the input is chunked.
type textGuard struct {
	mode guardMode
	line strings.Builder // current incomplete line, held while undecided
	// partial is true once the start of the current line has been released, so
	// the rest of it passes straight through.
	partial bool
	// inFence is true inside a non-JSON fenced block, whose contents are
	// never treated as JSON.
	inFence bool

	// Held region.
	regionOpen string          // the opening fence line (fenced regions)
	region     strings.Builder // region body, or the whole region when raw
	depth      int             // bracket depth (raw regions)
	inStr, esc bool            // string state (raw regions)

	fixes int // values rewritten so far
}

type guardMode int

const (
	modePass guardMode = iota
	modeFenced
	modeRaw
)

// feed accepts a streamed chunk and returns the text that is now safe to send.
func (g *textGuard) feed(chunk string) string {
	var out strings.Builder
	for len(chunk) > 0 {
		seg, complete := chunk, false
		if nl := strings.IndexByte(chunk, '\n'); nl >= 0 {
			seg, complete = chunk[:nl+1], true
		}
		chunk = chunk[len(seg):]
		g.consume(seg, complete, &out)
	}
	return out.String()
}

// flush releases everything still held; call it when the text block ends.
func (g *textGuard) flush() string {
	var out strings.Builder
	switch g.mode {
	case modePass:
		out.WriteString(g.line.String())
	case modeFenced:
		g.region.WriteString(g.line.String())
		g.endFenced("", &out)
	case modeRaw:
		g.region.WriteString(g.line.String())
		g.endRaw(&out)
	}
	g.line.Reset()
	g.partial = false
	return out.String()
}

func (g *textGuard) consume(seg string, complete bool, out *strings.Builder) {
	switch g.mode {
	case modeFenced:
		g.line.WriteString(seg)
		if !complete {
			return
		}
		line := g.line.String()
		g.line.Reset()
		if strings.TrimSpace(line) == "```" {
			g.endFenced(line, out)
			return
		}
		g.region.WriteString(line)

	case modeRaw:
		g.line.WriteString(seg)
		if !complete {
			return
		}
		line := g.line.String()
		g.line.Reset()
		g.region.WriteString(line)
		g.scanRaw(line)
		if g.depth <= 0 {
			g.endRaw(out)
		}

	default:
		if g.partial {
			out.WriteString(seg)
			g.partial = !complete
			return
		}
		g.line.WriteString(seg)
		if !complete {
			if g.mightStartRegion(g.line.String()) {
				return // hold until the line is complete
			}
			out.WriteString(g.line.String())
			g.line.Reset()
			g.partial = true
			return
		}
		line := g.line.String()
		g.line.Reset()
		g.startOrPass(line, out)
	}
}

// mightStartRegion reports whether an incomplete line could still become the
// opening of a fenced or raw JSON region.
func (g *textGuard) mightStartRegion(line string) bool {
	t := strings.TrimLeft(line, " \t")
	if t == "" || strings.HasPrefix("```", t) || strings.HasPrefix(t, "```") {
		return true
	}
	return !g.inFence && (t[0] == '{' || t[0] == '[')
}

// startOrPass handles one complete line while not inside a region.
func (g *textGuard) startOrPass(line string, out *strings.Builder) {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "```") {
		if g.inFence {
			g.inFence = false
			out.WriteString(line)
			return
		}
		switch strings.ToLower(strings.TrimSpace(t[3:])) {
		case "", "json", "jsonc", "json5":
			g.mode = modeFenced
			g.regionOpen = line
			g.region.Reset()
		default:
			g.inFence = true
			out.WriteString(line)
		}
		return
	}
	if !g.inFence && startsRawJSON(t) {
		g.mode = modeRaw
		g.region.Reset()
		g.depth, g.inStr, g.esc = 0, false, false
		g.region.WriteString(line)
		g.scanRaw(line)
		if g.depth <= 0 {
			g.endRaw(out)
		}
		return
	}
	out.WriteString(line)
}

// startsRawJSON reports whether a trimmed line looks like the start of a JSON
// object or array (and not, say, a Markdown link).
func startsRawJSON(t string) bool {
	switch {
	case strings.HasPrefix(t, "{"):
		return true
	case strings.HasPrefix(t, "["):
		rest := strings.TrimSpace(t[1:])
		return rest == "" || strings.ContainsRune(`{["`, rune(rest[0]))
	}
	return false
}

// scanRaw advances the bracket depth over a line of a raw region.
func (g *textGuard) scanRaw(line string) {
	for i := 0; i < len(line); i++ {
		c := line[i]
		if g.inStr {
			switch {
			case g.esc:
				g.esc = false
			case c == '\\':
				g.esc = true
			case c == '"':
				g.inStr = false
			}
			continue
		}
		switch c {
		case '"':
			g.inStr = true
		case '{', '[':
			g.depth++
		case '}', ']':
			g.depth--
		}
	}
}

func (g *textGuard) endRaw(out *strings.Builder) {
	fixed, n := repairJSONExpressions(g.region.String())
	g.fixes += n
	out.WriteString(fixed)
	g.region.Reset()
	g.mode = modePass
}

// endFenced releases a fenced region; closeLine is "" when the block ended
// before the closing fence arrived.
func (g *textGuard) endFenced(closeLine string, out *strings.Builder) {
	body := g.region.String()
	if t := strings.TrimSpace(body); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		var n int
		body, n = repairJSONExpressions(body)
		g.fixes += n
	}
	out.WriteString(g.regionOpen)
	out.WriteString(body)
	out.WriteString(closeLine)
	g.region.Reset()
	g.regionOpen = ""
	g.mode = modePass
}
