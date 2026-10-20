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
	Name string
	// Title, if not "", is the original's caption of the page for the grid
	// code it is shown for, where that differs from the page's own.
	Title string
	// Table, if not nil, is the fixed table the original shows at the top
	// of the page for the grid code (the interface protection of the
	// standard): a header row, then rows of cells, worded as the original
	// shows them.
	Table  [][]string
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
		pageAUNZ2015B, pageSouthAfrica, pageFrance, pageSpain, pageBelgium,
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
	// High, if not 0, is the setting that holds the high bits of a field
	// split over two words: the bits HighMask selects in High, above the
	// bits Mask selects in Word. Reverse-engineered: the original reads
	// such values from two words. For the 32-bit over-voltage values (both
	// masks 0xFFFF) the order is INFERRED from a real file, where 59728 in
	// Word and 77 in High (77 << 16 | 59728 = 5106000) show as the
	// original's 255.3 V.
	High, HighMask uint16
	// NeedsWord and NeedsMask, if NeedsMask is not 0, are a field that
	// switches p on: the original greys p's control while that field is
	// NeedsOff (normally 0), or, with NeedsClear, while it is not 0.
	NeedsWord, NeedsMask uint16
	NeedsOff             int
	NeedsClear           bool
	// Twin, if not 0, are other bits of Word that the original writes with
	// a copy of the field (it reads only Mask). Reverse-engineered: the
	// Poland start frequency is kept in both bytes of its word.
	Twin uint16
	// ShowWord and ShowMask, if ShowMask is not 0, are a field that
	// selects p: the original shows p only while that field is ShowValue.
	ShowWord, ShowMask uint16
	ShowValue          int
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
	// Decimals, if not 0, is the number of decimals the original shows,
	// where one step of the field would need more; negative is none.
	Decimals int
	// TripDelay marks a delay the original converts with its own handler
	// instead of Scale and Offset: the field counts ticks of 26.2144 ms
	// minus 15 (at least 4), shown in steps of Scale seconds, and 3 means
	// 0 s. Reverse-engineered (the TOR-D4 and Romania trip delays).
	TripDelay bool
	// CosPhiMin, if not 0, marks a cos phi from -1 to 1 that the original
	// converts with its own handler instead of Scale and Offset: the word
	// is 32768 * (2 - cos phi) for a positive cos phi, 32768 * -cos phi
	// otherwise. The value is taken in steps of 0.01; one nearer to 0 than
	// CosPhiMin is written as CosPhiMin with its sign (0 as +CosPhiMin), -1
	// as 1. Min and Max are the fields shown as -1 and 1. Reverse-engineered.
	CosPhiMin float64
	// TanPhi marks a cos phi from 0 to 1 that the original stores as
	// tan(phi) * 256 (in steps of 0.01 of the cos phi); a word with bit 15
	// set is 0 (no limit), and 0 is written as 0xFFFF. Min and Max are the
	// fields shown as 0 and 1. Reverse-engineered.
	TanPhi bool

	// Default is the field value of the default, if HasDefault. A default
	// the original gives outside the parameter's own range is treated as
	// unreliable and left out.
	Default    int
	HasDefault bool

	// Checkbox only: Inverted means ticked when the field is 0.
	Inverted bool
	// Checkbox only: Values, if not nil, are the field values of an
	// unticked and of a ticked box; a field of any other value than
	// Values[0] shows ticked.
	Values *[2]int
	// Choice only: the allowed field values and this project's names for
	// them.
	Choices map[int]string

	// cei marks a parameter of the CEI 0-21 page that the original
	// converts with handlers of its own (cei.go).
	cei ceiPart
}

// tripDelayTick is the tick of a TripDelay field, in seconds.
const tripDelayTick = 0.0262144

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
		if p.TanPhi {
			if field&0x8000 != 0 {
				return 0
			}
			return math.Round(math.Cos(math.Atan(float64(field)/256))*100) / 100
		}
		if p.CosPhiMin != 0 {
			c := -float64(field) / 32768
			if field >= 32768 {
				c = 2 - float64(field)/32768
			}
			return math.Round(c*100) / 100
		}
		if p.TripDelay {
			if field == 3 {
				return 0
			}
			return p.Scale * math.Round((float64(field)+15)*tripDelayTick/p.Scale)
		}
		v := p.Scale * (float64(field) + p.Offset)
		if p.Reciprocal {
			return 1 / v
		}
		return v
	case Checkbox:
		if p.Values != nil {
			if field == p.Values[0] {
				return 0
			}
			return 1
		}
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

// Shown reports whether p is shown while setting p.ShowWord holds w.
func (p Param) Shown(w uint16) bool {
	return p.ShowMask == 0 || Param{Mask: p.ShowMask}.Field(w) == p.ShowValue
}

// Enabled reports whether p can be changed (is not greyed) while setting
// p.NeedsWord holds w.
func (p Param) Enabled(w uint16) bool {
	if p.NeedsMask == 0 {
		return true
	}
	f := Param{Mask: p.NeedsMask}.Field(w)
	if p.NeedsClear {
		return f == 0
	}
	return f != p.NeedsOff
}

// FieldWords extracts p's field from word and high, the values of settings
// p.Word and p.High; high is ignored when p.High is 0. A signed field's
// sign bit is the top bit of its high part.
func (p Param) FieldWords(word, high uint16) int {
	if p.High == 0 {
		return p.Field(word)
	}
	h := Param{Mask: p.HighMask}
	f := h.Field(high)<<p.width() | int(word&p.Mask)>>p.shift()
	if w := p.width() + h.width(); p.Signed && f >= 1<<(w-1) {
		f -= 1 << w
	}
	return f
}

// UnpackWords returns p's display value from the values of settings p.Word
// and p.High (see FieldWords).
func (p Param) UnpackWords(word, high uint16) float64 { return p.Value(p.FieldWords(word, high)) }

// PackWords returns word and high, the values of settings p.Word and
// p.High, with p's field set to display value v, as Pack does; the other
// bits of both are unchanged. When p.High is 0, high is returned
// unchanged.
func (p Param) PackWords(word, high uint16, v float64) (uint16, uint16, error) {
	if p.High == 0 {
		w, err := p.Pack(word, v)
		return w, high, err
	}
	f, err := p.field(v)
	if err != nil {
		return word, high, err
	}
	h := Param{Mask: p.HighMask}
	return word&^p.Mask | uint16(f)<<p.shift()&p.Mask,
		high&^p.HighMask | uint16(f>>p.width())<<h.shift()&p.HighMask, nil
}

func (p Param) ceiDroop() bool { return p.cei == ceiOverDroop || p.cei == ceiUnderDroop }

// FieldIn returns p's field from the grid words; word returns the value of
// a grid word. It is FieldWords, except for a CEI 0-21 droop, which
// follows from two words (cei.go).
func (p Param) FieldIn(word func(uint16) uint16) int {
	if p.ceiDroop() {
		return p.ceiField(word)
	}
	var high uint16
	if p.High != 0 {
		high = word(p.High)
	}
	return p.FieldWords(word(p.Word), high)
}

// PackIn returns the grid words that change when p is set to display
// value v, with their new values; word returns the current value of a grid
// word. It is PackWords, except that a CEI 0-21 start frequency or droop
// also rewrites the rest of its curve, and that a CEI 0-21 frequency out of
// order is refused (ErrFrequencyOrder).
func (p Param) PackIn(word func(uint16) uint16, v float64) (map[uint16]uint16, error) {
	own := map[uint16]uint16{}
	if p.ceiDroop() {
		if _, err := p.field(v); err != nil {
			return nil, err
		}
	} else {
		var high uint16
		if p.High != 0 {
			high = word(p.High)
		}
		w, h, err := p.PackWords(word(p.Word), high, v)
		if err != nil {
			return nil, err
		}
		own[p.Word] = w
		if p.High != 0 {
			own[p.High] = h
		}
	}
	if p.cei != ceiNone {
		return p.ceiPack(word, own, v)
	}
	return own, nil
}

// Words returns the grid words p is stored in, each with the bits of it
// that p uses (with its Twin); a word p only reads has no bits (the start frequency of a
// CEI 0-21 droop).
func (p Param) Words() map[uint16]uint16 {
	m := map[uint16]uint16{p.Word: p.Mask | p.Twin}
	if p.High != 0 {
		m[p.High] |= p.HighMask
	}
	switch p.cei {
	case ceiOverStart, ceiUnderStart:
		for w := p.Word + 1; w <= p.Word+5; w++ {
			m[w] = 0xFFFF
		}
	case ceiOverDroop, ceiUnderDroop:
		m[p.Word-1] = 0
		for w := p.Word + 1; w <= p.Word+4; w++ {
			m[w] = 0xFFFF
		}
	}
	return m
}

// Pack returns word with p's field (and its Twin) set to display value v;
// the other bits of word are unchanged. A number is rounded to the nearest
// field value and must lie within p's limits; a checkbox takes 0 or 1; a
// choice takes one of its Choices. On error word is returned unchanged.
func (p Param) Pack(word uint16, v float64) (uint16, error) {
	if p.High != 0 {
		return word, fmt.Errorf("gridcode: %s spans two words; use PackWords", p.Name)
	}
	if p.ceiDroop() {
		return word, fmt.Errorf("gridcode: %s spans several words; use PackIn", p.Name)
	}
	f, err := p.field(v)
	if err != nil {
		return word, err
	}
	word = word&^p.Mask | uint16(f)<<p.shift()&p.Mask
	if p.Twin != 0 {
		word = word&^p.Twin | uint16(f)<<bits.TrailingZeros16(p.Twin)&p.Twin
	}
	return word, nil
}

func (p Param) field(v float64) (int, error) {
	switch p.Kind {
	case Checkbox:
		if v != 0 && v != 1 {
			return 0, fmt.Errorf("gridcode: %s: %g is not 0 or 1", p.Name, v)
		}
		if p.Values != nil {
			return p.Values[int(v)], nil
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
	if p.TanPhi {
		if !(v >= 0 && v <= 1) { // also catches NaN
			return 0, fmt.Errorf("gridcode: %s: %g is outside 0 to 1", p.Name, v)
		}
		c := math.Round(v*100) / 100
		if c == 0 {
			return 0xFFFF, nil
		}
		return int(math.Round(math.Tan(math.Acos(c)) * 256)), nil
	}
	if p.CosPhiMin != 0 {
		if !(v >= -1 && v <= 1) { // also catches NaN
			return 0, fmt.Errorf("gridcode: %s: %g is outside -1 to 1", p.Name, v)
		}
		c := math.Round(v*100) / 100
		switch {
		case c <= -1:
			c = 1
		case c >= 0 && c < p.CosPhiMin:
			c = p.CosPhiMin
		case c < 0 && c > -p.CosPhiMin:
			c = -p.CosPhiMin
		}
		if c > 0 {
			return int(math.Round((2 - c) * 32768)), nil
		}
		return int(math.Round(-c * 32768)), nil
	}
	var x float64
	if p.TripDelay {
		// Negative steps stay negative, so they fail the limits below.
		x = math.Round(v / p.Scale)
		switch {
		case x == 0:
			x = 3
		case x > 0:
			x = max(4, math.Round(x*p.Scale/tripDelayTick-15))
		}
	} else {
		x = v
		if p.Reciprocal {
			x = 1 / v
		}
		x = math.Round(x/p.Scale - p.Offset)
	}
	lo, hi := min(p.Min, p.Max), max(p.Min, p.Max)
	if !(x >= float64(lo) && x <= float64(hi)) { // also catches NaN
		a, b := p.Value(p.Min), p.Value(p.Max)
		return 0, fmt.Errorf("gridcode: %s: %g is outside %s",
			p.Name, v, strings.TrimSpace(fmt.Sprintf("%g to %g %s", min(a, b), max(a, b), p.Unit)))
	}
	return int(x), nil
}
