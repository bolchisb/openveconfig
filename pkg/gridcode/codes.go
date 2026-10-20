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
	// The original always sets the standard of 22-25 when settings are
	// loaded: this text, with a suffix that follows the grid-info table
	// (LookupFor).
	{22, "Australia A", "AS/NZS 4777.2:2020+A1+A2 stand alone app. M", "Regional setting A"},
	{23, "Australia B", "AS/NZS 4777.2:2020+A1+A2 stand alone app. M", "Regional setting B"},
	{24, "Australia C", "AS/NZS 4777.2:2020+A1+A2 stand alone app. M", "Regional setting C"},
	{25, "New Zealand", "AS/NZS 4777.2:2020+A1+A2 stand alone app. M", "2020 rule"},
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

// LookupFor returns grid code c as the original's grid code list shows it
// for settings with the grid-info table of model m, from a device with
// firmware version firmware: for some codes the standard follows the
// table (whether the code is in it, its parameter-set version, an edit
// mask, a flag of its entry) and the firmware. Where that cannot be told
// (m not known, or what the original does is not known), the standard is
// Lookup's. Reverse-engineered.
func LookupFor(c Code, m Model, firmware uint32) (CodeInfo, bool) {
	ci, ok := Lookup(c)
	if ok {
		if s := m.standard(c, firmware); s != "" {
			ci.Standard = s
		}
	}
	return ci, ok
}

// standard returns the standard the original's list shows for grid code
// c with model m and firmware, "" for Lookup's.
func (m Model) standard(c Code, firmware uint32) string {
	if !m.Known {
		return ""
	}
	listed, listedKnown := m.listed(c)
	v := m.version(c)
	switch c {
	case 2:
		in31, known31 := m.listed(31)
		mask, _ := m.mask(2, 148)
		switch {
		case !known31:
		case in31:
			return "G99/1 Amendment 8"
		case mask == 0:
		case v == 0:
			return "G98/1 March 2019, G99/1 May 2018"
		case v == 1:
			return "G98/1 Amendment 6, G99/1 Amendment 8"
		}
		// INFERRED: from version 2 the original leaves the entry as it
		// was, its built-in text when nothing changed it before.
	case 4, 5:
		if mask, _ := m.mask(4, 158); mask != 0 {
			side := "external"
			if c == 5 {
				side = "internal"
			}
			return "VDE-AR-N 4105:2018-11, " + side + " NS protection"
		}
	case 6, 7:
		return m.withSuffix(c, listed, listedKnown, " (AC Neutral path not externally joined)")
	case 9:
		if v == 1 || v == 2 {
			return "VDE V 0126-1-1 VFR 2019"
		}
	case 11:
		// Not in the table: left as it was (INFERRED: the built-in text).
		switch {
		case !listedKnown || !listed:
		case v == 0:
			return "C10/11:2012-04"
		default:
			return "C10/11 ed. 2.1:2019-09"
		}
	case 12:
		// The original asks this only while settings are loaded (INFERRED
		// from where it sets the flag it tests); the list is shown only then.
		// The version counts as 0 when the code is not in the table (and
		// is 0 without one).
		switch {
		case firmware%1000 < 450:
		case !listed || v == 0:
			return "TOR-D4 V2.3:2016-7"
		default:
			return "TOR-Erzeuger A V1.1:2019-12"
		}
	case 22, 23, 24, 25:
		return m.withSuffix(c, listed, listedKnown, " (no N-bypass)")
	}
	return ""
}

// withSuffix returns Lookup's standard of grid code c with suffix added
// when the original adds it: when c is in the table and its entry's flags
// are not there (format below 3) or have bit 15 clear. What the bit means
// is not known. "" when that cannot be told.
func (m Model) withSuffix(c Code, listed, known bool, suffix string) string {
	ci, _ := Lookup(c)
	switch {
	case !known:
		return ""
	case !listed:
		return ci.Standard
	}
	if e, _, _ := entryOf(m.Table, c); e.flags >= 0 && e.flags&0x8000 != 0 {
		return ci.Standard
	}
	return ci.Standard + suffix
}

// listed reports whether grid code c has an entry in the grid-info table
// of m that is not empty, the original's test of whether the device knows
// the code; known is false when that cannot be told. Without a table the
// original knows codes 0-4 and not those from 13; for 5-12 it depends on
// a flag of its own that is not known. A table of format 0 is not
// followed here. Reverse-engineered.
func (m Model) listed(c Code) (listed, known bool) {
	switch {
	case !m.Known:
		return false, false
	case m.Table == nil:
		return c < 5, c < 5 || c >= 13
	case m.Table[tableOffFormat] < 1:
		return false, false
	}
	e, ok, err := entryOf(m.Table, c)
	if err != nil {
		return false, false
	}
	return ok && e.used[0] != 0, true
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
// one. For the AS/NZS 4777.2:2015 codes (6, 7, 15, 16), France (9),
// Belgium (11) and Austria (12) the page follows the parameter-set
// version (PageFor). Reverse-engineered from the original's page choice.
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
// known: for a code not in Codes, and for a code whose page follows the
// version when that is not known. From version 2 France, and from
// version 1 Belgium and Austria, use the Europe page. p is nil for a code
// without parameters (0, 1, 21) and when the page is not known.
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
	case 9, 11, 12:
		from := 1
		if c == 9 {
			from = 2
		}
		switch {
		case v < 0:
			return nil, false
		case v >= from:
			return pageEurope, true
		}
	}
	p, known = pageOf[c]
	return p, known
}
