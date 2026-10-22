package wizard

import (
	"strings"
	"testing"

	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
)

func TestSpans(t *testing.T) {
	// The ESS welcome step: one label holding the two ends of a link.
	hl := asm.ListVal(asm.StrVal(`\IS\`), asm.StrVal(`\IL:http://ve3.nl/ess-assistant\`))
	got := Spans("Please read the %0:sESS manual%1:s for more.", []asm.Value{hl})
	want := []Span{
		{Text: "Please read the "},
		{Text: "ESS manual", Link: "http://ve3.nl/ess-assistant"},
		{Text: " for more."},
	}
	same(t, got, want)

	// Font style switches from the mark on.
	got = Spans(`\FS:1\Requirements:\FS:0\ and the rest`, nil)
	same(t, got, []Span{{Text: "Requirements:", Bold: true}, {Text: " and the rest"}})

	// A style we do not know reads as plain.
	got = Spans(`\FS:4\big\FS:0\`, nil)
	same(t, got, []Span{{Text: "big"}})

	// A place with no value is left as written, and %N:d works too.
	got = Spans("a %0:d b %9:s c", []asm.Value{asm.IntVal(7)})
	same(t, got, []Span{{Text: "a 7 b %9:s c"}})

	// A link that is never closed is ordinary text.
	got = Spans(`x \IS\y`, nil)
	same(t, got, []Span{{Text: "x "}, {Text: "y"}})

	if s := PlainText(`\FS:1\Note:\FS:0\ %0:s`, []asm.Value{asm.StrVal("ok")}); s != "Note: ok" {
		t.Errorf("PlainText = %q", s)
	}
	// A line of its own holding "_" is blank, and a line opens without
	// its leading dot.
	if s := PlainText("one\n_\n.two\n.", nil); s != "one\n\ntwo\n" {
		t.Errorf("line markup = %q", s)
	}
	// A line opening with ";" is the author's note, not for the user.
	if s := PlainText("keep\n; a note\n;-----\nalso", nil); s != "keep\nalso" {
		t.Errorf("comment lines = %q", s)
	}
	if n := len(Spans("", nil)); n != 0 {
		t.Errorf("empty text gives %d spans", n)
	}
}

// TestSpanMarks drops the marks that place and colour a step's text,
// alone and beside a style the program does show, and leaves a pair of
// backslashes that does not read as marks as it is written.
func TestSpanMarks(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{`\ABT,FS:4\other type BMS\FS:0\`, "other type BMS"},
		{`\FC:$00606060\(an explanation)`, "(an explanation)"},
		{`\DXI:10,TI,DX:10\0.005 C\DXI:10\= 48 V\TI\`, "0.005 C= 48 V"},
		{`a\FS:1,IN:10\b\FS:0,IN:0\c`, "abc"},
		{`a \DEPRECATED! b\ c`, `a \DEPRECATED! b\ c`},
	} {
		if s := PlainText(c.text, nil); s != c.want {
			t.Errorf("PlainText(%q) = %q, want %q", c.text, s, c.want)
		}
	}
	// A style still applies when it shares its pair with marks that do
	// not.
	got := Spans(`\ABT,FS:1\bold\FS:0,IN:10\plain`, nil)
	same(t, got, []Span{{Text: "bold", Bold: true}, {Text: "plain"}})
}

func same(t *testing.T, got, want []Span) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d spans %+v, want %d %+v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("span %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestChoices splits a step's text the way the shipped packages write it:
// a title and a prompt, then one dotted line per button, the last of them
// with an explanation of its own on the line after it.
func TestChoices(t *testing.T) {
	const text = "Battery system\n" +
		"Please select your system\n" +
		".System uses Gel or AGM batteries\n" +
		"." + `\FS:1\System uses LiFePo4 with other type BMS\FS:0\` + "\n" +
		"(This can be either a BMS connected via CAN bus or another.)\n" +
		";------------------------------------"
	body, choices := Choices(text, 2)
	if body != "Battery system\nPlease select your system" {
		t.Errorf("body = %q", body)
	}
	if len(choices) != 2 {
		t.Fatalf("%d choices: %q", len(choices), choices)
	}
	if choices[0] != "System uses Gel or AGM batteries" {
		t.Errorf("choice 0 = %q", choices[0])
	}
	if want := `\FS:1\System uses LiFePo4 with other type BMS\FS:0\` + "\n" +
		"(This can be either a BMS connected via CAN bus or another.)"; choices[1] != want {
		t.Errorf("choice 1 = %q, want %q", choices[1], want)
	}
	// The first line of a choice is what a plain list shows.
	if s := PlainText(strings.SplitN(choices[1], "\n", 2)[0], nil); s != "System uses LiFePo4 with other type BMS" {
		t.Errorf("first line = %q", s)
	}
	// A text that does not hold one dotted line per button is left whole:
	// a step whose single dotted line is its entry line, not a choice.
	for _, n := range []int{0, 1, 3} {
		if body, choices := Choices(text, n); body != text || choices != nil {
			t.Errorf("%d buttons: body %q, choices %q", n, body, choices)
		}
	}
}
