package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/thanhenti/bepilot/internal/agent/events"
)

// The case reported from production: the model left the sum unevaluated.
const reportedBad = `{
  "check": "Lưu chuyển tiền thuần trong kỳ + Tiền đầu kỳ + Ảnh hưởng tỷ giá = Tiền cuối kỳ (LCTT)",
  "passed": true,
  "expected": -39292632903 + 68499552588 + 617729,
  "actual": 29207537414,
  "difference": 0
}`

func TestRepairJSONExpressions(t *testing.T) {
	cases := []struct {
		name, in, want string
		n              int
	}{
		{"reported case", `{"expected": -39292632903 + 68499552588 + 617729, "actual": 29207537414}`,
			`{"expected": 29207537414, "actual": 29207537414}`, 1},
		{"parentheses and precedence", `{"a": (1 + 2) * 3 - 4 / 2}`, `{"a": 7}`, 1},
		{"negative literal untouched", `{"a": -4430456000, "b": 0}`, `{"a": -4430456000, "b": 0}`, 0},
		{"paren negative", `{"a": 14016038969 + (-4430456000)}`, `{"a": 9585582969}`, 1},
		{"array elements", `[1 + 1, 2, 3 * 3]`, `[2, 2, 9]`, 2},
		{"nested", `{"x": {"y": [10 - 3, {"z": 2 ^ 10}]}}`, `{"x": {"y": [7, {"z": 1024}]}}`, 2},
		{"decimal result", `{"a": 1 / 4, "b": 0.1 + 0.2}`, `{"a": 0.25, "b": 0.3}`, 2},
		{"non terminating is rounded", `{"a": 1 / 3}`, `{"a": 0.3333333333}`, 1},
		{"beyond float64", `{"a": 9007199254740993 + 2}`, `{"a": 9007199254740995}`, 1},
		{"multiline expression keeps layout", "{\n  \"a\": 1 +\n 2,\n  \"b\": 5\n}", "{\n  \"a\": 3,\n  \"b\": 5\n}", 1},
		{"leading zero normalised", `{"a": 007}`, `{"a": 7}`, 1},
		{"strings never touched", `{"note": "1 + 2 = 3", "k 1 + 1": "v"}`, `{"note": "1 + 2 = 3", "k 1 + 1": "v"}`, 0},
		{"escaped quote in string", `{"s": "a\" + 1", "n": 1 + 1}`, `{"s": "a\" + 1", "n": 2}`, 1},
		{"thousand separators left alone", `{"a": 1.547.961}`, `{"a": 1.547.961}`, 0},
		{"unevaluable left alone", `{"a": 12%, "b": 1 + 1}`, `{"a": 12%, "b": 2}`, 1},
		{"booleans and null", `{"a": true, "b": null, "c": [false]}`, `{"a": true, "b": null, "c": [false]}`, 0},
		{"already valid", `{"a": 1.5e3, "b": [1, 2, 3]}`, `{"a": 1.5e3, "b": [1, 2, 3]}`, 0},
	}
	for _, c := range cases {
		got, n := repairJSONExpressions(c.in)
		if got != c.want || n != c.n {
			t.Errorf("%s:\n got  %q (n=%d)\n want %q (n=%d)", c.name, got, n, c.want, c.n)
		}
	}

	fixed, _ := repairJSONExpressions(reportedBad)
	if !json.Valid([]byte(fixed)) {
		t.Fatalf("reported case must become valid JSON:\n%s", fixed)
	}
	var doc struct{ Expected, Actual int64 }
	if err := json.Unmarshal([]byte(fixed), &doc); err != nil || doc.Expected != 29207537414 || doc.Expected != doc.Actual {
		t.Errorf("expected must equal the true sum 29207537414: %+v (%v)", doc, err)
	}
}

// feedAll streams text through a guard in chunks of the given size.
func feedAll(text string, size int) (string, int) {
	g := &textGuard{}
	var out strings.Builder
	for i := 0; i < len(text); i += size {
		out.WriteString(g.feed(text[i:min(i+size, len(text))]))
	}
	out.WriteString(g.flush())
	return out.String(), g.fixes
}

func TestTextGuard(t *testing.T) {
	fixedDoc := strings.Replace(reportedBad, "-39292632903 + 68499552588 + 617729", "29207537414", 1)
	cases := []struct {
		name, in, want string
		fixes          int
	}{
		{"fenced json", "Kết quả:\n```json\n" + reportedBad + "\n```\nXong.", "Kết quả:\n```json\n" + fixedDoc + "\n```\nXong.", 1},
		{"fenced without language", "```\n[1 + 1]\n```\n", "```\n[2]\n```\n", 1},
		{"raw object at start", reportedBad, fixedDoc, 1},
		{"raw array of objects", "Đây:\n[\n  {\"a\": 1 + 1},\n  {\"a\": 2 + 2}\n]\nHết", "Đây:\n[\n  {\"a\": 2},\n  {\"a\": 4}\n]\nHết", 2},
		{"unterminated fence is released", "```json\n{\"a\": 1 + 1", "```json\n{\"a\": 2", 1},
		// Prose and other code must never be rewritten.
		{"prose with arithmetic", "Ví dụ: 5 + 3 = 8\nTổng: 2 * 4\n", "Ví dụ: 5 + 3 = 8\nTổng: 2 * 4\n", 0},
		{"other fence untouched", "```python\nx = {\"a\": 1 + 2}\n{\"b\": 1 + 1}\n```\n", "```python\nx = {\"a\": 1 + 2}\n{\"b\": 1 + 1}\n```\n", 0},
		{"markdown link is not json", "[docs](http://x) and [1 + 2]\n", "[docs](http://x) and [1 + 2]\n", 0},
		{"plain fence without json body", "```\nkey: 1 + 2\n```\n", "```\nkey: 1 + 2\n```\n", 0},
		{"valid json is byte-identical", "```json\n{\n  \"a\": 1,\n  \"b\": [1, 2]\n}\n```", "```json\n{\n  \"a\": 1,\n  \"b\": [1, 2]\n}\n```", 0},
		{"json after a python fence", "```python\nx = 1\n```\n```json\n{\"a\": 1 + 1}\n```\n", "```python\nx = 1\n```\n```json\n{\"a\": 2}\n```\n", 1},
		{"empty", "", "", 0},
	}
	for _, c := range cases {
		// The result must not depend on how the stream happened to be chunked.
		for _, size := range []int{1, 2, 3, 7, 64, len(c.in) + 1} {
			got, fixes := feedAll(c.in, size)
			if got != c.want || fixes != c.fixes {
				t.Errorf("%s (chunk=%d):\n got  %q (fixes=%d)\n want %q (fixes=%d)", c.name, size, got, fixes, c.want, c.fixes)
				break
			}
		}
	}
}

func TestTextGuardStreamsProseImmediately(t *testing.T) {
	g := &textGuard{}
	// Mid-line prose is released as it arrives, not held back for the newline.
	if got := g.feed("Xin chào, "); got != "Xin chào, " {
		t.Fatalf("prose was held: %q", got)
	}
	if got := g.feed("bạn khỏe không"); got != "bạn khỏe không" {
		t.Fatalf("prose was held: %q", got)
	}
	// A line that may open a JSON region is held until it is decided.
	g.feed("\n")
	if got := g.feed("```js"); got != "" {
		t.Fatalf("possible fence should be held, got %q", got)
	}
}

// TestAssemblerRepairsJSONInStreamAndStorage drives the assembler the way a
// model stream does and checks that the SSE deltas and the persisted block are
// the same repaired, valid JSON.
func TestAssemblerRepairsJSONInStreamAndStorage(t *testing.T) {
	answer := "```json\n" + `[
  {"check": "cash", "passed": true, "expected": -39292632903 + 68499552588 + 617729, "actual": 29207537414, "difference": 0},
  {"check": "assets", "passed": true, "expected": 658558466616 + 33142036070, "actual": 691700502686, "difference": 0}
]` + "\n```\n"

	var evs []events.Event
	a := newAssembler(func(e events.Event) { evs = append(evs, e) })
	for i := 0; i < len(answer); i += 5 {
		a.modelChunk(&schema.Message{Role: schema.Assistant, Content: answer[i:min(i+5, len(answer))]})
	}
	a.modelCallDone("stop", nil)
	a.finish("end_turn")

	var streamed strings.Builder
	sawStopAfterText := false
	for _, e := range evs {
		switch e.Kind {
		case events.KindTextDelta:
			streamed.WriteString(e.Text)
			if sawStopAfterText {
				t.Fatal("text delta arrived after content_block_stop")
			}
		case events.KindContentBlockStop:
			sawStopAfterText = true
		}
	}
	blocks, _ := a.result("end_turn")
	if len(blocks) != 1 || blocks[0].Text != streamed.String() {
		t.Fatalf("persisted text must equal what was streamed:\n stored   %q\n streamed %q", blocks[0].Text, streamed.String())
	}
	body := extractJSON(streamed.String())
	var rows []struct {
		Expected, Actual, Difference int64
		Passed                       bool
	}
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("streamed answer is not valid JSON: %v\n%s", err, body)
	}
	if rows[0].Expected != 29207537414 || rows[1].Expected != 691700502686 {
		t.Errorf("sums not evaluated: %+v", rows)
	}
	if a.fixedJSONValues() != 2 {
		t.Errorf("fixedJSONValues = %d, want 2", a.fixedJSONValues())
	}
}
