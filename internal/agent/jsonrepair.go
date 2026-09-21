package agent

import (
	"regexp"
	"strings"

	"github.com/thanhenti/bepilot/internal/tools"
)

// plainJSONNumber is the RFC 8259 number grammar.
var plainJSONNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// exprChars are the bytes an arithmetic expression may contain in a JSON value
// position (no commas or names, so no function calls).
const exprChars = "0123456789+-*/%^().eE \t\r\n"

// repairJSONExpressions rewrites arithmetic written where a JSON value belongs
// (`"expected": 658558466616 + 33142036070`) into the exact number it denotes
// (`"expected": 691700502686`), using the same evaluator as the calculate tool.
// It returns the repaired text and how many values it rewrote.
//
// It only touches value positions — after a `:` or inside an array — and never
// the inside of strings, so keys and text values are safe. Anything it cannot
// evaluate (`1.547.961`, `12%`, a stray word) is left exactly as written.
func repairJSONExpressions(s string) (string, int) {
	var (
		out   strings.Builder
		stack []byte // open containers: '{' or '['
		prev  byte   // last significant byte seen outside a string
		fixed int
	)
	valuePosition := func() bool {
		switch prev {
		case ':', '[':
			return true
		case ',':
			return len(stack) > 0 && stack[len(stack)-1] == '['
		}
		return false
	}

	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"':
			j := skipJSONString(s, i)
			out.WriteString(s[i:j])
			i, prev = j, '"'
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			out.WriteByte(c)
			i++
		case c == '{' || c == '[':
			stack = append(stack, c)
			out.WriteByte(c)
			i, prev = i+1, c
		case c == '}' || c == ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			out.WriteByte(c)
			i, prev = i+1, c
		case c == ',' || c == ':':
			out.WriteByte(c)
			i, prev = i+1, c
		case valuePosition() && (c >= '0' && c <= '9' || c == '-' || c == '+' || c == '(' || c == '.'):
			j, ok := scanExprValue(s, i)
			tok := s[i:j]
			trimmed := strings.TrimRight(tok, " \t\r\n")
			if ok && !plainJSONNumber.MatchString(trimmed) {
				if num, err := tools.EvalNumber(trimmed); err == nil {
					out.WriteString(num)
					out.WriteString(tok[len(trimmed):])
					fixed++
					i, prev = j, '0'
					continue
				}
			}
			out.WriteString(tok)
			i, prev = j, '0'
		default:
			out.WriteByte(c)
			i, prev = i+1, c
		}
	}
	return out.String(), fixed
}

// skipJSONString returns the index just past the string that opens at s[i].
func skipJSONString(s string, i int) int {
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '"':
			return j + 1
		}
	}
	return len(s)
}

// scanExprValue returns the end of the value that starts at s[i]: the next
// top-level `,` `}` or `]` (or the end of input). ok is false when it hits
// something that cannot be part of an arithmetic expression first.
func scanExprValue(s string, i int) (end int, ok bool) {
	depth := 0
	for j := i; j < len(s); j++ {
		c := s[j]
		switch {
		case c == '(':
			depth++
		case c == ')':
			if depth == 0 {
				return j, false
			}
			depth--
		case depth == 0 && (c == ',' || c == '}' || c == ']'):
			return j, true
		case strings.IndexByte(exprChars, c) < 0:
			return j, false
		}
	}
	return len(s), true
}
