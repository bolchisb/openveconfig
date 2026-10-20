package gridcode

// Code is a grid code: a value of setting 81. The value is the number the
// original tool shows in front of each entry of its grid code list, not
// the entry's position in that list (reverse-engineered, INFERRED: with
// this reading the codes that need no password are exactly the three
// entries without a standard).
type Code uint16

// CodeInfo describes a grid code.
type CodeInfo struct {
	Code Code
	// Region is the country or region; "" for codes without a standard.
	// Region and Standard are worded as in the original tool's grid code
	// list.
	Region string
	// Standard is the public name of the standard; "none" for codes
	// without one.
	Standard string
	// Description is this project's own short description.
	Description string
}

var codes = []CodeInfo{
	{0, "", "none", "No grid code; feeding energy back to the grid is disabled"},
	{1, "", "none", "Grid connection without a standard; not compliant"},
	{2, "UK", "G83/2 August 2012, G59/3-1 August 2014", "UK rules for small and large generators"},
	{3, "Italy", "CEI 0-21, 2014-09 and CEI 0-21;V1, 2014-12", "Italian low-voltage connection rule"},
	{4, "Germany", "VDE-AR-N 4105:2011-08, external NS protection", "German low-voltage rule, external grid-protection device"},
	{5, "Germany", "VDE-AR-N 4105:2011-08, internal NS protection", "German low-voltage rule, built-in grid protection"},
	{6, "Australia", "AS/NZS 4777.2:2015", "2015 inverter rule"},
	{7, "New Zealand", "AS/NZS 4777.2:2015", "2015 inverter rule"},
	{8, "South Africa", "NRS 097-2-1:2017", "South African rule for small-scale embedded generation"},
	{9, "France", "UTE C15-712-1:2013-07", "French guide for PV installations"},
	{10, "Spain", "RD 1699/2011 | UNE 206007-1:2013", "Spanish connection decree"},
	{11, "Belgium", "C10/11", "Belgian connection rule"},
	{12, "Austria", "TOR-D4 V2.3:2016-7; No reactive power control", "Austrian rule, without reactive-power control"},
	{13, "Chile", "NTG-BT 2014", "Chilean low-voltage rule"},
	{14, "Romania", "TOR-D4 derivative", "Romanian variant of the Austrian rule"},
	{15, "New Zealand", "AS/NZS 4777.2:2015 (AC Neutral Path externally joined)", "As code 7, with the neutral path joined outside the unit"},
	{16, "Australia", "AS/NZS 4777.2:2015 (AC Neutral Path externally joined)", "As code 6, with the neutral path joined outside the unit"},
	{17, "Europe", "EN50549-1:2019", "Generic European rule"},
	{18, "Denmark", "DK1, Western Denmark (Technical regulation 3.3.1)", "Danish rule, western grid"},
	{19, "Denmark", "DK2, Eastern Denmark (Technical regulation 3.3.1)", "Danish rule, eastern grid"},
	{20, "Northern Ireland", "G99/NI April 2019", "Northern Irish generator rule"},
	{21, "", "none", "As code 1, with the neutral path joined outside the unit; not compliant"},
	// The original sets the standard of 22-25 at run time; not known.
	{22, "Australia A", "AS/NZS 4777.2 (edition not known, likely 2020)", "Regional setting A"},
	{23, "Australia B", "AS/NZS 4777.2 (edition not known, likely 2020)", "Regional setting B"},
	{24, "Australia C", "AS/NZS 4777.2 (edition not known, likely 2020)", "Regional setting C"},
	{25, "New Zealand", "AS/NZS 4777.2:2020", "2020 rule"},
	{26, "Australia A", "AS/NZS 4777.2:2020+A1+A2 stand alone app. M (with N-bypass)", "Stand-alone use with neutral bypass"},
	{27, "Australia B", "AS/NZS 4777.2:2020+A1+A2 stand alone app. M (with N-bypass)", "Stand-alone use with neutral bypass"},
	{28, "Australia C", "AS/NZS 4777.2:2020+A1+A2 stand alone app. M (with N-bypass)", "Stand-alone use with neutral bypass"},
	{29, "New Zealand", "AS/NZS 4777.2:2020+A1+A2 stand alone app. M (with N-bypass)", "Stand-alone use with neutral bypass"},
	{30, "Czech", "Czech 2024 PPDS type A1/A2", "Czech distribution rule"},
	{31, "UK", "G98/1 Amendment 6 (16 A max.)", "UK micro-generator rule, 16 A at most"},
	{32, "Poland", "Commission Regulation (EU) 2016/631; Wymogi ogólnego stosowania 18-12-2018; zasady wykorzysta certyfikatow v.1.2 (26/04/2021)", "Polish rule"},
}

// Codes returns the known grid codes in ascending order.
func Codes() []CodeInfo { return append([]CodeInfo(nil), codes...) }

// Lookup returns the description of grid code c.
func Lookup(c Code) (CodeInfo, bool) {
	for _, ci := range codes {
		if ci.Code == c {
			return ci, true
		}
	}
	return CodeInfo{}, false
}

// SelectableWithoutPassword reports whether grid code c can be chosen
// without a password on a device with the given firmware version: code 0
// always, code 1 from firmware revision 415, code 21 from revision 554
// (revision = firmware version mod 1000, as in vebus). Every other code
// needs a password. Whether leaving a code has a rule of its own is not
// known.
func SelectableWithoutPassword(c Code, firmware uint32) bool {
	rev := firmware % 1000
	switch c {
	case 0:
		return true
	case 1:
		return rev >= 415
	case 21:
		return rev >= 554
	}
	return false
}

// pageOf maps grid codes to their parameter page, nil for codes without
// one. The AS/NZS 4777.2:2015 codes (6, 7, 15, 16) are not here: their
// page follows the parameter-set version (PageFor). Reverse-engineered
// from the original's page choice.
var pageOf = map[Code]*Page{
	0: nil, 1: nil, 21: nil,
	2: pageUK, 20: pageUK, 31: pageUK,
	3: pageItaly,
	4: pageGermanyA, 5: pageGermanyB,
	8:  pageSouthAfrica,
	9:  pageFrance,
	10: pageSpain,
	11: pageBelgium,
	12: pageAustria,
	13: pageChile,
	14: pageRomania,
	17: pageEurope, 18: pageEurope, 19: pageEurope, 30: pageEurope,
	22: pageAUNZM, 23: pageAUNZM, 24: pageAUNZM, 25: pageAUNZM,
	26: pageAUNZM, 27: pageAUNZM, 28: pageAUNZM, 29: pageAUNZM,
	32: pagePoland,
}

// PageFor returns the parameter page of grid code c with parameter-set
// version v (negative: not known). known is false when the page is not
// known: for a code not in Codes, and for the AS/NZS 4777.2:2015 codes
// without a version. p is nil for a code without parameters (0, 1, 21)
// and when the page is not known.
func PageFor(c Code, v int) (p *Page, known bool) {
	switch c {
	case 6, 7, 15, 16:
		switch {
		case v < 0:
			return nil, false
		case v == 0:
			return pageAUNZ2015, true
		}
		return pageAUNZ2015B, true
	}
	p, known = pageOf[c]
	return p, known
}
