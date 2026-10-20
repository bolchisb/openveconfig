package main

import "github.com/bolchisb/openveconfig/pkg/gridcode"

// The grid page shows the names of package gridcode (pages, groups,
// parameters, choices, units and regions) through these tables, so that
// they are translated. The labels are the original tool's words: the text
// on screen where it is a whole label, otherwise the name the original
// shows when the value is edited (see docs/CLEAN-ROOM.md). Names without
// a counterpart keep this project's wording.

// gridLabels returns the labels of the gridcode names that read the same
// on every page.
func gridLabels() map[string]string {
	return map[string]string{
		// Pages.
		"United Kingdom (G83/G59 family)":                                       tr("UK grid code settings (G83/2, G59/3-1)"),
		"Italy (CEI 0-21)":                                                      tr("CEI grid code settings (CEI 0-21)"),
		"Germany, variant A (VDE-AR-N 4105, short form)":                        tr("VDE grid code settings (VDE-AR-N 4105, variant A)"),
		"Germany, variant B (VDE-AR-N 4105, full form)":                         tr("VDE grid code settings (VDE-AR-N 4105, variant B)"),
		"Australia/New Zealand (AS/NZS 4777.2:2015)":                            tr("AS/NZS 4777 grid code settings (AS/NZS 4777.2:2015)"),
		"Australia/New Zealand (AS/NZS 4777.2:2015, parameter set 1 and later)": tr("AS/NZS 4777 grid code settings (AS/NZS 4777.2:2015)"),
		"South Africa (NRS 097-2-1)":                                            tr("NRS097 grid code settings (NRS 097-2-1)"),
		"France (UTE C15-712-1)":                                                tr("UTE C15 grid code settings (UTE C15-712-1)"),
		"Spain (RD 1699/2011)":                                                  tr("RD1699 grid code settings (RD 1699/2011)"),
		"Belgium (C10/11)":                                                      tr("C10/11 grid code settings"),
		"Austria (TOR-D4)":                                                      tr("TOR-D4 grid code settings"),
		"Chile (NTG-BT)":                                                        tr("NTG-BT grid code settings"),
		"Romania (TOR-D4 derivative)":                                           tr("Romania grid code settings (TOR-D4 derivative)"),
		"Europe (EN 50549-1)":                                                   tr("European grid code settings (EN 50549-1)"),
		// The original sets this page's caption by grid code (Page.Title).
		"Australia/New Zealand, stand-alone (AS/NZS 4777.2:2020 appendix M)": tr("Australia/New Zealand, stand-alone (AS/NZS 4777.2:2020 appendix M)"),
		"Poland":    tr("Polish grid code settings (EU 2016/631)"),
		"Microgrid": tr("Microgrid related settings"),

		// Captions the original gives a page for some grid codes.
		"VFR 2019 grid code settings":                            tr("VFR 2019 grid code settings (VDE V 0126-1-1 VFR 2019)"),
		"TOR Erzeuger Typ A grid code settings":                  tr("TOR Erzeuger Typ A grid code settings"),
		"DK1 (Western Denmark)":                                  tr("DK1 (Western Denmark)"),
		"DK2 (Eastern Denmark)":                                  tr("DK2 (Eastern Denmark)"),
		"C10/11 grid code settings":                              tr("C10/11 grid code settings"),
		"Northern Ireland grid code settings":                    tr("Northern Ireland grid code settings (G99/NI)"),
		"UK G99 grid code settings":                              tr("UK G99 grid code settings"),
		"UK G98 grid code settings":                              tr("UK G98 grid code settings"),
		"Czech 2023 grid code settings":                          tr("Czech 2023 grid code settings"),
		"AS/NZS4777.2:2020 Australia A (Stand alone appendix M)": tr("AS/NZS4777.2:2020 Australia A (Stand alone appendix M)"),
		"AS/NZS4777.2:2020 Australia B (Stand alone appendix M)": tr("AS/NZS4777.2:2020 Australia B (Stand alone appendix M)"),
		"AS/NZS4777.2:2020 Australia C (Stand alone appendix M)": tr("AS/NZS4777.2:2020 Australia C (Stand alone appendix M)"),
		"AS/NZS4777.2:2020 New Zealand (Stand alone appendix M)": tr("AS/NZS4777.2:2020 New Zealand (Stand alone appendix M)"),
		gridcode.ReactivePowerUnknownLayout:                      tr("Reactive power (its layout depends on the device's grid-info table, which could not be read)"),

		// Groups.
		"Reactive power: cos phi as a function of active power": tr("Cos Phi as function of P"),
		"Reactive power: Q as a function of voltage":            tr("Q as function of input voltage"),
		"Reactive power: limits for the Q set-point":            tr("Limitation"),
		"Reactive power: common":                                tr("Reactive power"),
		"Volt-watt curve":                                       tr("Volt-Watt response curve"),
		"Sustained over-frequency":                              tr("Sustained high frequency operation"),
		"Power-versus-voltage curve":                            tr("P(U) functionality"),
		"Power-versus-voltage response":                         tr("P(U) response"),
		"Reconnection after a protection trip":                  tr("Reconnect after trip"),
		"First connection":                                      tr("Normal connect"),
		"Power-versus-frequency response":                       tr("P(f) functionality"),
		"Interface (grid) protection, stage 1":                  tr("IP protection, stage 1"),
		"Interface (grid) protection, stage 2":                  tr("IP protection, stage 2"),

		// Parameters.
		"Over-voltage trip on 10-minute running average":                                    tr("Over voltage U> (10 min. running mean)"),
		"Over-frequency power reduction: start frequency":                                   tr("Start frequency for P(f>) function"),
		"Over-frequency power reduction: start delay":                                       tr("Start delay of P(f) function"),
		"Over-frequency power reduction: droop":                                             tr("Droop f>"),
		"Use auxiliary input 1 as feed-in inhibit":                                          tr("Use AUX1 as disable FeedIn signal"),
		"Network-support mode: activation delay":                                            tr("Network service activation delay"),
		"Network-support mode: release band, lower frequency":                               tr("Stop network service LF threshold"),
		"Network-support mode: release band, upper frequency":                               tr("Stop network service HF threshold"),
		"Network-support mode: time inside release band":                                    tr("NS stop delay"),
		"Power limit PSMax (CEI 0-21 term)":                                                 tr("PSMax"),
		"Power limit PCMax (CEI 0-21 term)":                                                 tr("PCMax"),
		"Network-support mode: upper activation frequency":                                  tr("Start network service HF threshold"),
		"Droop paired with power limit PCMax":                                               tr("Droop for PCMax"),
		"Network-support mode: lower activation frequency":                                  tr("Start network service LF threshold"),
		"Droop paired with power limit PSMax":                                               tr("Droop for PSMax"),
		"AC current cap for charging and feed-in":                                           tr("Max AC current"),
		"Use auxiliary input 2 as 4.2 kW charge-power cap signal (German EnWG section 14a)": tr("Use AUX2 as 4.2kW charge limit signal (§14a)"),
		"cos-phi(P) point 1: active power":                                                  tr("Cos phi curve point 1"),
		"cos-phi(P) point 2: active power":                                                  tr("Cos phi curve point 2"),
		"cos-phi(P) point 3: active power":                                                  tr("Cos phi curve point 3"),
		"cos-phi(P) point 4: active power":                                                  tr("Cos phi curve point 4"),
		"cos-phi(P) point 1: cos phi":                                                       tr("Cos phi at point 1"),
		"cos-phi(P) point 2: cos phi":                                                       tr("Cos phi at point 2"),
		"cos-phi(P) point 3: cos phi":                                                       tr("Cos phi at point 3"),
		"cos-phi(P) point 4: cos phi":                                                       tr("Cos phi at point 4"),
		"Constant cos phi (fixed mode)":                                                     tr("Cos phi is"),
		"Voltage threshold to enable the curve (lock-in)":                                   tr("U Lock-in"),
		"Voltage threshold to disable the curve (lock-out)":                                 tr("U Lock-out"),
		"Apply voltage lock-in/lock-out":                                                    tr("Use lock-in/out"),
		"Q(U) point 1: voltage":                                                             tr("Reactive power curve point 1"),
		"Q(U) point 2: voltage":                                                             tr("Reactive power curve point 2"),
		"Q(U) point 3: voltage":                                                             tr("Reactive power curve point 3"),
		"Q(U) point 4: voltage":                                                             tr("Reactive power curve point 4"),
		"Q(U) point 1: reactive power":                                                      tr("Q at point 1"),
		"Q(U) point 2: reactive power":                                                      tr("Q at point 2"),
		"Q(U) point 3: reactive power":                                                      tr("Q at point 3"),
		"Q(U) point 4: reactive power":                                                      tr("Q at point 4"),
		"Constant reactive power (fixed mode)":                                              tr("Q is"),
		"Lowest cos phi allowed while following a Q set-point":                              tr("Min. Cos phi"),
		"Active-power threshold to enable the curve (lock-in)":                              tr("P Lock-in"),
		"Active-power threshold to disable the curve (lock-out)":                            tr("P Lock-out"),
		"Reactive-power response time constant":                                             tr("Filter time reactive power (1T)"),
		"Reactive-power control mode":                                                       tr("Reactive power regulation"),
		"Demand-response mode 0 enabled":                                                    tr("Enable DRM0 functionality"),
		"Power ramp: time for a 0-100 % change":                                             tr("Power rate limit (Wgra) 100% per"),
		"Volt-watt curve voltage 1":                                                         tr("Volt-Watt curve voltage V1"),
		"Volt-watt curve voltage 2":                                                         tr("Volt-Watt curve voltage V2"),
		"Volt-watt curve voltage 3":                                                         tr("Volt-Watt curve voltage V3"),
		"Volt-watt curve voltage 4":                                                         tr("Volt-Watt curve voltage V4"),
		"Volt-watt curve power at voltage 1":                                                tr("Volt-Watt curve level at V1"),
		"Volt-watt curve power at voltage 2":                                                tr("Volt-Watt curve level at V2"),
		"Volt-watt curve power at voltage 3":                                                tr("Volt-Watt curve level at V3"),
		"Volt-watt curve power at voltage 4":                                                tr("Volt-Watt curve level at V4"),
		"Volt-watt point 1 applies to":                                                      tr("Power at V1 is"),
		"Volt-watt point 2 applies to":                                                      tr("Power at V2 is"),
		"Volt-watt point 3 applies to":                                                      tr("Power at V3 is"),
		"Volt-watt point 4 applies to":                                                      tr("Power at V4 is"),
		"Sustained over-frequency: cut-off frequency":                                       tr("Fstop for sustained high frequency operation"),
		"Power ramp after reconnection: time for 0-100 %":                                   tr("Reconnect power rate. 100% per"),
		"P(U) curve: knee voltage":                                                          tr("U Knick"),
		"P(U) curve type":                                                                   tr("P(U) curve"),
		"P(U) curve: voltage span to zero power":                                            tr("U Grenz"),
		"P(U) curve: limit voltage":                                                         tr("U Grenz"),
		"P(U) curve: start voltage":                                                         tr("U Start"),
		"P(U) curve: end voltage":                                                           tr("U Stop"),
		"P(U) reduction method":                                                             tr("P(U) curve"),
		"Under-voltage trip delay":                                                          tr("Trip delay U<"),
		"Fast over-voltage trip delay":                                                      tr("Trip delay U>>"),
		"Reconnection wait time":                                                            tr("Reconnection time"),
		"Upper frequency allowed for reconnection":                                          tr("High frequency reconnect value"),
		"P(U) point 1: voltage":                                                             tr("Volt-Watt curve voltage V1"),
		"P(U) point 2: voltage":                                                             tr("Volt-Watt curve voltage V2"),
		"P(U) point 3: voltage":                                                             tr("Volt-Watt curve voltage V3"),
		"P(U) point 4: voltage":                                                             tr("Volt-Watt curve voltage V4"),
		"P(U) point 1: power limit":                                                         tr("Max power at V1"),
		"P(U) point 2: power limit":                                                         tr("Max power at V2"),
		"P(U) point 3: power limit":                                                         tr("Max power at V3"),
		"P(U) point 4: power limit":                                                         tr("Max power at V4"),
		"P(U) point 1 applies to":                                                           tr("Power at V1 is"),
		"P(U) point 2 applies to":                                                           tr("Power at V2 is"),
		"P(U) point 3 applies to":                                                           tr("Power at V3 is"),
		"P(U) point 4 applies to":                                                           tr("Power at V4 is"),
		"P(U) point 1: charge power limit":                                                  tr("Max charge power at V1"),
		"P(U) point 2: charge power limit":                                                  tr("Max charge power at V2"),
		"P(U) set-point response time constant":                                             tr("Filter time P(U) setpoint (1T)"),
		"After trip: wait time before reconnecting":                                         tr("Waiting time"),
		"After trip: highest frequency allowed to reconnect":                                tr("High frequency"),
		"After trip: power ramp time for 0-100 %":                                           tr("Power rate. 100% per"),
		"After trip: lowest frequency allowed to reconnect":                                 tr("Low frequency"),
		"After trip: highest voltage allowed to reconnect":                                  tr("High voltage"),
		"After trip: lowest voltage allowed to reconnect":                                   tr("Low voltage"),
		"First connection: wait time":                                                       tr("Waiting time"),
		"First connection: highest frequency allowed":                                       tr("High frequency"),
		"First connection: power ramp time for 0-100 %":                                     tr("Power rate. 100% per"),
		"First connection: lowest frequency allowed":                                        tr("Low frequency"),
		"First connection: highest voltage allowed":                                         tr("High voltage"),
		"First connection: lowest voltage allowed":                                          tr("Low voltage"),
		"Over-frequency response: start delay":                                              tr("Start delay f>"),
		"Over-frequency response: end delay":                                                tr("Stop delay f>"),
		"Over-frequency response: droop":                                                    tr("Droop f>"),
		"Over-frequency response: start frequency":                                          tr("Start freq f>"),
		"Over-frequency response: end frequency":                                            tr("Stop freq f>"),
		"Under-frequency response: start delay":                                             tr("Start delay f<"),
		"Under-frequency response: end delay":                                               tr("Stop delay f<"),
		"Under-frequency response: start frequency":                                         tr("Start freq f<"),
		"Under-frequency response: end frequency":                                           tr("Stop freq f<"),
		"Under-frequency response: droop":                                                   tr("Droop f<"),
		"Cap on generated apparent power":                                                   tr("Limit generated apparent power to"),
		"Stage 1 under-voltage: trip delay":                                                 tr("Trip delay U<"),
		"Stage 1 over-voltage: trip delay":                                                  tr("Trip delay U>"),
		"Stage 1 under-frequency: trip delay":                                               tr("Trip delay f<"),
		"Stage 1 over-frequency: trip delay":                                                tr("Trip delay f>"),
		"Stage 1 under-voltage: trip level":                                                 tr("U<"),
		"Stage 1 over-voltage: trip level":                                                  tr("U>"),
		"Stage 1 over-frequency: trip level":                                                tr("f>"),
		"Stage 1 under-frequency: trip level":                                               tr("f<"),
		"Stage 2 under-voltage: trip delay":                                                 tr("Trip delay U<"),
		"Stage 2 over-voltage: trip delay":                                                  tr("Trip delay U>"),
		"Stage 2 under-frequency: trip delay":                                               tr("Trip delay f<"),
		"Stage 2 over-frequency: trip delay":                                                tr("Trip delay f>"),
		"Stage 2 under-voltage: trip level":                                                 tr("U<"),
		"Stage 2 over-voltage: trip level":                                                  tr("U>"),
		"Stage 2 over-frequency: trip level":                                                tr("f>"),
		"Stage 2 under-frequency: trip level":                                               tr("f<"),
		"Feed-in permitted":                                                                 tr("Allow feed-in"),
		"Limited frequency-sensitive mode (over-frequency) enabled":                         tr("Enable LFSM-O mode"),
		"Black-start retry delay mode":                                                      tr("Retry delay type"),
		"Reactive power at reference voltage (Q0)":                                          tr("Q0"),
		"Reference voltage for the Q(U) droop (U0)":                                         tr("U0"),
		"Active power at reference frequency (P0)":                                          tr("P0"),
		"Reference frequency for the P(f) droop (f0)":                                       tr("f0"),
		"Upper active-power bound":                                                          tr("PMax"),
		"Lower active-power bound":                                                          tr("PMin"),
		"Lower reactive-power bound":                                                        tr("QMin"),
		"Upper reactive-power bound":                                                        tr("QMax"),
		"Black start allowed by the unit":                                                   tr("Allow black start"),
		"Let assistants toggle microgrid mode":                                              tr("Allow assistants to enable/disable microgrid"),
		"Retry a failed black start (at most 3 tries)":                                      tr("Allow black start retries (max. 3 times)"),
		"Fixed black-start retry delay":                                                     tr("Delay before retrying a black start"),
		"Run as microgrid":                                                                  tr("Enable microgrid operation"),
		"Droop: frequency versus active power":                                              tr("Frequency droop"),
		"Droop: voltage versus reactive power":                                              tr("Voltage droop"),

		// Choices.
		"inverting (feed-in)":              tr("Invert"),
		"charging":                         tr("Charge"),
		"off":                              tr("Not used"),
		"curve type A":                     tr("Type A"),
		"curve type B":                     tr("Type B"),
		"reduce relative to nominal power": tr("Reduce from Pnominal"),
		"reduce relative to present power": tr("Reduce from Pactual"),
		"reduce feed-in relative to nominal power": tr("Feed In reduced from Pnominal"),
		"reduce feed-in relative to present power": tr("Feed In reduced from Pactual"),
		"constant cos phi":                         tr("Use a fixed Cos Phi"),
		"cos phi follows active power":             tr("Cos Phi as function of P"),
		"constant Q":                               tr("Use a fixed Q"),
		"Q follows grid voltage":                   tr("Q as function of input voltage"),
		"fixed delay":                              tr("Use specified retry delay"),
		"random delay":                             tr("Use random retry delay"),

		// The cells of the fixed protection tables (gridcode.Page.Table)
		// that are words.
		"Protection function": tr("Protection function"),
		"Trip setting":        tr("Trip setting"),
		"Trip delay":          tr("Trip delay"),
		"LOM detection":       tr("LOM detection"),
		"see LOM setting":     tr("see LOM setting"),
		"Reconnection time":   tr("Reconnection time"),
		"under voltage":       tr("under voltage"),
		"over voltage":        tr("over voltage"),
		"under freq.":         tr("under freq."),
		"over freq.":          tr("over freq."),

		// Units with words.
		"seconds":              tr("seconds"),
		"% of nominal current": tr("% of INom"),
		"% of Un":              tr("% Un"),
		"% of nominal voltage": tr("% of Un"),
		"% of Pemax":           tr("% of Pemax"),
		"% of Sr":              tr("% of Sr"),
		"% of rated power":     tr("% of rated power"),
		"% of Pref":            tr("% of Pref"),
		"% of Qref":            tr("% of Qref"),
	}
}

// gridCodeLabels returns the labels of the regions and descriptions of
// the grid code list (gridcode.CodeInfo).
func gridCodeLabels() map[string]string {
	return map[string]string{
		"No grid code; feeding energy back to the grid is disabled":               tr("None: (feeding energy from DC to grid not allowed)"),
		"Grid connection without a standard; not compliant":                       tr("Other: not compliant to any grid code standard"),
		"As code 1, with the neutral path joined outside the unit; not compliant": tr("Other: AC Neutral path externally joined (not compliant)"),
		"UK":               tr("UK"),
		"Italy":            tr("Italy"),
		"Germany":          tr("Germany"),
		"Australia":        tr("Australia"),
		"Australia A":      tr("Australia A"),
		"Australia B":      tr("Australia B"),
		"Australia C":      tr("Australia C"),
		"New Zealand":      tr("New Zealand"),
		"South Africa":     tr("South Africa"),
		"France":           tr("France"),
		"Spain":            tr("Spain"),
		"Belgium":          tr("Belgium"),
		"Austria":          tr("Austria"),
		"Chile":            tr("Chile"),
		"Romania":          tr("Romania"),
		"Europe":           tr("Europe"),
		"Denmark":          tr("Denmark"),
		"Northern Ireland": tr("Northern Ireland"),
		"Czech":            tr("Czech"),
		"Poland":           tr("Poland"),
	}
}

// gridPageLabels returns the labels that differ between pages, by page
// name: the original words the same parameter or choice differently there.
func gridPageLabels() map[string]map[string]string {
	return map[string]map[string]string{
		"United Kingdom (G83/G59 family)": {
			"Over-frequency power reduction: start frequency": tr("Start frequency for P(f) function"),
			"Over-frequency power reduction: droop":           tr("Droop setting for P(f) function"),
			"Use auxiliary input 1 as feed-in inhibit":        tr("Use AUX1 (if available) as disable FeedIn signal"),
		},
		"Germany, variant A (VDE-AR-N 4105, short form)": {
			"Use auxiliary input 1 as feed-in inhibit": tr("Use AUX1 (if available) as disable FeedIn signal"),
		},
		"Australia/New Zealand (AS/NZS 4777.2:2015, parameter set 1 and later)": {
			"Volt-watt curve power at voltage 1": tr("Max power at V1"),
			"Volt-watt curve power at voltage 2": tr("Max power at V2"),
			"Volt-watt curve power at voltage 3": tr("Max power at V3"),
			"Volt-watt curve power at voltage 4": tr("Max power at V4"),
		},
		"Romania (TOR-D4 derivative)": {
			"reduce relative to nominal power": tr("(a) reduce from Pnominal"),
			"reduce relative to present power": tr("(b) reduce from Pactual"),
			"P(U) curve: knee voltage":         tr("UKnick"),
			"P(U) curve: limit voltage":        tr("UGrenz"),
		},
		"Europe (EN 50549-1)": {
			"P(U) reduction method": tr("P(U) response"),
		},
		// The Europe page as Austria from version 1 shows it.
		"TOR Erzeuger Typ A grid code settings": {
			"P(U) reduction method":                    tr("P(U) response"),
			"reduce feed-in relative to nominal power": tr("Feed In reduced from Pnominal (= typ a)"),
			"reduce feed-in relative to present power": tr("Feed In reduced from Pactual (= typ b)"),
		},
	}
}

// gridText returns the label of name, a gridcode name, on page p (nil for
// a name that belongs to no page). A name without a label is returned as
// it is.
func gridText(p *gridcode.Page, name string) string {
	if p != nil {
		// A page shown under the caption of a grid code may word things
		// differently there.
		if l, ok := gridPageLabels()[p.Title][name]; ok && p.Title != "" {
			return l
		}
		if l, ok := gridPageLabels()[p.Name][name]; ok {
			return l
		}
	}
	if l, ok := gridLabels()[name]; ok {
		return l
	}
	return name
}

// gridCodeText returns the label of a region or description of the grid
// code list.
func gridCodeText(s string) string {
	if l, ok := gridCodeLabels()[s]; ok {
		return l
	}
	return s
}
