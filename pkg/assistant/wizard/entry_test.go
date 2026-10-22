package wizard

import (
	"testing"

	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
)

// TestEntry splits a step that asks for values the way the shipped
// packages write one: a heading, prose, then a dotted block whose lines
// name the values and keep a place for each.
func TestEntry(t *testing.T) {
	const text = "Dynamic cut-off\n" +
		"That is, the level depends on the discharge current.\n" +
		".Cut off voltage for a discharge current of:\n" +
		"0.005 C= |0 V\n" +
		"0.25 C= |1 V\n" +
		";------------------------------------"
	body, lines := Entry(text)
	if body != "Dynamic cut-off\nThat is, the level depends on the discharge current." {
		t.Errorf("body = %q", body)
	}
	if len(lines) != 3 {
		t.Fatalf("%d lines: %+v", len(lines), lines)
	}
	if !lines[0].Prose() || lines[0].Parts[0] != "Cut off voltage for a discharge current of:" {
		t.Errorf("line 0 = %+v, want the prose of the block", lines[0])
	}
	for i, want := range []struct {
		place  int
		label  string
		suffix string
	}{{0, "0.005 C= ", " V"}, {1, "0.25 C= ", " V"}} {
		l := lines[i+1]
		if len(l.Places) != 1 || l.Places[0] != want.place {
			t.Errorf("line %d places %v, want [%d]", i+1, l.Places, want.place)
			continue
		}
		if l.Parts[0] != want.label || l.Parts[1] != want.suffix {
			t.Errorf("line %d parts %q, want %q and %q", i+1, l.Parts, want.label, want.suffix)
		}
	}

	// A line can keep more than one place, and a text with no dotted
	// line is left whole.
	_, two := Entry(".from |0 to |1 Hz")
	if len(two) != 1 || len(two[0].Places) != 2 || len(two[0].Parts) != 3 {
		t.Errorf("two places: %+v", two)
	}
	if body, lines := Entry("just prose\nand more"); body != "just prose\nand more" || lines != nil {
		t.Errorf("without a dotted line: %q, %+v", body, lines)
	}
}

// TestTitle takes the heading off a step's text only when its first
// line names the step; a first line that reads as prose is left alone.
func TestTitle(t *testing.T) {
	title, rest := Title("Battery capacity\nPlease enter the correct capacity.", nil)
	if title != "Battery capacity" || rest != "Please enter the correct capacity." {
		t.Errorf("title %q, rest %q", title, rest)
	}
	// The places of a heading are filled, as they are in the body.
	if title, _ := Title("Step for %0:s", []asm.Value{asm.StrVal("ESS")}); title != "Step for ESS" {
		t.Errorf("title with a place = %q", title)
	}
	for _, text := range []string{
		"Which battery is it?",                     // a question is prose
		"The level to act at.",                     // so is a sentence
		"Read the manual, then press Next please.", // and so is a long line
		"A first line of more than forty-eight characters is prose",
	} {
		if title, rest := Title(text, nil); title != "" || rest != text {
			t.Errorf("Title(%q) = %q, %q; want no heading", text, title, rest)
		}
	}
}
