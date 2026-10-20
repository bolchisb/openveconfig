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
	{2, "United Kingdom", "G83/2 (2012), G59/3-1 (2014)", "UK rules for small and large generators"},
	{3, "Italy", "CEI 0-21 (2014-09 and V1 2014-12)", "Italian low-voltage connection rule"},
	{4, "Germany", "VDE-AR-N 4105:2011-08", "German low-voltage rule, external grid-protection device"},
	{5, "Germany", "VDE-AR-N 4105:2011-08", "German low-voltage rule, built-in grid protection"},
	{6, "Australia", "AS/NZS 4777.2:2015", "2015 inverter rule"},
	{7, "New Zealand", "AS/NZS 4777.2:2015", "2015 inverter rule"},
	{8, "South Africa", "NRS 097-2-1:2017", "South African rule for small-scale embedded generation"},
	{9, "France", "UTE C15-712-1:2013-07", "French guide for PV installations"},
	{10, "Spain", "RD 1699/2011, UNE 206007-1:2013", "Spanish connection decree"},
	{11, "Belgium", "C10/11", "Belgian connection rule"},
	{12, "Austria", "TOR-D4 V2.3 (2016)", "Austrian rule, without reactive-power control"},
	{13, "Chile", "NTG-BT 2014", "Chilean low-voltage rule"},
	{14, "Romania", "derived from TOR-D4", "Romanian variant of the Austrian rule"},
	{15, "New Zealand", "AS/NZS 4777.2:2015", "As code 7, with the neutral path joined outside the unit"},
	{16, "Australia", "AS/NZS 4777.2:2015", "As code 6, with the neutral path joined outside the unit"},
	{17, "Europe", "EN 50549-1:2019", "Generic European rule"},
	{18, "Denmark (DK1, west)", "Technical regulation 3.3.1", "Danish rule, western grid"},
	{19, "Denmark (DK2, east)", "Technical regulation 3.3.1", "Danish rule, eastern grid"},
	{20, "Northern Ireland", "G99/NI (April 2019)", "Northern Irish generator rule"},
	{21, "", "none", "As code 1, with the neutral path joined outside the unit; not compliant"},
	{22, "Australia, region A", "AS/NZS 4777.2 (edition not known, likely 2020)", "Regional setting A"},
	{23, "Australia, region B", "AS/NZS 4777.2 (edition not known, likely 2020)", "Regional setting B"},
	{24, "Australia, region C", "AS/NZS 4777.2 (edition not known, likely 2020)", "Regional setting C"},
	{25, "New Zealand", "AS/NZS 4777.2:2020", "2020 rule"},
	{26, "Australia, region A", "AS/NZS 4777.2:2020 + A1 + A2, appendix M", "Stand-alone use with neutral bypass"},
	{27, "Australia, region B", "AS/NZS 4777.2:2020 + A1 + A2, appendix M", "Stand-alone use with neutral bypass"},
	{28, "Australia, region C", "AS/NZS 4777.2:2020 + A1 + A2, appendix M", "Stand-alone use with neutral bypass"},
	{29, "New Zealand", "AS/NZS 4777.2:2020 + A1 + A2, appendix M", "Stand-alone use with neutral bypass"},
	{30, "Czech Republic", "PPDS 2024, type A1/A2", "Czech distribution rule"},
	{31, "United Kingdom", "G98/1 amendment 6", "UK micro-generator rule, 16 A at most"},
	{32, "Poland", "EU 2016/631 (RfG) with Polish requirements (2018, 2021)", "Polish rule"},
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
// one. Codes missing here use a page that is not known: 4 and 5 use one of
// the two Germany pages, 18 and 19 probably the Europe page, and nothing
// is known for 20, 30 and 31.
//
// Reverse-engineered, INFERRED: the original's rule for choosing the page
// is not known; this mapping follows the names of the codes and pages.
var pageOf = map[Code]*Page{
	0: nil, 1: nil, 21: nil,
	2: pageUK,
	3: pageItaly,
	6: pageAUNZ2015, 7: pageAUNZ2015, 15: pageAUNZ2015, 16: pageAUNZ2015,
	8:  pageSouthAfrica,
	9:  pageFrance,
	10: pageSpain,
	11: pageBelgium,
	12: pageAustria,
	13: pageChile,
	14: pageRomania,
	17: pageEurope,
	22: pageAUNZ2020, 23: pageAUNZ2020, 24: pageAUNZ2020, 25: pageAUNZ2020,
	26: pageAUNZM, 27: pageAUNZM, 28: pageAUNZM, 29: pageAUNZM,
	32: pagePoland,
}

// PageFor returns the parameter page of grid code c. known is false when
// the page is not known (including codes not in Codes); p is nil for a
// code without parameters (0, 1, 21) and when the page is not known.
func PageFor(c Code) (p *Page, known bool) {
	p, known = pageOf[c]
	return p, known
}
