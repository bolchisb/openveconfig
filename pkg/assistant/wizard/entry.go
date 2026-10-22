package wizard

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
)

// A step that asks for values writes them into its text: the line
// opening with "." keeps a place |N for the value of format N, with the
// words that name it before the place and the unit after it ("The
// battery capacity of the system is |0 Ah."). The line can carry
// further lines under it, one per row, as the dynamic cut-off curve of
// ESS does ("0.005 C= |0 V", "0.25 C= |1 V", …).
//
// Inferred (#33) from the shipped packages and checked against the
// steps of ESS: a step with n formats keeps the places |0 … |n-1 in
// that block, and no other line of a section holds a place.

// EntryLine is one line of a step's entry block: the places it keeps
// for values, and the texts around them. Parts always holds one text
// more than Places, so the line reads Parts[0], the value of Places[0],
// Parts[1], and so on. A line that keeps no place is prose.
type EntryLine struct {
	Places []int
	Parts  []string
}

// Prose reports whether the line asks for nothing.
func (l EntryLine) Prose() bool { return len(l.Places) == 0 }

var fieldRe = regexp.MustCompile(`\|(\d+)`)

// Entry splits a step's text into the part above its entry block and
// the lines of the block, which opens at the first line beginning with
// "." and runs to the end. Lines beginning with ";" are the author's
// comments and are dropped, as they are everywhere else in a text. With
// no such line the text is left whole and no line is returned.
func Entry(text string) (body string, lines []EntryLine) {
	var head []string
	var block []string
	for _, l := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(l, ";"):
		case strings.HasPrefix(l, "."):
			if block == nil {
				block = []string{l[1:]}
				continue
			}
			block = append(block, l[1:])
		case block != nil:
			block = append(block, l)
		default:
			head = append(head, l)
		}
	}
	if block == nil {
		return text, nil
	}
	for _, l := range block {
		lines = append(lines, parseEntryLine(l))
	}
	return strings.TrimRight(strings.Join(head, "\n"), "\n"), lines
}

// parseEntryLine finds the places of one line.
func parseEntryLine(l string) EntryLine {
	e := EntryLine{}
	from := 0
	for _, m := range fieldRe.FindAllStringSubmatchIndex(l, -1) {
		n, err := strconv.Atoi(l[m[2]:m[3]])
		if err != nil {
			continue
		}
		e.Parts = append(e.Parts, l[from:m[0]])
		e.Places = append(e.Places, n)
		from = m[1]
	}
	e.Parts = append(e.Parts, l[from:])
	return e
}

// titleMax is how long the line naming a step may be, and titleEnds the
// characters that end a sentence: the shipped packages open a section
// with a few words that name the step ("Battery system", "Dynamic
// cut-off"), never with a sentence, so a first line that runs on or
// closes like prose is prose and the step is left without a heading.
const (
	titleMax  = 48
	titleEnds = ".!?:,;"
)

// Title takes the heading off a step's text, with the step's values in
// its places. rest is what is left, and a text whose first line reads as
// prose is returned whole with no title.
func Title(text string, values []asm.Value) (title, rest string) {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) == "" || strings.HasPrefix(l, ";") {
			continue
		}
		t := strings.TrimSpace(PlainText(l, values))
		last, _ := utf8.DecodeLastRuneInString(t)
		if t == "" || len([]rune(t)) > titleMax || strings.ContainsRune(titleEnds, last) {
			return "", text
		}
		return t, strings.TrimRight(strings.Join(lines[i+1:], "\n"), "\n")
	}
	return "", text
}
