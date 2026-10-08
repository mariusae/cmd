package main

import (
	"strings"
	"testing"
)

func TestExpressions(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
	}{
		{"2 + 2", "4"},
		{"rent 1,200 + utilities 300", "1,500"},
		{"$10 * 3 people", "30"},
		{"3 x 4", "12"},
		{"7 ÷ 2", "3.50"},
		{"2^10", "1,024"},
		{"2 ** 3 ** 2", "512"},
		{"(1 + 2) * 3", "9"},
		{"-3 + 5", "2"},
		{"100 + 15%", "115"},
		{"100 - 10%", "90"},
		{"50% of 200", "100"},
		{"200 * 5%", "10"},
		{"5!", "120"},
		{"5!!", "15"},
		{"√16", "4"},
		{"∛27", "3"},
		{"sqrt(16) + 1", "5"},
		{"log(1000)", "3"},
		{"log2(8)", "3"},
		{"ceil(12.256)", "13"},
		{"floor(12.256)", "12"},
		{"1.000,25 + 1", "1,001.25"},
		{"3,5 * 2", "7"},
		{"1/3", "0.33"},
	} {
		v, ok := evalExpr(c.in)
		if !ok {
			t.Errorf("%q: no answer", c.in)
			continue
		}
		if got := formatNumber(v); got != c.want {
			t.Errorf("%q = %s, want %s", c.in, got, c.want)
		}
	}
	for _, in := range []string{"", "words only", "2 +", "(2", "3 4", "1/0", "(-1)!"} {
		if v, ok := evalExpr(in); ok {
			t.Errorf("%q: %v, want no answer", in, v)
		}
	}
}

func TestConversions(t *testing.T) {
	for _, c := range []struct {
		in, want string
	}{
		{`10" to cm`, "25.40 cm"},
		{"5 km to miles", "3.11 miles"},
		{"6 ft in m", "1.83 m"},
		{"10 in to cm", "25.40 cm"},
		{"3 cups in ml", "709.76 ml"},
		{"2 * 3 ft to in", "72 in"},
		{"72 F to C", "22.22 C"},
		{"100 °C to °F", "212 °F"},
		{"0 C to K", "273.15 K"},
		{"1 acre to sq m", "4,046.86 sq m"},
		{"90 min to hours", "1.50 hours"},
		{"1 lb to g", "453.59 g"},
	} {
		v, unit, ok := convert(c.in)
		if !ok {
			t.Errorf("%q: no conversion", c.in)
			continue
		}
		if got := formatNumber(v) + " " + unit; got != c.want {
			t.Errorf("%q = %s, want %s", c.in, got, c.want)
		}
	}
	for _, in := range []string{"5 kg to m", "go to the shop", "10 to cm"} {
		if v, u, ok := convert(in); ok {
			t.Errorf("%q: %v %s, want no conversion", in, v, u)
		}
	}
}

func TestMathNote(t *testing.T) {
	note := strings.Join([]string{
		"math: Party",
		"number of guests : 9",
		"number of guests + 1 =",
		"pizzas: number of guests / 3 =",
		"pizzas * 12.50 =",
		"ans + 5 =",
		"// 1 + 1 =",
		"table : 58 cm to in = 1",
		"table * 2 =",
		"a sum written by hand = forty",
		"nonsense words =",
		"2 + 2 = 5",
		"9:30 meeting",
	}, "\n")
	got := mathAnswers(strings.Split(note, "\n"))
	want := map[int]string{
		2:  "number of guests + 1 = 10",
		3:  "pizzas: number of guests / 3 = 3",
		4:  "pizzas * 12.50 = 37.50",
		5:  "ans + 5 = 42.50",
		7:  "table : 58 cm to in = 22.83 in",
		8:  "table * 2 = 45.67",
		11: "2 + 2 = 4",
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("line %d: %q, want %q", i, got[i], w)
		}
	}
	for i, g := range got {
		if _, ok := want[i]; !ok {
			t.Errorf("line %d rewritten: %q", i, g)
		}
	}
}

func TestFormatNumber(t *testing.T) {
	for v, want := range map[float64]string{0: "0", -0.001: "0", 1234567: "1,234,567", -1234.5: "-1,234.50", 0.125: "0.13", 1e20: "1e+20"} {
		if got := formatNumber(v); got != want {
			t.Errorf("%v: %s, want %s", v, got, want)
		}
	}
}
