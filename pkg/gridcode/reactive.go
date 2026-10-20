package gridcode

import "fmt"

// The reactive-power group. Reverse-engineered (#84): the original has one
// group that it moves onto the pages that have a place for it, with one of
// two layouts of the grid words, chosen by the grid code and the
// parameter-set version. Which fields it shows follows the regulation mode
// (word 148, bits 0-1).

// reactivePages are the pages the original shows the group on.
var reactivePages = []*Page{pageUK, pageAUNZ2020, pageFrance, pageSpain, pageBelgium, pageAustria, pageChile, pageRomania, pageEurope}

// compactReactive reports whether grid code c uses the compact layout (B)
// with parameter-set version v; known is false when that depends on a
// version that is not known (v < 0).
func compactReactive(c Code, v int) (compact, known bool) {
	switch c {
	case 9:
		return v > 1, v >= 0
	case 11, 12:
		return v > 0, v >= 0
	case 17, 18, 19, 30, 32:
		return true, true
	}
	return false, true
}

// mode makes p shown only while the regulation mode is m.
func (p Param) mode(m int) Param {
	p.ShowWord, p.ShowMask, p.ShowValue = 148, 0x3, m
	return p
}

// lowestCosPhi returns the cos phi nearest to 0 that the original lets
// grid code c with parameter-set version v set (either sign).
func lowestCosPhi(c Code, v int) float64 {
	switch {
	case c == 2, c >= 17 && c <= 20, c >= 30 && c <= 32,
		(c == 11 || c == 12) && v > 0, c == 9 && v > 1:
		return 0.8
	}
	return 0.9
}

// cosPhi is a cos phi field (see Param.CosPhiMin).
func cosPhi(name string, word uint16, lowest float64) Param {
	p := num(name, word, 0xFFFF, 0.01, 0, "", 32767, 32768, none)
	p.CosPhiMin, p.Decimals = lowest, 2
	return p
}

// ReactiveParams returns the reactive-power parameters of the layout (A,
// or the compact B) with cos phi values limited to lowest, and the names
// of those whose encoding is not known.
func ReactiveParams(compact bool, lowest float64) (params []Param, unsupported []string) {
	const common, limits = "Reactive power: common", "Reactive power: limits for the Q set-point"
	const pct = 0.0030517578125
	params = group(common,
		// Values are the list positions (INFERRED).
		choice("Reactive-power control mode", 148, 0x3, none, map[int]string{
			0: "constant cos phi", 1: "cos phi follows active power", 2: "constant Q", 3: "Q follows grid voltage",
		}),
		num("Reactive-power response time constant", 149, 0xFFFF, 7.276008090921e-05, 0, "s", 13744, 229, 4581).reciprocal(),
	)
	if !compact {
		params = append(params, group(common, cosPhi("Constant cos phi (fixed mode)", 151, lowest).mode(0))...)
		for n := 1; n <= 4; n++ {
			x := uint16(150 + 2*(n-1))
			params = append(params, group(common,
				num(fmt.Sprintf("cos-phi(P) point %d: active power", n), x, 0xFFFF, pct, 0, "% of Pemax", 3277, 32768, none).mode(1),
				cosPhi(fmt.Sprintf("cos-phi(P) point %d: cos phi", n), x+1, lowest).mode(1))...)
		}
		params = append(params, group(common,
			num("Constant reactive power (fixed mode)", 151, 0xFFFF, pct, -32768, "% of Sr", 13107, 52429, none).mode(2))...)
		for n := 1; n <= 4; n++ {
			x := uint16(150 + 2*(n-1))
			params = append(params, group(common,
				num(fmt.Sprintf("Q(U) point %d: voltage", n), x, 0xFFFF, pct, 0, "% of Un", 26215, 39321, none).mode(3),
				num(fmt.Sprintf("Q(U) point %d: reactive power", n), x+1, 0xFFFF, pct, -32768, "% of Sr", 13107, 52429, none).mode(3))...)
		}
		return decimals1(params), nil
	}
	// Layout B: the curve's X values two per word, the Y values in 152-155.
	// The ranges are those of layout A (INFERRED: 10-100 % and 80-120 %).
	xs := []struct{ word, mask uint16 }{{151, 0xFF00}, {151, 0xFF}, {150, 0xFF00}, {150, 0xFF}}
	params = append(params, group(common, cosPhi("Constant cos phi (fixed mode)", 152, lowest).mode(0))...)
	for n, x := range xs {
		params = append(params, group(common,
			num(fmt.Sprintf("cos-phi(P) point %d: active power", n+1), x.word, x.mask, 0.5, 0, "% of Pemax", 20, 200, none).mode(1),
			cosPhi(fmt.Sprintf("cos-phi(P) point %d: cos phi", n+1), uint16(152+n), lowest).mode(1))...)
	}
	params = append(params, group(common,
		num("Constant reactive power (fixed mode)", 152, 0xFFFF, pct, -32768, "% of Sr", 13107, 52429, none).mode(2))...)
	for n, x := range xs {
		params = append(params, group(common,
			num(fmt.Sprintf("Q(U) point %d: voltage", n+1), x.word, x.mask, 0.25, 400, "% of Un", -80, 80, none).signed().mode(3),
			num(fmt.Sprintf("Q(U) point %d: reactive power", n+1), uint16(152+n), 0xFFFF, pct, -32768, "% of Sr", 13107, 52429, none).mode(3))...)
	}
	params = append(params, group(limits,
		num("Active-power threshold to enable the curve (lock-in)", 157, 0xFF00, 0.5, 0, "% of Pemax", 0, 200, none),
		num("Active-power threshold to disable the curve (lock-out)", 157, 0xFF, 0.5, 0, "% of Pemax", 0, 200, none),
	)...)
	// The lowest cos phi (its own conversion in the original, not decoded)
	// and the voltage lock-in box, which the original shows on a grid-table
	// flag that is not known.
	unsupported = []string{"Lowest cos phi allowed while following a Q set-point",
		"Voltage threshold to enable the curve (lock-in)", "Voltage threshold to disable the curve (lock-out)", "Apply voltage lock-in/lock-out"}
	return decimals1(params), unsupported
}

// decimals1 sets the decimals the original shows for the group's numbers.
func decimals1(ps []Param) []Param {
	for i := range ps {
		if ps[i].Kind == Number && ps[i].Decimals == 0 {
			ps[i].Decimals = 1
		}
	}
	return ps
}

// WithReactivePower returns p with the reactive-power group of grid code c
// and parameter-set version v (negative: not known) added, as the original
// shows it, or p itself when its page has no such group. When the layout
// depends on a version that is not known, the group is listed as not
// supported instead.
func (p *Page) WithReactivePower(c Code, v int) *Page {
	if p == nil || !contains(reactivePages, p) {
		return p
	}
	q := *p
	q.Params = append([]Param(nil), p.Params...)
	q.Unsupported = append([]string(nil), p.Unsupported...)
	compact, known := compactReactive(c, v)
	if !known {
		q.Unsupported = append(q.Unsupported, ReactivePowerUnknownLayout)
		return &q
	}
	params, unsupported := ReactiveParams(compact, lowestCosPhi(c, v))
	q.Params = append(q.Params, params...)
	q.Unsupported = append(q.Unsupported, unsupported...)
	return &q
}

// ReactivePowerUnknownLayout is listed as not supported when the layout of
// the reactive-power group depends on a parameter-set version that is not
// known (a device, whose version is not read).
const ReactivePowerUnknownLayout = "Reactive power (its layout depends on the parameter-set version, which is not known)"

func contains(pages []*Page, p *Page) bool {
	for _, q := range pages {
		if q == p {
			return true
		}
	}
	return false
}
