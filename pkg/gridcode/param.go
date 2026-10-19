package gridcode

import (
	"fmt"
	"math"
	"math/bits"
	"strings"
)

// Kind is how a grid parameter is shown and edited.
type Kind uint8

const (
	// Number is a scaled numeric field.
	Number Kind = iota + 1
	// Checkbox is an on/off field; its display value is 1 when ticked,
	// 0 when not.
	Checkbox
	// Choice is a field holding one of a few named values; its display
	// value is the field value.
	Choice
)

// Page is a grid parameter page.
type Page struct {
	// Name is this project's own name of the page.
	Name   string
	Params []Param
	// Unsupported names parameters of the page that are left out of Params
	// because how they map to the words is not known (values spread over
	// several words, negative values in a field without a sign bit, a
	// control bound to no word).
	Unsupported []string
}

// Pages returns all known parameter pages, including those PageFor maps
// to no grid code (both Germany variants, Microgrid: when the original
// shows them is not known).
func Pages() []*Page {
	return []*Page{
		pageUK, pageItaly, pageGermanyA, pageGermanyB, pageAUNZ2015,
		pageAUNZ2020, pageSouthAfrica, pageFrance, pageSpain, pageBelgium,
		pageAustria, pageChile, pageRomania, pageEurope, pageAUNZM,
		pagePoland, pageMicrogrid,
	}
}

// Param is one grid parameter: a bit field of one grid word.
//
// A field is extracted as (word AND Mask) shifted right by the position of
// the mask's lowest bit, then read as two's complement if Signed. A
// number's display value is Scale * (field + Offset), or 1 / (Scale *
// (field + Offset)) if Reciprocal. Reverse-engineered; the shift is
// INFERRED (the limits make sense only with it).
type Param struct {
	// Name is this project's own name of the parameter.
	Name string
	// Group is the sub-page or group within the page; "" for the page
	// itself.
	Group string
	Kind  Kind
	// Word is the setting ID (129-190) that holds the field; Mask selects
	// the field's bits, which are contiguous.
	Word uint16
	Mask uint16
	// Signed marks a two's complement field; its sign bit is the field's
	// top bit.
	Signed bool

	// Number only.
	Scale, Offset float64
	Reciprocal    bool
	Unit          string
	// Min and Max are the field values of the displayed minimum and
	// maximum. With a negative Scale or Reciprocal, Min > Max.
	Min, Max int

	// Default is the field value of the default, if HasDefault. A default
	// the original gives outside the parameter's own range is treated as
	// unreliable and left out.
	Default    int
	HasDefault bool

	// Checkbox only: Inverted means ticked when the field is 0.
	Inverted bool
	// Choice only: the allowed field values and this project's names for
	// them.
	Choices map[int]string
}

func (p Param) shift() uint { return uint(bits.TrailingZeros16(p.Mask)) }
func (p Param) width() uint { return uint(bits.OnesCount16(p.Mask)) }

// Field extracts p's field from word.
func (p Param) Field(word uint16) int {
	f := int(word&p.Mask) >> p.shift()
	if p.Signed && f >= 1<<(p.width()-1) {
		f -= 1 << p.width()
	}
	return f
}

// Value converts a field value to p's display value.
func (p Param) Value(field int) float64 {
	switch p.Kind {
	case Number:
		v := p.Scale * (float64(field) + p.Offset)
		if p.Reciprocal {
			return 1 / v
		}
		return v
	case Checkbox:
		if (field != 0) != p.Inverted {
			return 1
		}
		return 0
	}
	return float64(field)
}

// Unpack returns p's display value from word, the current value of
// setting p.Word.
func (p Param) Unpack(word uint16) float64 { return p.Value(p.Field(word)) }

// Pack returns word with p's field set to display value v; the other bits
// of word are unchanged. A number is rounded to the nearest field value
// and must lie within p's limits; a checkbox takes 0 or 1; a choice takes
// one of its Choices. On error word is returned unchanged.
func (p Param) Pack(word uint16, v float64) (uint16, error) {
	f, err := p.field(v)
	if err != nil {
		return word, err
	}
	return word&^p.Mask | uint16(f)<<p.shift()&p.Mask, nil
}

func (p Param) field(v float64) (int, error) {
	switch p.Kind {
	case Checkbox:
		if v != 0 && v != 1 {
			return 0, fmt.Errorf("gridcode: %s: %g is not 0 or 1", p.Name, v)
		}
		if (v == 1) != p.Inverted {
			return int(p.Mask >> p.shift()), nil
		}
		return 0, nil
	case Choice:
		if _, ok := p.Choices[int(v)]; !ok || float64(int(v)) != v {
			return 0, fmt.Errorf("gridcode: %s: %g is not one of its choices", p.Name, v)
		}
		return int(v), nil
	}
	x := v
	if p.Reciprocal {
		x = 1 / v
	}
	x = math.Round(x/p.Scale - p.Offset)
	lo, hi := min(p.Min, p.Max), max(p.Min, p.Max)
	if !(x >= float64(lo) && x <= float64(hi)) { // also catches NaN
		a, b := p.Value(p.Min), p.Value(p.Max)
		return 0, fmt.Errorf("gridcode: %s: %g is outside %s",
			p.Name, v, strings.TrimSpace(fmt.Sprintf("%g to %g %s", min(a, b), max(a, b), p.Unit)))
	}
	return int(x), nil
}
