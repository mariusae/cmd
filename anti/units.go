package main

// Conversions in a math note: "10 ft to m", "3 cups in ml", "72 F to C".
// The amount may be worked out ("2 * 3 ft to m"); the answer carries the
// unit as it was asked for.

import (
	"sort"
	"strings"
	"unicode"
)

// A unit: what it measures, and how many of the measure's base unit it
// is. Temperatures are not a factor apart, and convert by name.
type unit struct {
	dim    string
	factor float64
}

var units = map[string]unit{}

// scales names each temperature unit's scale: c, f or k.
var scales = map[string]byte{}

// aliases, longest first, for reading a unit off the end of an amount.
var aliases []string

func init() {
	add := func(dim string, factor float64, names ...string) {
		for _, n := range names {
			units[n] = unit{dim, factor}
		}
	}
	add("length", 1, "m", "meter", "metre", "meters", "metres")
	add("length", 0.01, "cm", "centimeter", "centimetre", "centimeters", "centimetres")
	add("length", 0.001, "mm", "millimeter", "millimetre", "millimeters", "millimetres")
	add("length", 1000, "km", "kilometer", "kilometre", "kilometers", "kilometres")
	add("length", 0.0254, "in", "inch", "inches", `"`, "″")
	add("length", 0.3048, "ft", "foot", "feet", "'", "′")
	add("length", 0.9144, "yd", "yard", "yards")
	add("length", 1609.344, "mi", "mile", "miles")
	add("length", 1852, "nmi", "nautical mile", "nautical miles")

	add("area", 1, "sqm", "m²", "m2", "sq m", "square meter", "square meters", "square metre", "square metres")
	add("area", 1e-4, "sqcm", "cm²", "cm2", "sq cm", "square centimeter", "square centimeters")
	add("area", 1e-6, "sqmm", "mm²", "mm2", "sq mm", "square millimeter", "square millimeters")
	add("area", 1e6, "sqkm", "km²", "km2", "sq km", "square kilometer", "square kilometers")
	add("area", 1e4, "ha", "hectare", "hectares")
	add("area", 0.09290304, "sqft", "ft²", "ft2", "sq ft", "square foot", "square feet")
	add("area", 0.00064516, "sqin", "in²", "in2", "sq in", "square inch", "square inches")
	add("area", 0.83612736, "sqyd", "yd²", "yd2", "sq yd", "square yard", "square yards")
	add("area", 2589988.110336, "sqmi", "mi²", "mi2", "sq mi", "square mile", "square miles")
	add("area", 4046.8564224, "acre", "acres")

	add("volume", 1, "l", "liter", "litre", "liters", "litres")
	add("volume", 0.001, "ml", "milliliter", "millilitre", "milliliters", "millilitres")
	add("volume", 1000, "m³", "m3", "cubic meter", "cubic meters", "cubic metre", "cubic metres")
	add("volume", 0.001, "cm³", "cm3", "cc", "cubic centimeter", "cubic centimeters")
	add("volume", 3.785411784, "gal", "gallon", "gallons", "us gallon", "us gallons")
	add("volume", 0.946352946, "qt", "quart", "quarts")
	add("volume", 0.473176473, "pt", "pint", "pints")
	add("volume", 0.2365882365, "cup", "cups")
	add("volume", 0.0295735295625, "fl oz", "floz", "fluid ounce", "fluid ounces")
	add("volume", 0.01478676478125, "tbsp", "tablespoon", "tablespoons")
	add("volume", 0.00492892159375, "tsp", "teaspoon", "teaspoons")
	add("volume", 4.54609, "uk gal", "uk gallon", "uk gallons", "imperial gallon", "imperial gallons")
	add("volume", 0.56826125, "uk pt", "uk pint", "uk pints", "imperial pint", "imperial pints")
	add("volume", 0.016387064, "cu in", "in³", "cubic inch", "cubic inches")
	add("volume", 28.316846592, "cu ft", "ft³", "cubic foot", "cubic feet")

	add("mass", 1, "kg", "kgs", "kilogram", "kilograms")
	add("mass", 0.001, "g", "gram", "grams")
	add("mass", 1e-6, "mg", "milligram", "milligrams")
	add("mass", 1e-9, "µg", "ug", "mcg", "microgram", "micrograms")
	add("mass", 1000, "t", "tonne", "tonnes", "metric ton", "metric tons")
	add("mass", 0.45359237, "lb", "lbs", "pound", "pounds")
	add("mass", 0.028349523125, "oz", "ounce", "ounces")
	add("mass", 6.35029318, "st", "stone", "stones")
	add("mass", 907.18474, "ton", "tons", "short ton", "short tons", "us ton", "us tons")
	add("mass", 1016.0469088, "long ton", "long tons", "uk ton", "uk tons", "imperial ton", "imperial tons")

	add("time", 1, "s", "sec", "secs", "second", "seconds")
	add("time", 60, "min", "mins", "minute", "minutes")
	add("time", 3600, "h", "hr", "hrs", "hour", "hours")
	add("time", 86400, "day", "days")
	add("time", 604800, "week", "weeks")

	for scale, names := range map[byte][]string{
		'c': {"c", "°c", "celsius", "centigrade", "degrees c", "degrees celsius"},
		'f': {"f", "°f", "fahrenheit", "degrees f", "degrees fahrenheit"},
		'k': {"k", "kelvin", "kelvins", "degrees k"},
	} {
		add("temperature", 0, names...)
		for _, n := range names {
			scales[n] = scale
		}
	}

	for n := range units {
		aliases = append(aliases, n)
	}
	sort.Slice(aliases, func(i, j int) bool {
		if len(aliases[i]) != len(aliases[j]) {
			return len(aliases[i]) > len(aliases[j])
		}
		return aliases[i] < aliases[j]
	})
}

// convert reads "AMOUNT UNIT to UNIT" (or in, into, as) and converts.
func convert(expr string) (float64, string, bool) {
	low := strings.ToLower(expr)
	for _, kw := range []string{" to ", " into ", " in ", " as ", " -> ", " → "} {
		for at := strings.LastIndex(low, kw); at > 0; at = strings.LastIndex(low[:at], kw) {
			target := strings.TrimSpace(expr[at+len(kw):])
			to, ok := units[strings.ToLower(target)]
			if !ok {
				continue
			}
			amount, from, ok := trailingUnit(expr[:at])
			if !ok || units[from].dim != to.dim {
				continue
			}
			n, ok := evalExpr(amount)
			if !ok {
				continue
			}
			if to.dim == "temperature" {
				return temperature(n, from, strings.ToLower(target)), target, true
			}
			return n * units[from].factor / to.factor, target, true
		}
	}
	return 0, "", false
}

// trailingUnit splits "2 * 3 ft" into the amount and the unit it ends in,
// the longest name that fits: "sq ft" before "ft".
func trailingUnit(s string) (amount, name string, ok bool) {
	s = strings.TrimRightFunc(s, unicode.IsSpace)
	low := strings.ToLower(s)
	for _, a := range aliases {
		if !strings.HasSuffix(low, a) {
			continue
		}
		rest := s[:len(s)-len(a)]
		// the unit is a word of its own, but for the marks that sit on a
		// number: 10" and 5'
		if r := []rune(rest); len(r) > 0 {
			last := r[len(r)-1]
			attached := unicode.IsDigit(last) || last == ')'
			if !unicode.IsSpace(last) && !attached {
				continue
			}
		} else {
			continue
		}
		if strings.TrimSpace(rest) == "" {
			continue
		}
		return rest, a, true
	}
	return "", "", false
}

// temperature converts between the scales by their names.
func temperature(v float64, from, to string) float64 {
	c := v
	switch scales[from] {
	case 'f':
		c = (v - 32) * 5 / 9
	case 'k':
		c = v - 273.15
	}
	switch scales[to] {
	case 'f':
		return c*9/5 + 32
	case 'k':
		return c + 273.15
	}
	return c
}
