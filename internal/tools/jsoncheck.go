package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// --- json_validate ----------------------------------------------------------

type jsonValidateArgs struct {
	Text string `json:"text" jsonschema:"required" jsonschema_description:"The exact JSON text to check, as you intend to give it to the user (without surrounding Markdown fences)."`
}

type jsonValidateOutput struct {
	Valid bool `json:"valid"`
	// Type is the top-level JSON type when valid.
	Type   string `json:"type,omitempty"`
	Error  string `json:"error,omitempty"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
	// Context is the text around the error, with >>> <<< around the offending byte.
	Context string `json:"context,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

func newJSONValidateTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"json_validate",
		"Check that text is strictly valid JSON (RFC 8259) and, if not, report the line/column and a fix hint. Use it before you answer with a JSON document the user will parse, especially a long one built from computed numbers.",
		func(_ context.Context, a jsonValidateArgs) (jsonValidateOutput, error) {
			return validateJSON(a.Text), nil
		},
	)
}

func validateJSON(text string) jsonValidateOutput {
	data := []byte(strings.TrimPrefix(text, "\ufeff"))
	if len(bytes.TrimSpace(data)) == 0 {
		return jsonValidateOutput{Error: "empty input"}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var v any
	err := dec.Decode(&v)
	if err == nil {
		// Decode stops after the first value; anything but whitespace after it
		// (a second document, a stray comma) is invalid.
		off := int(dec.InputOffset())
		if tail := bytes.TrimLeft(data[off:], " \t\r\n"); len(tail) > 0 {
			return describeJSONError(data, len(data)-len(tail), "unexpected data after the top-level value")
		}
		return jsonValidateOutput{Valid: true, Type: jsonType(v)}
	}

	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		off := int(syn.Offset) - 1 // Offset is the count of bytes read, i.e. one past the bad byte.
		if strings.Contains(syn.Error(), "unexpected end") {
			off = len(data)
		}
		return describeJSONError(data, off, syn.Error())
	case errors.As(err, &typ):
		return describeJSONError(data, int(typ.Offset), typ.Error())
	}
	return jsonValidateOutput{Error: err.Error()}
}

func describeJSONError(data []byte, off int, msg string) jsonValidateOutput {
	if off < 0 {
		off = 0
	}
	if off > len(data) {
		off = len(data)
	}
	line, col := 1, 1
	for _, b := range data[:off] {
		if b == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	const window = 40
	lo, hi := max(0, off-window), min(len(data), off+window)
	var ctx strings.Builder
	ctx.Write(data[lo:off])
	if off < len(data) {
		fmt.Fprintf(&ctx, ">>>%c<<<", rune(data[off]))
		ctx.Write(data[off+1 : hi])
	} else {
		ctx.WriteString(">>><<<")
	}
	return jsonValidateOutput{
		Error:   msg,
		Line:    line,
		Column:  col,
		Context: ctx.String(),
		Hint:    jsonHint(data, off),
	}
}

// jsonHint recognises the mistakes models make most often and says how to fix
// them.
func jsonHint(data []byte, off int) string {
	if off >= len(data) {
		return "The document ends early: an object, array or string is not closed."
	}
	c := data[off]
	if c == '+' || c == '-' || c == '*' || c == '/' {
		prev := bytes.TrimRight(data[:off], " \t\r\n")
		if n := len(prev); n > 0 && (prev[n-1] >= '0' && prev[n-1] <= '9' || prev[n-1] == ')') {
			return "A JSON value must be a single literal, not an arithmetic expression. Compute the result (use the calculate tool) and write only the resulting number, e.g. \"expected\": 691700502686."
		}
	}
	switch {
	case c == '\'':
		return "JSON strings use double quotes, not single quotes."
	case c == '/' || c == '#':
		return "JSON does not allow comments."
	case c == '}' || c == ']':
		return "Check for a trailing comma or a missing value just before this closing bracket."
	case c == '.' || (c >= '0' && c <= '9'):
		return "Numbers must not contain thousand separators or leading zeros; write 1547961374470, not 1.547.961.374.470 or 1,547,961."
	}
	return ""
}

func jsonType(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	}
	return "unknown"
}
