package gridcode

import (
	"math"
	"slices"
)

// The parameter pages. Reverse-engineered (#27): word, mask, scale, offset,
// sign, limits and defaults are those the original tool uses for each
// page, restated here; names, groups and choice names are this project's
// own. Limits and defaults are field values (see Param); "none" means the
// original gives no default.
//
// Left out of every page (listed in Page.Unsupported): parameters whose
// value is spread over several words, converted by logic in the original
// that is not known. Many parameters also run extra logic in the original
// when changed (likely ordering checks between neighbouring values); its
// content is not known and is not modelled.

const none = math.MinInt

func num(name string, word, mask uint16, scale, offset float64, unit string, min, max, def int) Param {
	p := Param{Name: name, Kind: Number, Word: word, Mask: mask, Scale: scale, Offset: offset, Unit: unit, Min: min, Max: max}
	if def != none {
		p.Default, p.HasDefault = def, true
	}
	return p
}

func check(name string, word, mask uint16, def int) Param {
	p := Param{Name: name, Kind: Checkbox, Word: word, Mask: mask}
	if def != none {
		p.Default, p.HasDefault = def, true
	}
	return p
}

func choice(name string, word, mask uint16, def int, choices map[int]string) Param {
	p := Param{Name: name, Kind: Choice, Word: word, Mask: mask, Choices: choices}
	if def != none {
		p.Default, p.HasDefault = def, true
	}
	return p
}

// overVoltage is the over-voltage trip on the 10-minute running average,
// a 32-bit field in settings 129 (low) and 130 (high), in volts, with the
// field values of its limits and default (V x 20000).
func overVoltage(min, max, def int) Param {
	p := num(overVoltage10Min, 129, 0xFFFF, 5e-05, 0, "V", min, max, def)
	p.High, p.HighMask, p.Decimals = 130, 0xFFFF, 1
	return p
}

func (p Param) signed() Param { p.Signed = true; return p }

// high makes the bits mask of word the high part of p's field (see
// Param.High).
func (p Param) high(word, mask uint16) Param { p.High, p.HighMask = word, mask; return p }

// decimals sets the number of decimals the original shows for p.
func (p Param) decimals(n int) Param {
	p.Decimals = n
	if n == 0 {
		p.Decimals = -1 // none
	}
	return p
}

// needs makes p usable only while the field mask of word is not 0.
// tripDelay is a TOR-D4 trip delay of 0-180 s (see Param.TripDelay), 0 s
// by default.
func tripDelay(name string, word uint16) Param {
	p := num(name, word, 0xFFFF, 0.5, 0, "s", 3, 6851, 3)
	p.TripDelay, p.Decimals = true, 1
	return p
}

func (p Param) needs(word, mask uint16) Param { p.NeedsWord, p.NeedsMask = word, mask; return p }
func (p Param) reciprocal() Param             { p.Reciprocal = true; return p }
func (p Param) inverted() Param               { p.Inverted = true; return p }

// group sets the group of ps.
func group(name string, ps ...Param) []Param {
	for i := range ps {
		ps[i].Group = name
	}
	return ps
}

func params(groups ...[]Param) []Param {
	var all []Param
	for _, g := range groups {
		all = append(all, g...)
	}
	return all
}

var (
	appliesTo   = map[int]string{0: "inverting (feed-in)", 1: "charging"}
	puReduction = map[int]string{0: "off", 1: "reduce relative to nominal power", 2: "reduce relative to present power"}
)

// On several pages.
const overVoltage10Min = "Over-voltage trip on 10-minute running average"

// On both Germany pages.
const germanyDroop = "Over-frequency power reduction: droop"

// 5000000 / 2^32 Hz per step, used by several frequency fields.
const hzStep = 0.00116415321826935

var pageUK = &Page{
	Name: "United Kingdom (G83/G59 family)",
	Params: params(group("",
		num("Over-frequency power reduction: start frequency", 142, 0xFFFF, hzStep, 0, "Hz", 43121, 44668, 43293).decimals(2),
		num("Over-frequency power reduction: start delay", 144, 0xFFFF, 0.0262144, 0, "s", 3, 76, 3),
		num("Over-frequency power reduction: droop", 143, 0xFFFF, 0.0023283064365387, 0, "%", 859, 5154, 4295).decimals(1),
		check("Use auxiliary input 1 as feed-in inhibit", 148, 0x4, none).inverted(),
	)),
}

// ceiFreqParam is a frequency of the CEI 0-21 page, stored as a period (cei.go),
// with the field values of its limits and default.
func ceiFreqParam(name string, word uint16, part ceiPart, min, max, def int) Param {
	p := num(name, word, 0xFFFF, 1.0/ceiTicks, 0, "Hz", min, max, def).reciprocal().decimals(2)
	p.cei = part
	return p
}

// ceiDroopParam is the droop of the CEI 0-21 curve whose first word is the
// word before word (cei.go), in 0.01 %.
func ceiDroopParam(name string, word uint16, part ceiPart, def int) Param {
	p := num(name, word, 0xFFFF, 0.01, 0, "%", 100, 500, def)
	p.cei = part
	return p
}

// pageItaly, in the original's order. Its delays count ticks of 26.2144 ms
// from 2; the original shows them in steps of its own (0.05 s and 5 s).
var pageItaly = &Page{
	Name: "Italy (CEI 0-21)",
	Params: params(group("",
		ceiFreqParam("Network-support mode: upper activation frequency", ceiOverStartWord, ceiOverStart, 50000, 48077, 49702),
		ceiFreqParam("Network-support mode: lower activation frequency", ceiUnderStartWord, ceiUnderStart, 53191, 50000, 50302),
		num("Network-support mode: activation delay", 141, 0xFFFF, 0.0262144, -2, "seconds", 2, 78, 2),
		ceiFreqParam("Network-support mode: release band, lower frequency", ceiLowerStopWord, ceiStop, 53191, 50000, 50100),
		ceiFreqParam("Network-support mode: release band, upper frequency", ceiUpperStopWord, ceiStop, 50000, 48077, 49900),
		num("Network-support mode: time inside release band", 144, 0xFFFF, 0.0262144, -2, "seconds", 2, 34334, 11446).decimals(-1),
		num("Power limit PSMax (CEI 0-21 term)", 146, 0xFFFF, 1, 0, "W", 100, 2400, 2400),
		ceiDroopParam("Droop paired with power limit PSMax", ceiUnderStartWord+1, ceiUnderDroop, 120),
		// Stored as 65536 - watts.
		num("Power limit PCMax (CEI 0-21 term)", 145, 0xFFFF, -1, -65536, "W", 65436, 64286, 64286),
		ceiDroopParam("Droop paired with power limit PCMax", ceiOverStartWord+1, ceiOverDroop, none),
	)),
}

// germanyCommon are the parameters both Germany variants have.
func germanyCommon() []Param {
	return group("",
		num("Over-frequency power reduction: start frequency", 158, 0xFFFF, hzStep, 0, "Hz", 43121, 43379, 43121).decimals(2),
		// Left out when grid code 4 has no edit mask for word 158 (forCode).
		num(germanyDroop, 144, 0xFF, 0.05, 0, "%", 20, 250, 100),
		check("Use auxiliary input 1 as feed-in inhibit", 148, 0x4, none).inverted(),
		check("Use auxiliary input 2 as 4.2 kW charge-power cap signal (German EnWG section 14a)", 184, 0x1, none),
		num("AC current cap for charging and feed-in", 169, 0xFF, 0.5, 0, "% of nominal current", 0, 200, 0),
	)
}

var pageGermanyA = &Page{
	Name:   "Germany, variant A (VDE-AR-N 4105, short form)",
	Params: germanyCommon(),
}

// pageGermanyB. Its reactive-power group is added by WithReactivePower,
// as on the other pages that have one.
var pageGermanyB = &Page{
	Name: "Germany, variant B (VDE-AR-N 4105, full form)",
	Params: params(
		group("", overVoltage(5060000, 5290000, 5060000)),
		germanyCommon(),
	),
}

// The fixed tables of the UK and Germany pages: the interface protection of
// the standard, as the original shows it (cells worded as on its screen,
// blank rows kept, trailing ones left out). The original fills the UK table
// by grid code and, for codes 2 and 31, by whether grid code 2 has an edit
// mask for word 148, and its loss-of-mains cell by whether the AC input
// settings box of its grid code tab is shown; that box is taken as shown
// (INFERRED: it is for devices with setting 190).
var (
	protectionHeader = []string{"Protection function", "Trip setting", "Trip delay"}
	tableG99         = [][]string{protectionHeader,
		{"U/V", "184 V", "2.5 s"},
		{"O/V stage 1", "262.2 V", "1 s"},
		{"O/V stage 2", "273.7 V", "0.5 s"},
		{"U/F stage 1", "47.5 Hz", "20 s"},
		{"U/F stage 2", "47 Hz", "0.5 s"},
		{"O/F", "52 Hz", "0.5 s"},
		{"LOM detection", "see LOM setting", "0 s"},
		{"", "", ""},
		{"Reconnection time", "", "> 20 s"},
	}
	// Grid code 2 without the edit mask (the G59 settings).
	tableG59 = [][]string{protectionHeader,
		{"U/V stage 1", "200.1V", "2.5s"},
		{"U/V stage 2", "184V", "0.5s"},
		{"O/V stage 1", "262.2V", "1.0s"},
		{"O/V stage 2", "273.7V", "0.5s"},
		{"U/F stage 1", "47.5 Hz", "20s"},
		{"U/F stage 2", "47 Hz", "0.5s"},
		{"O/F stage 1", "51.5 Hz", "90s"},
		{"O/F stage 2", "52 Hz", "0.5s"},
		{"LOM detection", "see LOM setting", "0.0s"},
	}
	tableNorthernIreland = [][]string{protectionHeader,
		{"U/V", "195.5 V", "3 s"},
		{"O/V", "253.0 V", "0.5 s"},
		{"U/F", "48 Hz", "0.5 s"},
		{"O/F", "52 Hz", "1 s"},
		{"LOM detection", "see LOM setting", "0 s"},
		{"", "", ""},
		{"Reconnection time", "", "> 60 s"},
	}
)

// tableVDE returns the table of the Germany page with built-in protection
// (grid code 5); the under-voltage trip delay follows the standard's
// edition, told by grid code 4's edit mask for word 158.
func tableVDE(edition2018 bool) [][]string {
	delay := "< 100ms"
	if edition2018 {
		delay = "> 3s"
	}
	return [][]string{protectionHeader,
		{"under voltage", "U < 184 V", delay},
		{"over voltage", "U >> 264.5 V", "< 100ms"},
		{"under freq.", "f < 47.5 Hz", "< 100ms"},
		{"over freq.", "f > 51.5 Hz", "< 100ms"},
		{"", "", ""},
		{"Reconnection time", "", "> 60 s"},
	}
}

// forCode returns p as the original shows it for grid code c with model m:
// the UK captions and tables, the Germany table, and the Germany droop left
// out when grid code 4 has no edit mask for word 158 (the original then
// reports the droop as not supported). Where that depends on a table that
// is not known, p is left as it is.
func (p *Page) forCode(c Code, m Model) *Page {
	q := *p
	switch c {
	case 2, 31:
		if in31, known := m.listed(31); c == 31 {
			q.Title = "UK G98 grid code settings"
		} else if known && in31 {
			q.Title = "UK G99 grid code settings"
		}
		if mask, known := m.mask(2, 148); known && mask != 0 {
			q.Table = tableG99
		} else if known {
			q.Table = tableG59
		}
	case 20:
		q.Table = tableNorthernIreland
	case 4, 5:
		mask, known := m.mask(4, 158)
		if known && mask == 0 {
			q.Params = slices.DeleteFunc(slices.Clone(p.Params), func(prm Param) bool { return prm.Name == germanyDroop })
		}
		if c == 5 && known {
			q.Table = tableVDE(mask != 0)
		}
	default:
		return p
	}
	return &q
}

// pageAUNZ2015 is the page of the AS/NZS 4777.2:2015 codes at
// parameter-set version 0. Its other values are not stored as fields: the
// original keeps the ramp as a rate that depends on a value it reads from
// the device, and writes derived words with the volt-watt points (the
// slopes between them) and with the cut-off frequency.
var pageAUNZ2015 = &Page{
	Name: "Australia/New Zealand (AS/NZS 4777.2:2015)",
	Params: params(group("",
		overVoltage(4880000, 5160000, 5100000),
		check("Demand-response mode 0 enabled", 146, 0x1, 1),
	)),
	Unsupported: []string{
		"Power ramp: time for a 0-100 % change",
		"Volt-watt curve voltage 1",
		"Volt-watt curve voltage 2",
		"Volt-watt curve voltage 3",
		"Volt-watt curve voltage 4",
		"Volt-watt curve power at voltage 1",
		"Volt-watt curve power at voltage 2",
		"Volt-watt curve power at voltage 3",
		"Volt-watt curve power at voltage 4",
		"Sustained over-frequency: cut-off frequency",
	},
}

// pageAUNZ2015B is the page of the AS/NZS 4777.2:2015 codes from
// parameter-set version 1 (the original's second AS/NZS 4777 page).
var pageAUNZ2015B = &Page{
	Name: "Australia/New Zealand (AS/NZS 4777.2:2015, parameter set 1 and later)",
	Params: params(
		group("",
			overVoltage(4880000, 5160000, 5100000),
			num("Power ramp: time for a 0-100 % change", 146, 0xFFFF, 0.0262144, 0, "s", 2289, 45776, 13733).decimals(0),
			check("Demand-response mode 0 enabled", 147, 0x1, 1),
		),
		group("Sustained over-frequency",
			num("Sustained over-frequency: cut-off frequency", 143, 0xFFFF, hzStep, 43164, "Hz", 645, 1504, 1504).decimals(2),
		),
		group("Volt-watt curve",
			num("Volt-watt curve voltage 1", 131, 0xFFFF, 0.01, 0, "V", 20300, 21500, 20700).decimals(1),
			choice("Volt-watt point 1 applies to", 132, 0x8000, none, appliesTo),
			num("Volt-watt curve power at voltage 1", 132, 0x7FFF, 0.00305185094759972, 0, "%", 0, 32767, 0).decimals(0),
			num("Volt-watt curve voltage 2", 133, 0xFFFF, 0.01, 0, "V", 21600, 23000, 22000).decimals(1),
			choice("Volt-watt point 2 applies to", 134, 0x8000, none, appliesTo),
			num("Volt-watt curve power at voltage 2", 134, 0x7FFF, 0.00305185094759972, 0, "%", 0, 32767, 0).decimals(0),
			num("Volt-watt curve voltage 3", 135, 0xFFFF, 0.01, 0, "V", 23500, 25500, 25000).decimals(1),
			choice("Volt-watt point 3 applies to", 136, 0x8000, none, appliesTo),
			num("Volt-watt curve power at voltage 3", 136, 0x7FFF, 0.00305185094759972, 0, "%", 0, 32767, 0).decimals(0),
			num("Volt-watt curve voltage 4", 137, 0xFFFF, 0.01, 0, "V", 24400, 26500, 26500).decimals(1),
			choice("Volt-watt point 4 applies to", 138, 0x8000, none, appliesTo),
			num("Volt-watt curve power at voltage 4", 138, 0x7FFF, 0.00305185094759972, 0, "%", 0, 32767, 0).decimals(0),
		),
	),
}

// pageSouthAfrica. Its one parameter, the power ramp after reconnection
// (word 129), is a power step: the original shows it as the time for a
// 0-100 % change, a conversion that needs the device's rated power, which
// this program does not read.
var pageSouthAfrica = &Page{
	Name:        "South Africa (NRS 097-2-1)",
	Unsupported: []string{"Power ramp after reconnection: time for 0-100 %"},
}

var pageFrance = &Page{
	Name:   "France (UTE C15-712-1)",
	Params: params(group("", overVoltage(5060000, 5290000, 5060000))),
}

// pageSpain has no parameters; the original shows only static content.
var pageSpain = &Page{Name: "Spain (RD 1699/2011)"}

var pageBelgium = &Page{
	Name:   "Belgium (C10/11)",
	Params: params(group("", overVoltage(5060000, 5290000, 5060000))),
}

var pageAustria = &Page{
	Name: "Austria (TOR-D4)",
	Params: params(
		group("",
			tripDelay("Under-voltage trip delay", 135),
			tripDelay("Fast over-voltage trip delay", 137),
			overVoltage(5060000, 5290000, 5106000),
			num("Reconnection wait time", 131, 0xFF, 1, 0, "s", 15, 255, 60),
		),
		group("Power-versus-voltage curve",
			choice("P(U) curve type", 184, 0x3, none, map[int]string{0: "off", 1: "curve type A", 2: "curve type B"}),
			num("P(U) curve: knee voltage", 182, 0xFFFF, 0.01, 0, "V", 24000, 27600, 25300),
			num("P(U) curve: voltage span to zero power", 183, 0xFFFF, 1.19209289550781e-05, 0, "V", 65535, 4194, 18236).reciprocal(),
		),
	),
}

var pageChile = &Page{
	Name: "Chile (NTG-BT)",
	Params: params(
		group("",
			overVoltage(4840000, 5060000, 4840000),
			check("Use auxiliary input 1 as feed-in inhibit", 148, 0x4, none).inverted(),
		),
		// As on the Romania page (the same handler in the original): both
		// voltages are greyed while the P(U) curve is "Not used".
		group("Power-versus-voltage curve",
			choice("P(U) reduction method", 148, 0x18, none, puReduction),
			num("P(U) curve: start voltage", 131, 0xFFFF, 0.01, 0, "V", 23000, 25500, 23800).needs(148, 0x18),
			num("P(U) curve: end voltage", 132, 0xFFFF, 0.01, 0, "V", 23500, 26000, 24200).needs(148, 0x18),
		),
	),
}

// pageRomania, in the original's order.
var pageRomania = &Page{
	Name: "Romania (TOR-D4 derivative)",
	Params: params(
		group("",
			num("Upper frequency allowed for reconnection", 147, 0xFFFF, hzStep, 0, "Hz", 42993, 43379, 43121).decimals(2),
			tripDelay("Under-voltage trip delay", 134),
			tripDelay("Fast over-voltage trip delay", 135),
			overVoltage(5060000, 5290000, 5106000),
			// Stored as 65550 - seconds.
			num("Reconnection wait time", 133, 0xFFFF, -1, -65550, "s", 65535, 64350, 64650),
		),
		// Observed on the original's screen: both voltages are greyed
		// while the P(U) curve is "Not used".
		group("Power-versus-voltage curve",
			choice("P(U) reduction method", 148, 0x18, none, puReduction),
			num("P(U) curve: knee voltage", 131, 0xFFFF, 0.01, 0, "V", 23000, 25500, 23800).needs(148, 0x18),
			num("P(U) curve: limit voltage", 132, 0xFFFF, 0.01, 0, "V", 23500, 26000, 24200).needs(148, 0x18),
		),
	),
}

// pageEurope. The two wait times have a negative scale on a 12-bit field:
// the stored field is the low 12 bits of (14 - seconds), so it is read
// back as two's complement (Signed). Not yet checked on a device. The
// decimals are those the original shows (its frequencies with three, its
// % Un values with two).
var pageEurope = &Page{
	Name: "Europe (EN 50549-1)",
	Params: params(
		group("",
			check("Use auxiliary input 1 as feed-in inhibit", 148, 0x4, none).inverted(),
			num("AC current cap for charging and feed-in", 169, 0xFF, 0.5, 0, "% of nominal current", 0, 200, 0),
			num("Cap on generated apparent power", 169, 0xFF00, 0.5, 0, "% of rated power", 0, 200, 0),
		),
		group("First connection",
			num("First connection: wait time", 166, 0xFFF, -1, -14, "s", -1, -1186, -46).signed(),
			num("First connection: power ramp time for 0-100 %", 165, 0xFFF, 0.5, 0, "s", 0, 2400, 0),
			num("First connection: lowest frequency allowed", 168, 0xFF, -0.025, -2000, "Hz", 200, 0, 20).decimals(3),
			num("First connection: highest frequency allowed", 168, 0xFF00, 0.025, 2000, "Hz", 0, 200, 4).decimals(3),
			num("First connection: lowest voltage allowed", 167, 0xFF, 0.25, 400, "% of Un", -88, 0, -60).signed().decimals(2),
			num("First connection: highest voltage allowed", 167, 0xFF00, 0.25, 400, "% of Un", 0, 120, 40).signed().decimals(2),
		),
		group("Reconnection after a protection trip",
			num("After trip: wait time before reconnecting", 130, 0xFFF, -1, -14, "s", -1, -1186, -46).signed(),
			num("After trip: power ramp time for 0-100 %", 129, 0xFFF, 0.5, 0, "s", 0, 2400, 1200),
			num("After trip: lowest frequency allowed to reconnect", 147, 0xFF, -0.025, -2000, "Hz", 200, 0, 20).decimals(3),
			num("After trip: highest frequency allowed to reconnect", 147, 0xFF00, 0.025, 2000, "Hz", 0, 200, 8).decimals(3),
			num("After trip: lowest voltage allowed to reconnect", 146, 0xFF, 0.25, 400, "% of Un", -88, 0, -60).signed().decimals(2),
			num("After trip: highest voltage allowed to reconnect", 146, 0xFF00, 0.25, 400, "% of Un", 0, 120, 40).signed().decimals(2),
		),
		// The 10-minute value is a signed byte whose low half is the top 4
		// bits of word 129 and whose high half the top 4 bits of word 130;
		// each stage-1 frequency trip delay is 12 bits, the top 4 bits of
		// word 131 or 132 above a byte of word 133.
		group("Interface (grid) protection, stage 1",
			num(overVoltage10Min, 129, 0xF000, 0.25, 400, "% of Un", 0, 100, 40).signed().high(130, 0xF000).decimals(2),
			num("Stage 1 under-voltage: trip level", 159, 0xFF, 0.25, 400, "% of Un", -88, 0, -60).signed().decimals(2),
			num("Stage 1 under-voltage: trip delay", 131, 0xFFF, 0.05, 0, "s", 0, 4000, 10),
			num("Stage 1 over-voltage: trip level", 159, 0xFF00, 0.25, 400, "% of Un", 0, 100, 60).signed().decimals(2),
			num("Stage 1 over-voltage: trip delay", 132, 0xFFF, 0.05, 0, "s", 0, 4000, 10),
			num("Stage 1 under-frequency: trip level", 160, 0xFF, -0.025, -2000, "Hz", 196, 0, 100).decimals(3),
			num("Stage 1 under-frequency: trip delay", 133, 0xFF, 0.05, 0, "s", 0, 4000, 600).high(131, 0xF000),
			num("Stage 1 over-frequency: trip level", 160, 0xFF00, 0.025, 2000, "Hz", 0, 196, 108).decimals(3),
			num("Stage 1 over-frequency: trip delay", 133, 0xFF00, 0.05, 0, "s", 0, 4000, 600).high(132, 0xF000),
		),
		group("Interface (grid) protection, stage 2",
			num("Stage 2 under-voltage: trip level", 161, 0xFF00, 0.25, 400, "% of Un", -89, 0, -80).signed().decimals(2),
			num("Stage 2 under-voltage: trip delay", 161, 0xFF, 0.05, 0, "s", 0, 130, 4),
			num("Stage 2 over-voltage: trip level", 162, 0xFF00, 0.25, 400, "% of Un", 0, 120, 80).signed().decimals(2),
			num("Stage 2 over-voltage: trip delay", 162, 0xFF, 0.05, 0, "s", 0, 130, 4),
			num("Stage 2 under-frequency: trip level", 163, 0xFF00, -0.025, -2000, "Hz", 200, 0, 120).decimals(3),
			num("Stage 2 under-frequency: trip delay", 163, 0xFF, 0.05, 0, "s", 0, 130, 4),
			num("Stage 2 over-frequency: trip level", 164, 0xFF00, 0.025, 2000, "Hz", 0, 200, 120).decimals(3),
			num("Stage 2 over-frequency: trip delay", 164, 0xFF, 0.05, 0, "s", 0, 130, 4),
		),
		group("Power-versus-frequency response",
			num("Over-frequency response: start frequency", 142, 0xFF00, 0.025, 2000, "Hz", 0, 200, 8).decimals(3),
			num("Over-frequency response: end frequency", 142, 0xFF, 0.025, 2000, "Hz", 0, 200, 8).decimals(3),
			num("Over-frequency response: start delay", 143, 0xF800, 0.1, 0, "s", 0, 31, 0).decimals(2),
			num("Over-frequency response: end delay", 143, 0x7FF, 0.5, 0, "s", 0, 1200, 60).decimals(2),
			num("Over-frequency response: droop", 144, 0xFF, 0.05, 0, "%", 20, 250, 100),
			num("Under-frequency response: start frequency", 140, 0xFF00, -0.025, -2000, "Hz", 200, 0, 8).decimals(3),
			num("Under-frequency response: end frequency", 140, 0xFF, -0.025, -2000, "Hz", 200, 0, 8).decimals(3),
			num("Under-frequency response: start delay", 141, 0xF800, 0.1, 0, "s", 0, 31, 0).decimals(2),
			num("Under-frequency response: end delay", 141, 0x7FF, 0.5, 0, "s", 0, 1200, 60).decimals(2),
			num("Under-frequency response: droop", 144, 0xFF00, 0.05, 0, "%", 20, 250, 100),
		),
		// The original disables the points and the filter time while the
		// response is "Not used".
		group("Power-versus-voltage response",
			choice("P(U) reduction method", 148, 0x18, none, map[int]string{
				0: "off", 1: "reduce feed-in relative to nominal power", 2: "reduce feed-in relative to present power",
			}),
			num("P(U) point 1: voltage", 135, 0xFF, 0.25, 400, "% of Un", -120, 120, -40).signed().decimals(2).needs(148, 0x18),
			choice("P(U) point 1 applies to", 135, 0x8000, 1, appliesTo).needs(148, 0x18),
			num("P(U) point 1: power limit", 135, 0x7F00, 1, 0, "%", 0, 100, 0).needs(148, 0x18),
			num("P(U) point 2: voltage", 136, 0xFF, 0.25, 400, "% of Un", -120, 120, -17).signed().decimals(2).needs(148, 0x18),
			choice("P(U) point 2 applies to", 136, 0x8000, none, appliesTo).needs(148, 0x18),
			num("P(U) point 2: power limit", 136, 0x7F00, 1, 0, "%", 0, 100, 100).needs(148, 0x18),
			num("P(U) point 3: voltage", 137, 0xFF, 0.25, 400, "% of Un", -120, 120, 35).signed().decimals(2).needs(148, 0x18),
			choice("P(U) point 3 applies to", 137, 0x8000, none, appliesTo).needs(148, 0x18),
			num("P(U) point 3: power limit", 137, 0x7F00, 1, 0, "%", 0, 100, 100).needs(148, 0x18),
			num("P(U) point 4: voltage", 138, 0xFF, 0.25, 400, "% of Un", -120, 120, 61).signed().decimals(2).needs(148, 0x18),
			choice("P(U) point 4 applies to", 138, 0x8000, none, appliesTo).needs(148, 0x18),
			num("P(U) point 4: power limit", 138, 0x7F00, 1, 0, "%", 0, 100, 20).needs(148, 0x18),
			num("P(U) set-point response time constant", 139, 0xFFFF, 7.276008090921e-05, 0, "s", 13744, 229, 4165).reciprocal().decimals(1).needs(148, 0x18),
		),
	),
}

var pageAUNZM = &Page{
	Name: "Australia/New Zealand, stand-alone (AS/NZS 4777.2:2020 appendix M)",
	Params: params(
		group("",
			check("Feed-in permitted", 129, 0x1, none).inverted(),
		),
		group("Power-versus-voltage response",
			// The original's defaults for the two voltages (90 V, 95.5 V)
			// lie outside their own range: left out as unreliable.
			num("P(U) point 1: voltage", 135, 0xFF, 0.5, 460, "V", -100, 0, none).signed(),
			num("P(U) point 1: charge power limit", 135, 0xFF00, 1, -128, "%", 128, 148, 148),
			num("P(U) point 2: voltage", 136, 0xFF, 0.5, 460, "V", -100, 0, none).signed(),
			// Fixed at 100 %: minimum and maximum are equal.
			num("P(U) point 2: charge power limit", 136, 0xFF00, 1, -128, "%", 228, 228, 228),
		),
	),
}

// pagePoland. The start frequency is the high byte of word 142; the
// original writes it to both bytes. The LFSM-O box is ticked unless that
// byte is 0xFF; unticking writes 0xFFFF, ticking the default start
// frequency (INFERRED: the original sets its default, and also the droop's,
// which is not modelled), and the two values are greyed while it is not
// ticked.
var pagePoland = &Page{
	Name: "Poland",
	Params: params(group("",
		check("Use auxiliary input 1 as feed-in inhibit", 148, 0x4, none).inverted(),
		polandLFSMO(),
		num("Over-frequency power reduction: start frequency", 142, 0xFF00, 0.025, 2000, "Hz", 8, 20, 8).decimals(3).twin(0xFF).lfsmo(),
		num("Over-frequency power reduction: droop", 144, 0xFF, 0.05, 0, "%", 20, 250, 100).lfsmo(),
	)),
}

// polandLFSMO is the Poland page's LFSM-O box.
func polandLFSMO() Param {
	p := check("Limited frequency-sensitive mode (over-frequency) enabled", 142, 0xFF00, none).twin(0xFF)
	p.Values = &[2]int{0xFF, 8}
	return p
}

// lfsmo makes p greyed while the Poland page's LFSM-O box is not ticked.
func (p Param) lfsmo() Param { p.NeedsWord, p.NeedsMask, p.NeedsOff = 142, 0xFF00, 0xFF; return p }

// twin makes the bits mask of p's word a copy of its field (Param.Twin).
func (p Param) twin(mask uint16) Param { p.Twin = mask; return p }

// pageMicrogrid uses words 129-149, which the grid-code pages use too, so
// its parameters and a grid code's parameters cannot both be meaningful.
// When the original shows it is not known. Its hidden diagnostic curve
// points (a panel the original never shows) are left out.
var pageMicrogrid = &Page{
	Name: "Microgrid",
	Params: params(
		group("",
			check("Run as microgrid", 129, 0x1, none).inverted(),
			check("Let assistants toggle microgrid mode", 129, 0x4, none).inverted(),
			check("Black start allowed by the unit", 129, 0x2, none),
			check("Retry a failed black start (at most 3 tries)", 129, 0x8, none),
			choice("Black-start retry delay mode", 129, 0x10, none, map[int]string{0: "fixed delay", 1: "random delay"}),
			// Stored as 255 - delay. The original gives no unit (seconds,
			// INFERRED).
			num("Fixed black-start retry delay", 129, 0xFF00, -1, -255, "", 255, 0, 255),
			num("Active power at reference frequency (P0)", 139, 0xFFFF, -0.09765625, 0, "% of Pref", 1024, -1024, 0).signed(),
			num("Lower active-power bound", 137, 0xFFFF, -0.09765625, 0, "% of Pref", 2048, -2048, -1024).signed(),
			num("Reference frequency for the P(f) droop (f0)", 134, 0xFFFF, hzStep, 0, "Hz", 38655, 55835, 43379),
			num("Upper active-power bound", 135, 0xFFFF, -0.09765625, 0, "% of Pref", 2048, -2048, 1024).signed(),
			num("Reactive power at reference voltage (Q0)", 149, 0xFFFF, 0.09765625, 0, "% of Qref", -717, 717, 0).signed(),
			// The original's defaults for the two reactive-power bounds
			// (-100 %, 100 %) lie outside their own range: left out.
			num("Lower reactive-power bound", 145, 0xFFFF, 0.09765625, 0, "% of Qref", -717, 717, none).signed(),
			num("Reference voltage for the Q(U) droop (U0)", 144, 0xFFFF, 0.01, 0, "V", 18000, 27000, 23000),
			num("Upper reactive-power bound", 147, 0xFFFF, 0.09765625, 0, "% of Qref", -717, 717, none).signed(),
		),
	),
	Unsupported: []string{
		"Droop: frequency versus active power",
		"Droop: voltage versus reactive power",
	},
}
