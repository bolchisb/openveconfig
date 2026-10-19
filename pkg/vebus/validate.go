package vebus

import (
	"fmt"
	"strconv"
	"strings"
)

// Severity is how serious an Issue is.
type Severity uint8

const (
	// Warning is shown to the user but does not block a write.
	Warning Severity = iota + 1
	// Error blocks a write unless the caller overrides validation.
	Error
)

func (s Severity) String() string {
	switch s {
	case Warning:
		return "warning"
	case Error:
		return "error"
	}
	return fmt.Sprintf("Severity(%d)", uint8(s))
}

// Issue is one validation finding about one variable. It is also the error
// type that SetRaw, SetDisplay and the Validate functions return for a
// rejected value.
type Issue struct {
	ID       ID
	Severity Severity
	// Rule is a short, stable key for the rule, for example "range".
	Rule string
	// Source cites where the rule comes from.
	Source string
	// Message is a complete sentence for the user, naming the variable.
	Message string
}

func (i *Issue) Error() string { return i.Message }

// ValidationError is returned by CheckWrite when validation fails.
type ValidationError struct {
	Issues []Issue // the Error-severity issues
}

func (e *ValidationError) Error() string {
	msgs := make([]string, len(e.Issues))
	for i, is := range e.Issues {
		msgs[i] = is.Message
	}
	return "vebus: validation failed: " + strings.Join(msgs, "; ")
}

// Rule sources.
const (
	srcDocLimits     = "MK2 Protocol 3.14 §7.3.8 (Minimum, Maximum)"
	srcDocScaling    = "MK2 Protocol 3.14 §7.3.8 (Scale)"
	srcDocRAMScaling = "MK2 Protocol 3.14 §7.3.9 (Scale)"
	srcDocFlags      = "MK2 Protocol 3.14 §7.3.13.3"
	srcRE            = "reverse-engineered, #6"
)

func (d *Device) issue(id ID, sev Severity, rule, src, format string, a ...any) *Issue {
	return &Issue{ID: id, Severity: sev, Rule: rule, Source: src, Message: fmt.Sprintf(format, a...)}
}

func (d *Device) rangeIssue(id ID, s *settingVar, value string, sev Severity) *Issue {
	return d.issue(id, sev, "range", srcDocLimits, "%s: %s is outside the allowed range %s to %s%s",
		d.label(id), value, num(s.info.Value(s.info.Min)), num(s.info.Value(s.info.Max)), d.unit(id))
}

// num formats a display value, dropping float noise such as
// 13.799999999999999.
func num(v float64) string { return strconv.FormatFloat(v, 'g', 10, 64) }

// ValidateDisplay checks a display value for id without setting it: the
// device's range (§7.3.8) and the step given by the scale. Use it while the
// user edits. The error is an *Issue whose message states the allowed
// range.
func (d *Device) ValidateDisplay(id ID, v float64) error {
	raw, err := d.displayToRaw(id, v)
	if err != nil {
		return err
	}
	return d.ValidateRaw(id, raw)
}

// ValidateRaw checks a raw value for id without setting it: a setting must
// lie within the device's Minimum and Maximum (§7.3.8); a flag word may
// change only bits the device supports (§7.3.13.3); a flag is 0 or 1.
func (d *Device) ValidateRaw(id ID, raw uint16) error {
	switch id.Kind {
	case Setting:
		s, ok := d.settings[id.Num]
		if !ok {
			return d.unsupported(id)
		}
		if is := d.checkSetting(id.Num, s, raw, Error); is != nil {
			return is
		}
		return nil
	case Flag:
		if !d.Supported(id) {
			return d.unsupported(id)
		}
		if raw > 1 {
			return d.issue(id, Error, "range", srcDocFlags, "%s: %d is not allowed: a flag is 0 or 1", d.label(id), raw)
		}
		return nil
	case RAM:
		if _, ok := d.ram[id.Num]; !ok {
			return d.unsupported(id)
		}
		return nil
	}
	return d.unsupported(id)
}

// checkSetting is the single-value check of a setting.
func (d *Device) checkSetting(id uint16, s *settingVar, raw uint16, sev Severity) *Issue {
	sid := SettingID(id)
	if d.cat.IsFlagWord(id) {
		var read uint16
		if s.hasRead {
			read = s.read
		}
		if bad := (raw ^ read) &^ s.info.Max; bad != 0 {
			return d.issue(sid, sev, "flag-unsupported", srcDocFlags,
				"%s: bits 0x%04X are not supported by the device and must not be changed (supported: 0x%04X)",
				d.label(sid), bad, s.info.Max)
		}
		return nil
	}
	if raw < s.info.Min || raw > s.info.Max {
		return d.rangeIssue(sid, s, num(s.info.Value(raw))+d.unit(sid), sev)
	}
	return nil
}

// severity is Error when any of ids was changed by the user, else Warning:
// a value the device already holds is reported but does not block writing
// other settings.
func (d *Device) severity(ids ...ID) Severity {
	for _, id := range ids {
		if d.Dirty(id) {
			return Error
		}
	}
	return Warning
}

// Validate checks the whole model: every value against the device's limits
// and the cross-setting rules below. Run it after edits, after loading a
// file, and before writing to a device (CheckWrite). A finding about a
// value the user changed is an Error; one about a value as read from the
// device is a Warning.
func (d *Device) Validate() []Issue {
	var out []Issue
	add := func(is *Issue) {
		if is != nil {
			out = append(out, *is)
		}
	}
	for _, id := range d.SettingIDs() {
		s := d.settings[id]
		if d.isNA(id, s, s.value) {
			continue
		}
		add(d.checkSetting(id, s, s.value, d.severity(SettingID(id))))
		if d.cat.IsFlagWord(id) {
			d.checkFlagWord(id, s, add)
		}
	}
	for _, r := range d.crossRules() {
		if !d.available(r.ids) {
			continue
		}
		bad, msg := r.check(d)
		if !bad {
			continue
		}
		sev := d.severity(r.ids...)
		if r.warnOnly {
			sev = Warning
		}
		add(&Issue{ID: r.ids[0], Severity: sev, Rule: r.key, Source: r.source, Message: msg})
	}
	return out
}

// checkFlagWord reports changed flags the document says must not be changed
// (§7.3.13.3): "Not promoted" options and unused flags, here any flag the
// catalogue does not name.
func (d *Device) checkFlagWord(word uint16, s *settingVar, add func(*Issue)) {
	if !s.hasRead {
		return
	}
	first, _ := d.cat.flagsIn(word)
	changed := (s.value ^ s.read) & s.info.Max
	for bit := uint16(0); bit < 16; bit++ {
		if changed>>bit&1 == 0 {
			continue
		}
		id := FlagID(first + bit)
		e, ok := d.cat.Entry(id)
		switch {
		case !ok:
			add(d.issue(id, Error, "flag-unknown", srcDocFlags,
				"%s (bit %d of %s): unknown flag; do not change unused flags", id, bit, d.label(SettingID(word))))
		case e.NotPromoted:
			add(d.issue(id, Error, "flag-not-promoted", srcDocFlags,
				"%s: a \"not promoted\" option must not be changed; changing it can damage the device", d.label(id)))
		}
	}
}

// available reports whether every id is supported and has a value.
func (d *Device) available(ids []ID) bool {
	for _, id := range ids {
		if !d.Supported(id) || d.NotApplicable(id) {
			return false
		}
		if id.Kind == Setting {
			if _, err := d.Absolute(id.Num); err != nil {
				return false
			}
		}
	}
	return true
}

// crossRule is a rule between variables. ids lists every variable the rule
// reads; the rule is skipped unless all are available. The issue is
// reported on ids[0].
type crossRule struct {
	key, source string
	ids         []ID
	warnOnly    bool
	check       func(d *Device) (bad bool, msg string)
}

func (d *Device) val(id ID) float64 {
	if id.Kind == Setting {
		v, _ := d.Absolute(id.Num)
		return v
	}
	v, _ := d.Display(id)
	return v
}

func (d *Device) on(n uint16) bool {
	v, _ := d.Flag(n)
	return v
}

// crossRules returns the cross-setting rules. The flag-inverse rules come
// from the catalogue (InverseOf); the others are fixed here, each with its
// source.
func (d *Device) crossRules() []crossRule {
	var rules []crossRule
	for _, e := range d.cat.entries {
		if e.Kind != Flag || e.InverseOf == nil {
			continue
		}
		a, b := e.Num, *e.InverseOf
		rules = append(rules, crossRule{
			key: "flag-inverse", source: srcDocFlags,
			ids: []ID{FlagID(a), FlagID(b)},
			check: func(d *Device) (bool, string) {
				return d.on(a) == d.on(b), fmt.Sprintf("%s must hold the inverse value of %s",
					d.label(FlagID(a)), d.label(FlagID(b)))
			},
		})
	}
	s := SettingID
	rules = append(rules,
		crossRule{
			// The original tool clamps float to absorption.
			key: "float-above-absorption", source: srcRE + ", OBSERVED",
			ids: []ID{s(3), s(2)},
			check: func(d *Device) (bool, string) {
				return d.val(s(3)) > d.val(s(2)), fmt.Sprintf("%s: %s%s must not be above %s (%s%s)",
					d.label(s(3)), num(d.val(s(3))), d.unit(s(3)), d.label(s(2)), num(d.val(s(2))), d.unit(s(2)))
			},
		},
		crossRule{
			// The original tool clamps the SoC restart level to the
			// shutdown level.
			key: "soc-restart-below-shutdown", source: srcRE + ", OBSERVED",
			ids: []ID{s(84), s(83)},
			check: func(d *Device) (bool, string) {
				return d.val(s(84)) < d.val(s(83)), fmt.Sprintf("%s: %s must not be below %s (%s)",
					d.label(s(84)), num(d.val(s(84))), d.label(s(83)), num(d.val(s(83))))
			},
		},
		crossRule{
			// The AC input connect levels: low is 44 + 45, high is 46 - 47
			// (§7.3.13.2). The original tool only warns.
			key: "ac-connect-levels", source: srcRE + ", OBSERVED (warning only)", warnOnly: true,
			ids: []ID{s(45), s(47)},
			check: func(d *Device) (bool, string) {
				lo, hi := d.val(s(45)), d.val(s(47))
				return lo >= hi, fmt.Sprintf("the AC input low connect level (%s%s, settings 44 + 45) is not below the high connect level (%s%s, settings 46 - 47)",
					num(lo), d.unit(s(45)), num(hi), d.unit(s(47)))
			},
		},
		crossRule{
			key: "repeated-absorption", source: srcRE + ", OBSERVED (warning only)", warnOnly: true,
			ids: []ID{s(7), s(8)},
			check: func(d *Device) (bool, string) {
				return d.val(s(7)) >= d.val(s(8)), fmt.Sprintf("%s (%s%s) is not shorter than %s (%s%s)",
					d.label(s(7)), num(d.val(s(7))), d.unit(s(7)), d.label(s(8)), num(d.val(s(8))), d.unit(s(8)))
			},
		},
		crossRule{
			// Flag 11 is storage mode per COMMUNITY FINDINGS.md §7.1.
			key: "lithium-storage-mode", source: srcRE + ", OBSERVED",
			ids: []ID{FlagID(11), FlagID(36)},
			check: func(d *Device) (bool, string) {
				return d.on(36) && d.on(11), fmt.Sprintf("%s must be off with %s", d.label(FlagID(11)), d.label(FlagID(36)))
			},
		},
		crossRule{
			key: "lithium-traction-curve", source: srcRE + ", OBSERVED",
			ids: []ID{FlagID(29), FlagID(36)},
			check: func(d *Device) (bool, string) {
				return d.on(36) && d.on(29), fmt.Sprintf("%s must be off with %s", d.label(FlagID(29)), d.label(FlagID(36)))
			},
		},
		crossRule{
			key: "lithium-charge-curve", source: srcRE + ", OBSERVED",
			ids: []ID{s(10), FlagID(36)},
			check: func(d *Device) (bool, string) {
				raw, _ := d.Raw(s(10))
				return d.on(36) && raw == chargeCurveAdaptiveBatterySafe, fmt.Sprintf("%s: value %d (%s) is not allowed with %s",
					d.label(s(10)), raw, d.choice(s(10), raw), d.label(FlagID(36)))
			},
		},
	)
	return rules
}

func (d *Device) choice(id ID, raw uint16) string {
	if e, ok := d.cat.Entry(id); ok && e.Choices[raw] != "" {
		return e.Choices[raw]
	}
	return "unknown"
}

// CheckWrite runs Validate and returns a *ValidationError holding the
// Error-severity issues, or nil. With override it always returns nil; only
// an explicit user override (the CLI's) should pass true. Call it before
// writing to a device.
func (d *Device) CheckWrite(override bool) error {
	if override {
		return nil
	}
	var errs []Issue
	for _, is := range d.Validate() {
		if is.Severity == Error {
			errs = append(errs, is)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return &ValidationError{Issues: errs}
}
