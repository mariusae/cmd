package main

// Math notes: a note whose first line is "math" works out every line that
// ends in "=", and writes the answer after the "=". The words around the
// numbers stay, so a line can say what the number was for; they are not
// part of the sum. "name : value" names a value for the lines below it,
// "ans" is the nearest answer above, and "10 ft to m" converts.

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// mathAnswers works out a math note's lines (the keyword line included,
// and skipped): for each line with an answer slot after its last "=", the
// line as it should read, with the answer in the slot. A slot that holds
// something other than an answer is the writer's, and its line is left
// alone.
func mathAnswers(lines []string) map[int]string {
	out := map[int]string{}
	vars := map[string]float64{}
	ans, haveAns := 0.0, false
	for i, line := range lines {
		if i == 0 || isComment(line) {
			continue
		}
		body, slot, hasSlot := splitSlot(line)
		if hasSlot && !answerLike(slot) {
			continue
		}
		name, expr, assigns := splitAssignment(body)
		if !assigns {
			expr = body
		}
		if !hasSlot && !assigns {
			continue
		}
		v, unit, ok := evalLine(expr, vars, ans, haveAns)
		if assigns && ok {
			vars[strings.ToLower(strings.TrimSpace(name))] = v
		}
		if !hasSlot {
			continue
		}
		answer := ""
		if ok {
			answer = formatNumber(v)
			if unit != "" {
				answer += " " + unit
			}
			ans, haveAns = v, true
		}
		want := strings.TrimRight(body, " \t") + " ="
		if answer != "" {
			want += " " + answer
		}
		if want != line {
			out[i] = want
		}
	}
	return out
}

// isComment says a line is the writer's alone: "//" begins a comment, and
// "#" a heading.
func isComment(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#")
}

// splitSlot splits a line at its last "=": what is worked out, and the
// answer slot after it.
func splitSlot(line string) (body, slot string, ok bool) {
	i := strings.LastIndex(line, "=")
	if i < 0 {
		return line, "", false
	}
	return line[:i], line[i+1:], true
}

// answerLike says a slot holds nothing, or what an answer would: a number,
// perhaps with a unit after it. Anything else in it was written there.
var answerRE = regexp.MustCompile(`^\s*(-?[0-9][0-9,]*(\.[0-9]+)?(e[+-]?[0-9]+)?(\s*[^\s0-9][^0-9]*)?)?\s*$`)

func answerLike(slot string) bool { return answerRE.MatchString(slot) }

// splitAssignment splits "name : value", where the name has a letter in
// it: a time (9:30) or a ratio (16:9) is not a name.
func splitAssignment(s string) (name, value string, ok bool) {
	i := strings.Index(s, ":")
	if i < 0 {
		return "", "", false
	}
	name, value = strings.TrimSpace(s[:i]), s[i+1:]
	if name == "" || strings.TrimSpace(value) == "" || !strings.ContainsFunc(name, unicode.IsLetter) {
		return "", "", false
	}
	return name, value, true
}

// evalLine works out an expression or a conversion, with the note's names
// and its last answer in it. A conversion's answer carries its unit.
func evalLine(expr string, vars map[string]float64, ans float64, haveAns bool) (float64, string, bool) {
	expr = substitute(expr, vars, ans, haveAns)
	if v, unit, ok := convert(expr); ok {
		return v, unit, true
	}
	v, ok := evalExpr(expr)
	return v, "", ok
}

// substitute puts the names' values in for the names, longest name first
// so that "rent total" is not read as "rent" and a word.
func substitute(expr string, vars map[string]float64, ans float64, haveAns bool) string {
	names := make([]string, 0, len(vars)+1)
	values := map[string]float64{}
	for n, v := range vars {
		names = append(names, n)
		values[n] = v
	}
	if _, named := vars["ans"]; haveAns && !named {
		names = append(names, "ans")
		values["ans"] = ans
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	for _, n := range names {
		re := regexp.MustCompile(`(?i)(^|[^\pL\pN_])` + regexp.QuoteMeta(n) + `($|[^\pL\pN_])`)
		value := "(" + strconv.FormatFloat(values[n], 'g', -1, 64) + ")"
		// twice, since a match takes the character on either side and so
		// two names a space apart would hide the second
		for k := 0; k < 2; k++ {
			expr = re.ReplaceAllString(expr, "${1}"+value+"${2}")
		}
	}
	return expr
}

// formatNumber writes an answer: a whole number as one, anything else to
// two places, with thousands separated.
func formatNumber(v float64) string {
	if math.Abs(v) >= 1e15 {
		return strconv.FormatFloat(v, 'g', 6, 64)
	}
	r := math.Round(v*100) / 100
	s := strconv.FormatFloat(r, 'f', 2, 64)
	if r == math.Trunc(r) {
		s = strconv.FormatFloat(r, 'f', 0, 64)
	}
	if s == "-0" {
		s = "0"
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String()
	if frac != "" {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

// parseNumber reads a number as people write one: "1,234.5", and "1.234,5"
// and "3,5" where the comma is the decimal point.
func parseNumber(s string) (float64, bool) {
	s = strings.Trim(s, ".,")
	if s == "" {
		return 0, false
	}
	comma, dot := strings.LastIndex(s, ","), strings.LastIndex(s, ".")
	switch {
	case comma >= 0 && dot >= 0 && comma > dot:
		s = strings.ReplaceAll(s, ".", "")
		s = strings.Replace(s, ",", ".", 1)
	case comma >= 0 && dot >= 0:
		s = strings.ReplaceAll(s, ",", "")
	case comma >= 0:
		groups := strings.Split(s, ",")
		thousands := true
		for _, g := range groups[1:] {
			if len(g) != 3 {
				thousands = false
			}
		}
		if thousands {
			s = strings.ReplaceAll(s, ",", "")
		} else if len(groups) == 2 {
			s = groups[0] + "." + groups[1]
		} else {
			return 0, false
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

// ---- expressions ------------------------------------------------------------

type token struct {
	kind  byte // 'n' a number, 'o' an operator, 'f' a function
	num   float64
	text  string
	space bool // a space before it
}

var functions = map[string]func(float64) float64{
	"sqrt":  math.Sqrt,
	"cbrt":  math.Cbrt,
	"log":   math.Log10,
	"log10": math.Log10,
	"log2":  math.Log2,
	"ln":    math.Log,
	"exp":   math.Exp,
	"abs":   math.Abs,
	"ceil":  math.Ceil,
	"floor": math.Floor,
	"round": math.Round,
	"sin":   math.Sin,
	"cos":   math.Cos,
	"tan":   math.Tan,
}

// tokenize reads an expression, leaving out what is not arithmetic: words,
// currency signs, punctuation.
func tokenize(s string) []token {
	var out []token
	rs := []rune(s)
	space := false
	for i := 0; i < len(rs); {
		c := rs[i]
		switch {
		case unicode.IsSpace(c):
			space = true
			i++
			continue
		case unicode.IsDigit(c) || c == '.' && i+1 < len(rs) && unicode.IsDigit(rs[i+1]):
			j := i
			for j < len(rs) && (unicode.IsDigit(rs[j]) || rs[j] == '.' || rs[j] == ',' && j+1 < len(rs) && unicode.IsDigit(rs[j+1])) {
				j++
			}
			if v, ok := parseNumber(string(rs[i:j])); ok {
				out = append(out, token{kind: 'n', num: v, space: space})
			}
			i = j
		case unicode.IsLetter(c):
			j := i
			for j < len(rs) && (unicode.IsLetter(rs[j]) || unicode.IsDigit(rs[j])) {
				j++
			}
			w := strings.ToLower(string(rs[i:j]))
			switch {
			case functions[w] != nil:
				out = append(out, token{kind: 'f', text: w, space: space})
			case w == "x":
				out = append(out, token{kind: 'o', text: "*", space: space})
			case w == "of":
				out = append(out, token{kind: 'o', text: "of", space: space})
			case w == "pi":
				out = append(out, token{kind: 'n', num: math.Pi, space: space})
			}
			i = j
		default:
			op := ""
			switch c {
			case '+', '-', '/', '^', '(', ')', '%', '√', '∛':
				op = string(c)
			case '−':
				op = "-"
			case '*', '×', '·':
				op = "*"
				if c == '*' && i+1 < len(rs) && rs[i+1] == '*' {
					op = "^"
					i++
				}
			case '÷':
				op = "/"
			case '!':
				op = "!"
				if i+1 < len(rs) && rs[i+1] == '!' {
					op = "!!"
					i++
				}
			}
			if op != "" {
				out = append(out, token{kind: 'o', text: op, space: space})
			}
			i++
		}
		space = false
	}
	return out
}

// A value, and whether it is a percentage: "100 + 15%" is 115.
type value struct {
	v   float64
	pct bool
}

type parser struct {
	toks []token
	i    int
	bad  bool
}

func (p *parser) peek() token {
	if p.i < len(p.toks) {
		return p.toks[p.i]
	}
	return token{}
}

func (p *parser) op(text string) bool {
	if t := p.peek(); t.kind == 'o' && t.text == text {
		p.i++
		return true
	}
	return false
}

// evalExpr works out an arithmetic expression; false when there is none,
// or what is there does not add up to one.
func evalExpr(s string) (float64, bool) {
	p := &parser{toks: tokenize(s)}
	if len(p.toks) == 0 {
		return 0, false
	}
	v := p.sum()
	if p.bad || p.i != len(p.toks) || math.IsNaN(v.v) || math.IsInf(v.v, 0) {
		return 0, false
	}
	return v.v, true
}

func (p *parser) sum() value {
	l := p.product()
	for {
		switch {
		case p.op("+"):
			r := p.product()
			if r.pct {
				l = value{v: l.v * (1 + r.v)}
			} else {
				l = value{v: l.v + r.v}
			}
		case p.op("-"):
			r := p.product()
			if r.pct {
				l = value{v: l.v * (1 - r.v)}
			} else {
				l = value{v: l.v - r.v}
			}
		default:
			return l
		}
	}
}

func (p *parser) product() value {
	l := p.unary()
	for {
		switch {
		case p.op("*"), p.op("of"):
			r := p.unary()
			l = value{v: l.v * r.v}
		case p.op("/"):
			r := p.unary()
			l = value{v: l.v / r.v}
		default:
			return l
		}
	}
}

func (p *parser) unary() value {
	switch {
	case p.op("-"):
		v := p.unary()
		return value{v: -v.v, pct: v.pct}
	case p.op("+"):
		return p.unary()
	}
	return p.power()
}

func (p *parser) power() value {
	b := p.postfix()
	if p.op("^") {
		e := p.unary()
		return value{v: math.Pow(b.v, e.v)}
	}
	return b
}

func (p *parser) postfix() value {
	v := p.primary()
	for {
		switch {
		case p.op("%"):
			v = value{v: v.v / 100, pct: true}
		case p.op("!!"):
			v = value{v: factorial(v.v, 2)}
		case p.op("!"):
			v = value{v: factorial(v.v, 1)}
		default:
			return v
		}
	}
}

func (p *parser) primary() value {
	t := p.peek()
	switch {
	case t.kind == 'n':
		p.i++
		return value{v: t.num}
	case t.kind == 'f':
		p.i++
		arg := p.postfix()
		return value{v: functions[t.text](arg.v)}
	case p.op("("):
		v := p.sum()
		if !p.op(")") {
			p.bad = true
		}
		return v
	case p.op("√"):
		return value{v: math.Sqrt(p.postfix().v)}
	case p.op("∛"):
		return value{v: math.Cbrt(p.postfix().v)}
	}
	p.bad = true
	return value{}
}

// factorial is n!, or with step 2 the double factorial n!!.
func factorial(n float64, step int) float64 {
	if n < 0 || n != math.Trunc(n) || n > 170 {
		return math.NaN()
	}
	r := 1.0
	for k := int(n); k > 1; k -= step {
		r *= float64(k)
	}
	return r
}
