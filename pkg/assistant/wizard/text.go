package wizard

import (
	"fmt"
	"strconv"
	"strings"
)

// Text is a package's on-screen text for one language: sections, each
// opened by a line "#tag" (reverse-engineered, #8: a step's name and
// AssistantText('tag') select a section). What a section holds beyond
// plain lines (captions, radio labels) is not known; it is kept as written.
type Text map[string]string

// ParseText splits a text file into its sections. Lines before the first
// tag and empty lines at the end of a section are dropped.
func ParseText(b []byte) Text {
	t := Text{}
	tag := ""
	var body []string
	flush := func() {
		if tag != "" {
			t[strings.ToLower(tag)] = strings.TrimRight(strings.Join(body, "\n"), "\n")
		}
	}
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(l, "#") {
			flush()
			tag, body = strings.TrimSpace(l[1:]), nil
			continue
		}
		body = append(body, l)
	}
	flush()
	return t
}

// Section returns the text of a tag ("" if there is none).
func (t Text) Section(tag string) string { return t[strings.ToLower(tag)] }

// Format describes one numeric field of a step: a descriptor such as
// "2B 4800 5600 5000", whose first token is the number of decimals shown
// and a type letter, then minimum, maximum and default as raw integers
// (reverse-engineered, #8). Seen type letters: N (number), B (battery
// voltage, scaled to the unit's DC voltage by the source), L (with N; an
// enumeration), A (load). The raw value is the shown value × 10^Decimals
// (inferred).
type Format struct {
	Spec     string
	Decimals int
	Type     string // the letters after the decimals
	Min, Max int64
	Default  int64
	Extra    []string // tokens after the default, if any (not interpreted)
}

// ParseFormat decodes a format descriptor.
func ParseFormat(spec string) (Format, error) {
	f := Format{Spec: spec}
	tok := strings.Fields(spec)
	if len(tok) < 4 {
		return f, fmt.Errorf("format %q: want type, minimum, maximum and default", spec)
	}
	t := tok[0]
	if t != "" && t[0] >= '0' && t[0] <= '9' {
		f.Decimals, t = int(t[0]-'0'), t[1:]
	}
	f.Type = t
	var err error
	for i, p := range []*int64{&f.Min, &f.Max, &f.Default} {
		if *p, err = strconv.ParseInt(tok[i+1], 10, 64); err != nil {
			return f, fmt.Errorf("format %q: %v", spec, err)
		}
	}
	f.Extra = tok[4:]
	return f, nil
}

// Show returns raw as the user sees it, with the format's decimals.
func (f Format) Show(raw int64) string {
	if f.Decimals == 0 {
		return strconv.FormatInt(raw, 10)
	}
	sign := ""
	if raw < 0 {
		sign, raw = "-", -raw
	}
	p := int64(1)
	for range f.Decimals {
		p *= 10
	}
	return fmt.Sprintf("%s%d.%0*d", sign, raw/p, f.Decimals, raw%p)
}

// Parse returns the raw value of a shown value ("50.00", "50.2", "50"),
// checked against the limits.
func (f Format) Parse(s string) (int64, error) {
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > f.Decimals || whole == "" {
		return 0, fmt.Errorf("%q: at most %d decimals", s, f.Decimals)
	}
	frac += strings.Repeat("0", f.Decimals-len(frac))
	raw, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	if neg {
		raw = -raw
	}
	if raw < f.Min || raw > f.Max {
		return raw, fmt.Errorf("%s is outside %s … %s", f.Show(raw), f.Show(f.Min), f.Show(f.Max))
	}
	return raw, nil
}
