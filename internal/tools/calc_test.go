package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func runBuiltin(t *testing.T, name string, args any, out any) {
	t.Helper()
	r := newReg(t)
	for _, e := range r.Builtin() {
		if e.Name != name {
			continue
		}
		in, _ := json.Marshal(args)
		raw, err := e.tool.InvokableRun(context.Background(), string(in))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := json.Unmarshal([]byte(raw), out); err != nil {
			t.Fatalf("bad %s result %q: %v", name, raw, err)
		}
		return
	}
	t.Fatalf("built-in %s not registered", name)
}

func TestCalculate(t *testing.T) {
	cases := []struct {
		expr, want string
		exact      bool
	}{
		// The figures from a real balance-sheet check that the model used to
		// leave unevaluated.
		{"658558466616 + 33142036070", "691700502686", true},
		{"525990687369 + 165709815317", "691700502686", true},
		{"151339225025 + 3042642417 - 29764305468 - 49856366672 - 60745156333", "14016038969", true},
		{"1182052553 - 5612508553", "-4430456000", true},
		{"14016038969 + (-4430456000)", "9585582969", true},
		{"9585582969 - 1917116594 - 0", "7668466375", true},
		{"1386477364 + 26510634750 + 0", "27897112114", true},
		{"1547961374470 - 1396622149445", "151339225025", true},
		// Beyond float64's 2^53 integer range.
		{"9007199254740993 + 2", "9007199254740995", true},
		{"0.1 + 0.2", "0.3", true},
		{"1/4", "0.25", true},
		{"1/3", "0.3333333333", false},
		{"2^10", "1024", true},
		{"2^-2", "0.25", true},
		{"-2^2", "-4", true},
		{"2^3^2", "512", true},
		{"7 % 3", "1", true},
		{"-7 % 3", "-1", true},
		{"(1+2)*3-4/2", "7", true},
		{"1.5e3 + 1", "1501", true},
		{"round(2.5)", "3", true},
		{"round(-2.5)", "-3", true},
		{"round(1234.5678, 2)", "1234.57", true},
		{"round(1234, -2)", "1200", true},
		{"floor(-1.5)", "-2", true},
		{"ceil(-1.5)", "-1", true},
		{"ceil(1.2)", "2", true},
		{"max(1, 5, 3) - min(4, 2)", "3", true},
		{"sum(1,2,3,4)", "10", true},
		{"avg(1,2)", "1.5", true},
		{"abs(-3.25)", "3.25", true},
		{"1 - 1", "0", true},
		{"1.0 - 1.5 + 0.5", "0", true},
		{"12.5 / 100 * 800", "100", true},
	}
	exprs := make([]string, len(cases))
	for i, c := range cases {
		exprs[i] = c.expr
	}
	var out calculateOutput
	runBuiltin(t, "calculate", map[string]any{"expressions": exprs}, &out)
	if len(out.Results) != len(cases) {
		t.Fatalf("got %d results, want %d", len(out.Results), len(cases))
	}
	for i, c := range cases {
		got := out.Results[i]
		if got.Error != "" || got.Result != c.want || got.Exact != c.exact {
			t.Errorf("%q = %+v, want %s (exact=%v)", c.expr, got, c.want, c.exact)
		}
	}
}

func TestCalculateErrorsDoNotAbortBatch(t *testing.T) {
	cases := []struct{ expr, wantErr string }{
		{"1/0", "division by zero"},
		{"5 % 0", "remainder by zero"},
		{"1.547.961", "thousand separators"},
		{"1 234", "two numbers in a row"},
		{"(1+2", "closing parenthesis"},
		{"1 +", "end of expression"},
		{"x + 1", "unknown name"},
		{"foo(1)", "unknown function"},
		{"2^0.5", "integer exponents"},
		{"2^100000", "too large"},
		{"1e999999", "too large"},
		{"0^-1", "negative power"},
		{"", "empty"},
		{"1 2 3", "two numbers in a row"},
		{"1 + * 2", "unexpected"},
		{"3 )", "unexpected"},
	}
	exprs := []string{"1+1"}
	for _, c := range cases {
		exprs = append(exprs, c.expr)
	}
	exprs = append(exprs, "2*2")

	var out calculateOutput
	runBuiltin(t, "calculate", map[string]any{"expressions": exprs}, &out)
	if out.Results[0].Result != "2" || out.Results[len(exprs)-1].Result != "4" {
		t.Fatalf("valid expressions around failures must still evaluate: %+v", out.Results)
	}
	for i, c := range cases {
		got := out.Results[i+1]
		if got.Result != "" || !strings.Contains(got.Error, c.wantErr) {
			t.Errorf("%q: got %+v, want error containing %q", c.expr, got, c.wantErr)
		}
	}
}

func TestCalculateLimits(t *testing.T) {
	r := newReg(t)
	for _, e := range r.Builtin() {
		if e.Name != "calculate" {
			continue
		}
		if _, err := e.tool.InvokableRun(context.Background(), `{"expressions":[]}`); err == nil {
			t.Error("empty expressions should be rejected")
		}
		many, _ := json.Marshal(map[string]any{"expressions": make([]string, maxExpressions+1)})
		if _, err := e.tool.InvokableRun(context.Background(), string(many)); err == nil {
			t.Error("too many expressions should be rejected")
		}
	}
}

func TestJSONValidate(t *testing.T) {
	var out jsonValidateOutput

	runBuiltin(t, "json_validate", map[string]any{"text": `{"a": 1, "b": [true, null, "x"], "c": -4430456000}`}, &out)
	if !out.Valid || out.Type != "object" {
		t.Fatalf("valid document rejected: %+v", out)
	}

	// The exact mistake from the bug report: an expression where a value goes.
	bad := "[\n  {\n    \"passed\": true,\n    \"expected\": 658558466616 + 33142036070,\n    \"actual\": 691700502686\n  }\n]"
	out = jsonValidateOutput{}
	runBuiltin(t, "json_validate", map[string]any{"text": bad}, &out)
	if out.Valid || out.Line != 4 || !strings.Contains(out.Hint, "arithmetic expression") || !strings.Contains(out.Context, ">>>+<<<") {
		t.Fatalf("expression-in-value not diagnosed: %+v", out)
	}

	for name, text := range map[string]string{
		"trailing comma": `{"a": 1,}`,
		"single quotes":  `{'a': 1}`,
		"truncated":      `{"a": [1, 2`,
		"second value":   `{"a": 1} {"b": 2}`,
		"comment":        "{\n// note\n\"a\": 1}",
		"empty":          "  ",
		"thousands":      `{"a": 1.547.961}`,
	} {
		out = jsonValidateOutput{}
		runBuiltin(t, "json_validate", map[string]any{"text": text}, &out)
		if out.Valid || out.Error == "" {
			t.Errorf("%s: should be invalid: %+v", name, out)
		}
	}

	out = jsonValidateOutput{}
	runBuiltin(t, "json_validate", map[string]any{"text": `[1, 2]  `}, &out)
	if !out.Valid || out.Type != "array" {
		t.Errorf("trailing whitespace should be fine: %+v", out)
	}
}
