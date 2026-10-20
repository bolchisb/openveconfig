package gridcode

import (
	"errors"
	"fmt"
	"math"
)

// The CEI 0-21 page (Italy). Reverse-engineered (#84):
//
//   - Its frequencies are stored as periods: a word holds 2 500 000 /
//     frequency (ticks of 0.4 µs).
//   - Each network-service curve is six words from its first word W: W
//     holds the period of the start frequency; W+1 half the period
//     difference between the start and the end frequency; W+2 to W+5 two
//     straight pieces, each a slope (times 256) and its value at its end,
//     that approximate the curve over the periods (ceiWords). The end
//     frequency is the start frequency plus (over-frequency curve) or minus
//     (under-frequency curve) droop × 50 Hz / 100.
//   - Changing a start frequency or a droop rewrites W+1 to W+5 from the
//     start frequency and the droop as the original shows them (in steps of
//     0.05 Hz and 0.01 %).
//   - The original refuses a start or stop frequency that breaks the order
//     lower start < lower stop < upper stop < upper start
//     (ErrFrequencyOrder).

// ceiPart marks a parameter of the CEI 0-21 page that the original
// converts with its own handlers.
type ceiPart uint8

const (
	ceiNone ceiPart = iota
	// The start frequency of a curve, in Word, the curve's first word.
	ceiOverStart
	ceiUnderStart
	// The droop of a curve, from Word (W+1) and the word before it.
	ceiOverDroop
	ceiUnderDroop
	// A stop frequency.
	ceiStop
)

// The words of the four frequencies the original keeps in order.
const (
	ceiOverStartWord  = 129
	ceiUnderStartWord = 135
	ceiUpperStopWord  = 142
	ceiLowerStopWord  = 143
)

// ceiTicks is the number of period ticks per second.
const ceiTicks = 2500000

// ErrFrequencyOrder is returned for a CEI 0-21 network-service frequency
// that would break the order the original keeps: lower start < lower stop
// < upper stop < upper start.
var ErrFrequencyOrder = errors.New("gridcode: the network-service frequencies must rise: lower start, lower stop, upper stop, upper start")

// ceiFreq converts a period word to a frequency and a frequency to a
// period, as the original does (0 gives 65535).
func ceiFreq(x float64) float64 {
	if x == 0 {
		return 65535
	}
	return ceiTicks / x
}

// ceiShown returns frequency f as the original's control shows it: in steps
// of 0.05 Hz.
func ceiShown(f float64) float64 { return math.Round(f/0.05) * 0.05 }

// ceiDroop returns the droop of the curve whose start period is start and
// half period difference is half, in %, rounded to 0.01 as the original
// shows it.
func ceiDroop(start, half uint16, under bool) float64 {
	f0 := ceiFreq(float64(start))
	t := 2 * float64(half) / ceiTicks
	var d float64
	if under {
		d = f0 - 1/(1/f0+t)
	} else {
		d = 1/(1/f0-t) - f0
	}
	if math.IsNaN(d) || math.IsInf(d, 0) {
		return 0 // words no curve gives
	}
	return math.Round(d*100/50*100) / 100
}

// ceiWords returns words W+1 to W+5 of the curve that falls from 65536 at
// frequency lo to 0 at frequency hi. It fails when the pieces do not fit
// the words.
func ceiWords(lo, hi float64) (w [5]uint16, err error) {
	if !(hi > lo && lo > 0) { // also catches NaN
		return w, fmt.Errorf("gridcode: CEI curve from %g to %g Hz", lo, hi)
	}
	const a, b = 0, 65536
	k := (a - b) / (hi - lo)
	c := a - k*hi
	y := func(period float64) float64 { return k*ceiTicks/period + c }
	ps, pe := ceiFreq(lo), ceiFreq(hi)
	half := (ps - pe) / 2
	pm := pe + half
	ym := y(pm)
	for i := 1; i < 1000; i++ {
		h := half - float64(i>>1)
		if i&1 == 1 {
			h = half + float64(i>>1)
		}
		w0, ok := ceiRound(h)
		if !ok {
			return w, fmt.Errorf("gridcode: CEI curve from %g to %g Hz does not fit", lo, hi)
		}
		s1, e1, ok := ceiPiece(y, h, ps, b, ym)
		if !ok {
			continue
		}
		s2, e2, ok := ceiPiece(y, h, pm, ym, a)
		if ok {
			return [5]uint16{w0, s1, e1, s2, e2}, nil
		}
	}
	return w, fmt.Errorf("gridcode: CEI curve from %g to %g Hz does not fit", lo, hi)
}

// ceiPiece returns the slope (times 256) and the value at its end of the
// straight piece that approximates curve y from period ps (value ys) over h
// periods down (value ye), as the original fits it: the slope rounded to
// 1/256, the line moved to halve the largest errors at the ends and the
// middle. ok is false when either does not fit a word.
func ceiPiece(y func(float64) float64, h, ps, ys, ye float64) (slope, end uint16, ok bool) {
	m := math.Trunc((ys-ye)/h*256+0.5) / 256
	b := ys - m*ps
	endInt := delphiRound(ps) - delphiRound(h)
	mid := ps - h/2
	errs := []float64{
		math.Trunc(m*ps) + b - ys,
		math.Trunc(m*mid) + b - y(mid),
		math.Trunc(m*(ps-h)) + b - ye,
	}
	off := (slicesMax(errs) + slicesMin(errs)) / 2
	end, ok1 := ceiRound(endInt*m + (b - off))
	slope, ok2 := ceiRound(m * 256)
	return slope, end, ok1 && ok2
}

// delphiRound rounds half away from zero, as the original.
func delphiRound(x float64) float64 { return math.Trunc(x + math.Copysign(0.5, x)) }

// ceiRound rounds x to a word; ok is false when it does not fit.
func ceiRound(x float64) (uint16, bool) {
	r := delphiRound(x)
	switch {
	case math.IsNaN(r):
		return 0, false
	case r < 0:
		return 0, false
	case r > 0xFFFF:
		return 0xFFFF, false
	}
	return uint16(r), true
}

func slicesMax(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs[1:] {
		m = math.Max(m, x)
	}
	return m
}

func slicesMin(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs[1:] {
		m = math.Min(m, x)
	}
	return m
}

// ceiField returns the field of a CEI droop parameter: the droop in 0.01 %.
func (p Param) ceiField(word func(uint16) uint16) int {
	return int(math.Round(ceiDroop(word(p.Word-1), word(p.Word), p.cei == ceiUnderDroop) * 100))
}

// ceiPack returns the words that change when CEI parameter p is set to v,
// with their new values, after p's own word, if any, was set to own.
func (p Param) ceiPack(word func(uint16) uint16, own map[uint16]uint16, v float64) (map[uint16]uint16, error) {
	get := func(w uint16) uint16 {
		if x, ok := own[w]; ok {
			return x
		}
		return word(w)
	}
	first := p.Word // the curve's first word
	var start, droop float64
	switch p.cei {
	case ceiOverStart, ceiUnderStart:
		start = ceiShown(v)
		droop = ceiDroop(word(first), word(first+1), p.cei == ceiUnderStart)
	case ceiOverDroop, ceiUnderDroop:
		first--
		start = ceiShown(ceiFreq(float64(get(first))))
		droop = math.Round(v*100) / 100
	}
	if p.cei != ceiStop {
		lo, hi := start, start+droop*50/100
		if p.cei == ceiUnderStart || p.cei == ceiUnderDroop {
			lo, hi = start-droop*50/100, start
		}
		ws, err := ceiWords(lo, hi)
		if err != nil {
			return nil, err
		}
		for i, x := range ws {
			own[first+1+uint16(i)] = x
		}
	}
	f := func(w uint16) float64 { return ceiShown(ceiFreq(float64(get(w)))) }
	if !(f(ceiUnderStartWord) < f(ceiLowerStopWord) && f(ceiLowerStopWord) < f(ceiUpperStopWord) && f(ceiUpperStopWord) < f(ceiOverStartWord)) {
		return nil, ErrFrequencyOrder
	}
	return own, nil
}
