package gridcode

import (
	"fmt"
	"slices"
)

// The reactive-power group. Reverse-engineered (#84): the original has one
// group that it moves onto the pages that have a place for it, with one of
// two layouts of the grid words, chosen by the grid code and the
// parameter-set version. Which fields it shows follows the regulation mode
// (word 148, bits 0-1): the cos phi settings with modes 0 and 1, the Q
// settings with modes 2 and 3.

// reactivePages are the pages the original shows the group on.
var reactivePages = []*Page{pageUK, pageGermanyA, pageGermanyB, pageAUNZ2015B, pageFrance, pageSpain,
	pageBelgium, pageAustria, pageChile, pageRomania, pageEurope}

// Model is what the original knows about the grid codes of the loaded
// settings: the grid-info table and the parameter-set version it gives.
type Model struct {
	// Table is the grid-info table, nil when the settings have none.
	Table []byte
	// Known is false when the table is not known (not read from the
	// device, or one CheckTable refuses).
	Known bool
}

// TableModel is the Model of settings whose grid-info table, from a file
// or read from the device, is table (nil: none). A table CheckTable
// refuses gives a Model that is not known.
func TableModel(table []byte) Model {
	if CheckTable(table) != nil {
		return Model{}
	}
	return Model{Table: table, Known: true}
}

// maxVersion is the highest parameter-set version of each grid code the
// original knows. Reverse-engineered.
var maxVersion = [...]uint8{2, 0, 2, 0, 0, 0, 1, 1, 0, 2, 0, 2, 2, 0, 0, 1, 1, 1, 1, 1, 0, 0, 1, 1, 1, 1, 1, 1, 1, 1, 0, 2, 1}

// CheckTable returns an error for a grid-info table whose layout this
// program does not know, as the original refuses it: a format above 3,
// more than 33 grid codes, or a grid code with a parameter-set version
// above the highest known. nil is no table, which is fine.
func CheckTable(table []byte) error {
	if table == nil {
		return nil
	}
	if len(table) < tableOffOffsets {
		return fmt.Errorf("gridcode: grid-info table of %d bytes is too short", len(table))
	}
	if table[tableOffFormat] > 3 || int(table[tableOffCount]) > len(maxVersion) {
		return fmt.Errorf("gridcode: grid-info table of format %d with %d grid codes is not known", table[tableOffFormat], table[tableOffCount])
	}
	for c := range Code(table[tableOffCount]) {
		v, err := ParamSetVersion(table, c)
		if err != nil {
			return err
		}
		if v > maxVersion[c] {
			return fmt.Errorf("gridcode: grid code %d has parameter-set version %d; the highest known is %d", c, v, maxVersion[c])
		}
	}
	return nil
}

// version returns the parameter-set version of grid code c, -1 when it is
// not known.
func (m Model) version(c Code) int {
	if !m.Known {
		return -1
	}
	v, err := ParamSetVersion(m.Table, c)
	if err != nil {
		return -1
	}
	return int(v)
}

// mask returns EditMask of grid code c and word w; known is false when it
// is not known. Without a table the original's built-in masks are 0 for
// the words asked here (word 148 of codes from 5, word 158 of code 4).
func (m Model) mask(c Code, w uint16) (mask uint16, known bool) {
	if !m.Known {
		return 0, false
	}
	mask, ok, err := EditMask(m.Table, c, w)
	if err != nil {
		return 0, false
	}
	if !ok {
		return 0, true
	}
	return mask, true
}

// PageOf returns the page grid code c shows with the settings of model m:
// its parameters, the reactive-power group (WithReactivePower) and the
// original's caption when it follows the code (Title). known is false
// when the page is not known.
func PageOf(c Code, m Model) (p *Page, known bool) {
	v := m.version(c)
	p, known = PageFor(c, v)
	if p == nil {
		return p, known
	}
	p = p.WithReactivePower(c, m)
	if t := pageTitle(c, v); t != "" {
		q := *p
		q.Title = t
		p = &q
	}
	return p, known
}

// pageTitle returns the caption the original gives the page of grid code
// c with parameter-set version v when it differs from the page's own, ""
// otherwise.
func pageTitle(c Code, v int) string {
	switch {
	case c == 9 && v > 1:
		return "VFR 2019 grid code settings"
	case c == 12 && v > 0:
		return "TOR Erzeuger Typ A grid code settings"
	case c == 18:
		return "DK1 (Western Denmark)"
	case c == 19:
		return "DK2 (Eastern Denmark)"
	case c == 20:
		return "Northern Ireland grid code settings"
	case c == 30:
		return "Czech 2023 grid code settings"
	case c >= 22 && c <= 29:
		region := [...]string{"Australia A", "Australia B", "Australia C", "New Zealand"}[(c-22)%4]
		return "AS/NZS4777.2:2020 " + region + " (Stand alone appendix M)"
	}
	return ""
}

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

// filterWithQCurve reports whether the original shows the filter time
// inside the Q(U) settings (so only with regulation 3) instead of below
// the regulation, for grid code c with model m; known is false when that
// is not known.
func filterWithQCurve(c Code, m Model) (with, known bool) {
	v := m.version(c)
	switch c {
	case 4, 5:
		// The original asks whether code 4 uses word 158.
		mask, known := m.mask(4, 158)
		return mask == 0, known
	case 9:
		return v < 2, v >= 0
	case 10:
		return true, true
	case 11, 12:
		return v == 0, v >= 0
	}
	return false, true
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

// mode makes p shown only while the regulation mode is m.
func (p Param) mode(m int) Param {
	p.ShowWord, p.ShowMask, p.ShowValue = 148, 0x3, m
	return p
}

// tab makes p shown only with the cos phi regulations (0 and 1; q false)
// or the Q ones (2 and 3; q true), as the original's two tabs.
func (p Param) tab(q bool) Param {
	p.ShowWord, p.ShowMask, p.ShowValue = 148, 0x2, 0
	if q {
		p.ShowValue = 1
	}
	return p
}

// reactiveLayout is how the original lays out the group for one grid code.
type reactiveLayout struct {
	compact bool    // layout B
	volts   bool    // the Q(U) voltages in volts (AS/NZS 4777.2:2015 codes)
	filterQ bool    // the filter time inside the Q(U) settings
	lowest  float64 // the lowest cos phi
	// lockU tells whether the voltage lock-in box is shown (layout B
	// only); lockKnown is false when that is not known.
	lockU, lockKnown bool
}

// reactiveParams returns the reactive-power parameters of layout l and the
// names of those that are not shown because what the original does is
// not known.
func reactiveParams(l reactiveLayout) (params []Param, unsupported []string) {
	const common, limits = "Reactive power: common", "Reactive power: limits for the Q set-point"
	const pct = 0.0030517578125
	regulation := choice("Reactive-power control mode", 148, 0x3, none, map[int]string{
		// Values are the list positions (INFERRED).
		0: "constant cos phi", 1: "cos phi follows active power", 2: "constant Q", 3: "Q follows grid voltage",
	})
	filter := num("Reactive-power response time constant", 149, 0xFFFF, 7.276008090921e-05, 0, "s", 13744, 229, 4581).reciprocal()
	params = group(common, regulation)
	if !l.filterQ {
		params = append(params, group(common, filter)...)
	}

	// The curves' words: one point per word (A) or the X values two per
	// word and the Y values in 152-155 (B).
	type point struct {
		x, xMask, y uint16
	}
	points := []point{{150, 0xFFFF, 151}, {152, 0xFFFF, 153}, {154, 0xFFFF, 155}, {156, 0xFFFF, 157}}
	fixed := uint16(151)
	if l.compact {
		points = []point{{151, 0xFF00, 152}, {151, 0xFF, 153}, {150, 0xFF00, 154}, {150, 0xFF, 155}}
		fixed = 152
	}
	activePower := func(n int, pt point) Param {
		if l.compact {
			// The range of layout A (INFERRED: 10-100 %).
			return num(fmt.Sprintf("cos-phi(P) point %d: active power", n), pt.x, pt.xMask, 0.5, 0, "% of Pemax", 20, 200, none)
		}
		return num(fmt.Sprintf("cos-phi(P) point %d: active power", n), pt.x, pt.xMask, pct, 0, "% of Pemax", 3277, 32768, none)
	}
	voltage := func(n int, pt point) Param {
		name := fmt.Sprintf("Q(U) point %d: voltage", n)
		switch {
		case l.volts:
			return num(name, pt.x, pt.xMask, 0.01, 0, "V", 20300, 26500, none)
		case l.compact:
			// The original shows two decimals here (INFERRED from the
			// display type it sets for this layout only).
			p := num(name, pt.x, pt.xMask, 0.25, 400, "% of Un", -80, 80, none).signed()
			p.Decimals = 2
			return p
		}
		return num(name, pt.x, pt.xMask, pct, 0, "% of Un", 26215, 39321, none)
	}
	reactive := func(name string, word uint16) Param {
		return num(name, word, 0xFFFF, pct, -32768, "% of Sr", 13107, 52429, none)
	}

	// The cos phi tab.
	params = append(params, group(common, cosPhi("Constant cos phi (fixed mode)", fixed, l.lowest).mode(0))...)
	for i, pt := range points {
		params = append(params, group(common,
			activePower(i+1, pt).mode(1),
			cosPhi(fmt.Sprintf("cos-phi(P) point %d: cos phi", i+1), pt.y, l.lowest).mode(1))...)
	}
	lockNames := []string{"Voltage threshold to enable the curve (lock-in)", "Voltage threshold to disable the curve (lock-out)", "Apply voltage lock-in/lock-out"}
	switch {
	case !l.compact:
	case !l.lockKnown:
		unsupported = append(unsupported, lockNames...)
	case l.lockU:
		lockIn := num(lockNames[0], 157, 0xFF00, 0.25, 400, "% of Un", -120, 120, none).signed().tab(false)
		lockOut := num(lockNames[1], 157, 0xFF, 0.25, 400, "% of Un", -120, 120, none).signed().tab(false)
		// Greyed while "Use lock-in/out" is not ticked (the bit set).
		for _, p := range []*Param{&lockIn, &lockOut} {
			p.NeedsWord, p.NeedsMask, p.NeedsClear, p.Decimals = 148, 0x20, true, 2
		}
		params = append(params, group(common, lockIn, lockOut,
			check(lockNames[2], 148, 0x20, none).inverted().tab(false))...)
	}

	// The Q tab.
	params = append(params, group(common, reactive("Constant reactive power (fixed mode)", fixed).mode(2))...)
	for i, pt := range points {
		params = append(params, group(common,
			voltage(i+1, pt).mode(3),
			reactive(fmt.Sprintf("Q(U) point %d: reactive power", i+1), pt.y).mode(3))...)
	}
	if l.filterQ {
		params = append(params, group(common, filter.mode(3))...)
	}
	if l.compact {
		minCosPhi := num("Lowest cos phi allowed while following a Q set-point", 156, 0xFFFF, 0.01, 0, "", 0xFFFF, 0, none)
		minCosPhi.TanPhi, minCosPhi.Decimals = true, 2
		params = append(params, group(limits,
			minCosPhi.tab(true),
			num("Active-power threshold to enable the curve (lock-in)", 157, 0xFF00, 0.5, 0, "% of Pemax", 0, 200, none).tab(true),
			num("Active-power threshold to disable the curve (lock-out)", 157, 0xFF, 0.5, 0, "% of Pemax", 0, 200, none).tab(true),
		)...)
	}
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
// and model m added, as the original shows it, or p itself when its page
// has no such group. When the layout depends on what is not known (the
// parameter-set version or the grid-info table of a device), the group is
// listed as not supported instead.
func (p *Page) WithReactivePower(c Code, m Model) *Page {
	if p == nil || !slices.Contains(reactivePages, p) {
		return p
	}
	q := *p
	q.Params = slices.Clone(p.Params)
	q.Unsupported = slices.Clone(p.Unsupported)
	v := m.version(c)
	compact, known := compactReactive(c, v)
	filterQ, filterKnown := filterWithQCurve(c, m)
	if !known || !filterKnown {
		q.Unsupported = append(q.Unsupported, ReactivePowerUnknownLayout)
		return &q
	}
	lock, lockKnown := m.mask(c, 148)
	params, unsupported := reactiveParams(reactiveLayout{
		compact: compact,
		volts:   c == 6 || c == 7 || c == 15 || c == 16,
		filterQ: filterQ,
		lowest:  lowestCosPhi(c, v),
		lockU:   lock&0x20 != 0, lockKnown: lockKnown,
	})
	q.Params = append(q.Params, params...)
	q.Unsupported = append(q.Unsupported, unsupported...)
	return &q
}

// ReactivePowerUnknownLayout is listed as not supported when the layout of
// the reactive-power group depends on the grid-info table of a device that
// could not be read (the parameter-set version, the edit masks).
const ReactivePowerUnknownLayout = "Reactive power (its layout depends on the device's grid-info table, which could not be read)"
