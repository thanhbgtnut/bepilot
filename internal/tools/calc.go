package tools

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"unicode"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// --- calculate --------------------------------------------------------------

const (
	maxExpressions = 200
	maxExprLen     = 2000
	maxExponent    = 1000
	// defaultPrecision is how many decimals a non-terminating result (1/3) is
	// rounded to.
	defaultPrecision = 10
)

type calculateArgs struct {
	Expressions []string `json:"expressions" jsonschema:"required" jsonschema_description:"Arithmetic expressions to evaluate, one result each, in order. Batch every calculation you need into a single call. Operators: + - * / % (remainder) ^ (power, integer exponent) and parentheses; functions: abs, round(x[,digits]), floor, ceil, min, max, sum, avg. Write plain numbers only: no thousand separators (write 1547961374470, not 1.547.961.374.470 or 1,547,961), decimal point '.', exponent like 1.5e3."`
	Precision   int      `json:"precision" jsonschema_description:"Decimals to round a non-terminating result to (e.g. 1/3). Default 10. Terminating results are always exact."`
}

type calcResult struct {
	Expression string `json:"expression"`
	Result     string `json:"result,omitempty"`
	// Exact is false only when Result was rounded to Precision decimals.
	Exact bool   `json:"exact,omitempty"`
	Error string `json:"error,omitempty"`
}

type calculateOutput struct {
	Results []calcResult `json:"results"`
}

func newCalculateTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"calculate",
		"Evaluate arithmetic exactly (arbitrary-size integers and decimals, no floating-point error). Use it for ANY sum, difference, product, ratio, percentage or comparison of numbers — never add or subtract multi-digit numbers yourself. To check that a = b + c, evaluate both `b + c` (the expected value) and `a - (b + c)` (the difference, sign as defined by the user) — not only the residual. Accepts many expressions per call.",
		func(_ context.Context, a calculateArgs) (calculateOutput, error) {
			if len(a.Expressions) == 0 {
				return calculateOutput{}, fmt.Errorf("expressions must not be empty")
			}
			if len(a.Expressions) > maxExpressions {
				return calculateOutput{}, fmt.Errorf("too many expressions (%d); the limit is %d per call", len(a.Expressions), maxExpressions)
			}
			prec := a.Precision
			if prec <= 0 {
				prec = defaultPrecision
			}
			if prec > 50 {
				prec = 50
			}
			out := calculateOutput{Results: make([]calcResult, 0, len(a.Expressions))}
			for _, src := range a.Expressions {
				res := calcResult{Expression: src}
				v, err := evalExpr(src)
				if err != nil {
					res.Error = err.Error()
				} else {
					res.Result, res.Exact = formatRat(v, prec)
				}
				out.Results = append(out.Results, res)
			}
			return out, nil
		},
	)
}

// EvalNumber evaluates an arithmetic expression exactly and renders the result
// as a plain decimal, rounded to defaultPrecision places only when the decimal
// expansion does not terminate. It is the same evaluator the calculate tool
// uses, exported so server-side output checks agree with it.
func EvalNumber(expr string) (string, error) {
	v, err := evalExpr(expr)
	if err != nil {
		return "", err
	}
	s, _ := formatRat(v, defaultPrecision)
	return s, nil
}

// formatRat renders r as a plain decimal string. It is exact when the decimal
// expansion terminates; otherwise it is rounded to prec places and exact is
// false.
func formatRat(r *big.Rat, prec int) (string, bool) {
	if r.IsInt() {
		return r.Num().String(), true
	}
	if terminates(r.Denom()) {
		// The denominator is 2^a * 5^b, so max(a, b) decimals are exact.
		return trimDecimal(r.FloatString(decimalsNeeded(r.Denom()))), true
	}
	return trimDecimal(r.FloatString(prec)), false
}

// decimalsNeeded returns max(a, b) for a denominator of the form 2^a * 5^b.
func decimalsNeeded(d *big.Int) int {
	x := new(big.Int).Set(d)
	count := func(p int64) int {
		bp, n := big.NewInt(p), 0
		for new(big.Int).Mod(x, bp).Sign() == 0 {
			x.Div(x, bp)
			n++
		}
		return n
	}
	a, b := count(2), count(5)
	return max(a, b)
}

func terminates(d *big.Int) bool {
	x := new(big.Int).Set(d)
	for _, p := range []int64{2, 5} {
		bp := big.NewInt(p)
		for new(big.Int).Mod(x, bp).Sign() == 0 {
			x.Div(x, bp)
		}
	}
	return x.Cmp(big.NewInt(1)) == 0
}

func trimDecimal(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "-0" || s == "" {
		return "0"
	}
	return s
}

// --- expression evaluator ---------------------------------------------------

// evalExpr parses and evaluates src with exact rational arithmetic.
func evalExpr(src string) (*big.Rat, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return nil, fmt.Errorf("empty expression")
	}
	if len(src) > maxExprLen {
		return nil, fmt.Errorf("expression longer than %d characters", maxExprLen)
	}
	p := &exprParser{src: []rune(src)}
	v, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos < len(p.src) {
		return nil, p.errorf("unexpected %q", string(p.src[p.pos]))
	}
	return v, nil
}

type exprParser struct {
	src []rune
	pos int
}

func (p *exprParser) errorf(format string, a ...any) error {
	return fmt.Errorf("%s (at position %d)", fmt.Sprintf(format, a...), p.pos+1)
}

func (p *exprParser) skipSpace() {
	for p.pos < len(p.src) && unicode.IsSpace(p.src[p.pos]) {
		p.pos++
	}
}

func (p *exprParser) peek() rune {
	p.skipSpace()
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

// expr = term (('+'|'-') term)*
func (p *exprParser) parseExpr() (*big.Rat, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek() {
		case '+':
			p.pos++
			right, err := p.parseTerm()
			if err != nil {
				return nil, err
			}
			left = new(big.Rat).Add(left, right)
		case '-':
			p.pos++
			right, err := p.parseTerm()
			if err != nil {
				return nil, err
			}
			left = new(big.Rat).Sub(left, right)
		default:
			return left, nil
		}
	}
}

// term = unary (('*'|'/'|'%') unary)*
func (p *exprParser) parseTerm() (*big.Rat, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		op := p.peek()
		if op != '*' && op != '/' && op != '%' {
			return left, nil
		}
		p.pos++
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		switch op {
		case '*':
			left = new(big.Rat).Mul(left, right)
		case '/':
			if right.Sign() == 0 {
				return nil, p.errorf("division by zero")
			}
			left = new(big.Rat).Quo(left, right)
		case '%':
			if right.Sign() == 0 {
				return nil, p.errorf("remainder by zero")
			}
			left = ratMod(left, right)
		}
	}
}

// ratMod is the remainder of a/b with the sign of a, like Go's % on integers.
func ratMod(a, b *big.Rat) *big.Rat {
	q := new(big.Rat).Quo(a, b)
	t := ratTrunc(q)
	return new(big.Rat).Sub(a, new(big.Rat).Mul(t, b))
}

func ratTrunc(r *big.Rat) *big.Rat {
	return new(big.Rat).SetInt(new(big.Int).Quo(r.Num(), r.Denom()))
}

// unary = ('-'|'+') unary | power
func (p *exprParser) parseUnary() (*big.Rat, error) {
	switch p.peek() {
	case '-':
		p.pos++
		v, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return new(big.Rat).Neg(v), nil
	case '+':
		p.pos++
		return p.parseUnary()
	}
	return p.parsePower()
}

// power = primary ('^' unary)?   (right-associative; -2^2 == -(2^2))
func (p *exprParser) parsePower() (*big.Rat, error) {
	base, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	if p.peek() != '^' {
		return base, nil
	}
	p.pos++
	exp, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	return p.pow(base, exp)
}

func (p *exprParser) pow(base, exp *big.Rat) (*big.Rat, error) {
	if !exp.IsInt() {
		return nil, p.errorf("only integer exponents are supported")
	}
	e := exp.Num()
	if e.CmpAbs(big.NewInt(maxExponent)) > 0 {
		return nil, p.errorf("exponent too large (limit %d)", maxExponent)
	}
	n := int(e.Int64())
	neg := n < 0
	if neg {
		n = -n
	}
	num := new(big.Int).Exp(base.Num(), big.NewInt(int64(n)), nil)
	den := new(big.Int).Exp(base.Denom(), big.NewInt(int64(n)), nil)
	if neg {
		if num.Sign() == 0 {
			return nil, p.errorf("zero to a negative power")
		}
		num, den = den, num
	}
	return new(big.Rat).SetFrac(num, den), nil
}

// primary = number | '(' expr ')' | ident '(' args ')'
func (p *exprParser) parsePrimary() (*big.Rat, error) {
	c := p.peek()
	switch {
	case c == 0:
		return nil, p.errorf("unexpected end of expression")
	case c == '(':
		p.pos++
		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.peek() != ')' {
			return nil, p.errorf("missing closing parenthesis")
		}
		p.pos++
		return v, nil
	case unicode.IsDigit(c) || c == '.':
		return p.parseNumber()
	case unicode.IsLetter(c):
		return p.parseCall()
	}
	return nil, p.errorf("unexpected %q", string(c))
}

func (p *exprParser) parseNumber() (*big.Rat, error) {
	start := p.pos
	for p.pos < len(p.src) && (unicode.IsDigit(p.src[p.pos]) || p.src[p.pos] == '.' || p.src[p.pos] == '_') {
		p.pos++
	}
	// Optional exponent: e/E, optional sign, digits.
	if p.pos < len(p.src) && (p.src[p.pos] == 'e' || p.src[p.pos] == 'E') {
		q := p.pos + 1
		if q < len(p.src) && (p.src[q] == '+' || p.src[q] == '-') {
			q++
		}
		if q < len(p.src) && unicode.IsDigit(p.src[q]) {
			for q < len(p.src) && unicode.IsDigit(p.src[q]) {
				q++
			}
			p.pos = q
		}
	}
	lit := strings.ReplaceAll(string(p.src[start:p.pos]), "_", "")
	if strings.Count(lit, ".") > 1 {
		p.pos = start
		return nil, p.errorf("%q looks like a number with thousand separators; write it without them (e.g. 1547961374470)", lit)
	}
	// A number followed directly by another digit group ("1 234") is a
	// thousand-separated number: refuse rather than silently misread it.
	p.skipSpace()
	if p.pos < len(p.src) && unicode.IsDigit(p.src[p.pos]) {
		return nil, p.errorf("two numbers in a row; write numbers without separators and use an operator between them")
	}
	if i := strings.IndexAny(lit, "eE"); i >= 0 {
		if e, ok := new(big.Int).SetString(strings.TrimPrefix(lit[i+1:], "+"), 10); !ok || e.CmpAbs(big.NewInt(maxExponent)) > 0 {
			return nil, p.errorf("exponent in %q is too large", lit)
		}
	}
	v, ok := new(big.Rat).SetString(lit)
	if !ok {
		return nil, p.errorf("invalid number %q", lit)
	}
	return v, nil
}

func (p *exprParser) parseCall() (*big.Rat, error) {
	start := p.pos
	for p.pos < len(p.src) && (unicode.IsLetter(p.src[p.pos]) || unicode.IsDigit(p.src[p.pos]) || p.src[p.pos] == '_') {
		p.pos++
	}
	name := strings.ToLower(string(p.src[start:p.pos]))
	if p.peek() != '(' {
		p.pos = start
		return nil, p.errorf("unknown name %q (variables are not supported; substitute the numbers)", name)
	}
	p.pos++
	var args []*big.Rat
	if p.peek() == ')' {
		p.pos++
	} else {
		for {
			v, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, v)
			c := p.peek()
			if c == ',' || c == ';' {
				p.pos++
				continue
			}
			if c == ')' {
				p.pos++
				break
			}
			return nil, p.errorf("expected ',' or ')' in call to %s", name)
		}
	}
	return p.callFunc(name, args)
}

func (p *exprParser) callFunc(name string, args []*big.Rat) (*big.Rat, error) {
	need := func(min, max int) error {
		if len(args) < min || (max >= 0 && len(args) > max) {
			return p.errorf("wrong number of arguments to %s", name)
		}
		return nil
	}
	switch name {
	case "abs":
		if err := need(1, 1); err != nil {
			return nil, err
		}
		return new(big.Rat).Abs(args[0]), nil
	case "floor", "ceil":
		if err := need(1, 1); err != nil {
			return nil, err
		}
		fl := ratFloor(args[0])
		if name == "ceil" && !args[0].IsInt() {
			fl = new(big.Rat).Add(fl, big.NewRat(1, 1))
		}
		return fl, nil
	case "round":
		if err := need(1, 2); err != nil {
			return nil, err
		}
		digits := 0
		if len(args) == 2 {
			if !args[1].IsInt() || args[1].Num().CmpAbs(big.NewInt(50)) > 0 {
				return nil, p.errorf("round digits must be an integer between -50 and 50")
			}
			digits = int(args[1].Num().Int64())
		}
		return ratRound(args[0], digits), nil
	case "min", "max":
		if err := need(1, -1); err != nil {
			return nil, err
		}
		best := args[0]
		for _, a := range args[1:] {
			if (name == "min" && a.Cmp(best) < 0) || (name == "max" && a.Cmp(best) > 0) {
				best = a
			}
		}
		return best, nil
	case "sum", "avg":
		if err := need(1, -1); err != nil {
			return nil, err
		}
		total := new(big.Rat)
		for _, a := range args {
			total.Add(total, a)
		}
		if name == "avg" {
			total.Quo(total, big.NewRat(int64(len(args)), 1))
		}
		return total, nil
	}
	return nil, p.errorf("unknown function %q", name)
}

func ratFloor(r *big.Rat) *big.Rat {
	// Euclidean division floors when the divisor is positive, as a Rat's is.
	return new(big.Rat).SetInt(new(big.Int).Div(r.Num(), r.Denom()))
}

// ratRound rounds half away from zero to the given number of decimal digits
// (negative digits round to tens, hundreds, ...).
func ratRound(r *big.Rat, digits int) *big.Rat {
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs(digits))), nil))
	x := new(big.Rat).Set(r)
	if digits >= 0 {
		x.Mul(x, scale)
	} else {
		x.Quo(x, scale)
	}
	half := big.NewRat(1, 2)
	if x.Sign() < 0 {
		x.Sub(x, half)
	} else {
		x.Add(x, half)
	}
	x = ratTrunc(x)
	if digits >= 0 {
		return x.Quo(x, scale)
	}
	return x.Mul(x, scale)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
