package wizard

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
)

// A step's text carries a little markup that the original works out
// before it shows the step (reverse-engineered, #32):
//
//   - %N:s and %N:d are places for the values of the step's labels, in
//     the order .AUE names them; a label holding a list fills as many
//     places as it has elements.
//   - \FS:n\ switches the font style from there on: 0 plain, 1 bold.
//     Other numbers are styles we do not know, and are read as plain.
//   - \IS\ opens a link and \IL:address\ closes it and gives its
//     address, so the text between the two is what the link shows. A
//     package names a link through a label, as ESS does with
//     hlManual = ['\IS\', '\IL:http://…\'].
//
// Anything else is left as written.

// Span is a run of a step's text with the styling its package asks for.
type Span struct {
	Text string
	Bold bool
	Link string // the address when this run is a link
}

var (
	placeRe = regexp.MustCompile(`%(\d+):[sd]`)
	markRe  = regexp.MustCompile(`\\(FS:\d+|IS|IL:[^\\]*)\\`)
)

// Spans returns the runs to show for a step's text, with the values of
// the step's labels put in their places. Runs are in reading order and
// none is empty.
func Spans(text string, values []asm.Value) []Span {
	var spans []Span
	bold, link, from := false, "", 0
	add := func(s string) {
		if s != "" {
			spans = append(spans, Span{Text: s, Bold: bold, Link: link})
		}
	}
	s := fillPlaces(lines(text), values)
	for _, m := range markRe.FindAllStringSubmatchIndex(s, -1) {
		add(s[from:m[0]])
		from = m[1]
		switch mark := s[m[2]:m[3]]; {
		case mark == "IS":
			// The address is only known at the closing mark, so the runs
			// between the two are marked when it arrives.
			link = openLink
		case strings.HasPrefix(mark, "IL:"):
			closeLink(spans, mark[len("IL:"):])
			link = ""
		default: // FS:n
			bold = mark == "FS:1"
		}
	}
	add(s[from:])
	// A link never closed shows as ordinary text.
	closeLink(spans, "")
	return spans
}

// openLink marks a run as being inside a link whose address has not been
// read yet; it is no address a package would give.
const openLink = "\x00open"

func closeLink(spans []Span, address string) {
	for i := range spans {
		if spans[i].Link == openLink {
			spans[i].Link = address
		}
	}
}

// lines takes the markup a text carries line by line: a line opening
// with ";" is a comment to the package's author and is dropped, a line
// of its own holding only "_" is a blank line, and a line opening with
// "." shows without it (inferred: the sources write all three in their
// thousands, what follows a dot is always ordinary prose, and what
// follows a semicolon reads as a note or a rule of dashes).
func lines(text string) string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(l, ";"):
			continue
		case strings.TrimRight(l, "\r") == "_":
			out = append(out, "")
		case strings.HasPrefix(l, "."):
			out = append(out, l[1:])
		default:
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// fillPlaces puts the values in their %N:s places. A value that is a
// list fills the places from N on, one element each, which is how a
// package gives the two ends of a link in one label. A place with no
// value is left as written.
func fillPlaces(text string, values []asm.Value) string {
	var flat []string
	for _, v := range values {
		if v.K == asm.List {
			for _, e := range v.L {
				flat = append(flat, e.String())
			}
			continue
		}
		flat = append(flat, v.String())
	}
	return placeRe.ReplaceAllStringFunc(text, func(m string) string {
		var n int
		if _, err := fmt.Sscanf(placeRe.FindStringSubmatch(m)[1], "%d", &n); err != nil || n >= len(flat) {
			return m
		}
		return flat[n]
	})
}

// PlainText is a step's text with its markup worked out and dropped, for
// somewhere that cannot show styling.
func PlainText(text string, values []asm.Value) string {
	var b strings.Builder
	for _, s := range Spans(text, values) {
		b.WriteString(s.Text)
	}
	return b.String()
}
