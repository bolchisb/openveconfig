package wizard

import (
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
