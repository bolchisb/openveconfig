package assistant

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
	"github.com/bolchisb/openveconfig/pkg/assistant/vap"
	"github.com/bolchisb/openveconfig/pkg/assistant/wizard"
)

// synthOther is a second user assistant, so a folder has two to add.
const synthOther = `
Mark .AID Mark, EL, '0N 0 255 7'
 .AUE Mark, m
m .ALD RB1($+1)
 DATA 0x46, m
`

// addFolder writes a package folder with the two system assistants and
// two user ones, and returns it.
func addFolder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(file string, id uint16, src string) {
		p := &vap.Package{Format: 1, ID: id, Members: []vap.Member{
			{Name: "x.vll", Data: []byte(src)}, {Name: "x.en", Data: []byte("#Level\r\nHow high\r\n#Mark\r\nWhich mark\r\n")},
		}}
		if err := os.WriteFile(filepath.Join(dir, file), vap.Encode(p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("asfStart_0015.vap", 0x15, synthStart)
	write("asfStop_000E.vap", 0x0E, synthStop)
	write("User_0190.vap", 0x0190, synthUser)
	write("Other_0191.vap", 0x0191, synthOther)
	return dir
}

func TestAdd(t *testing.T) {
	dir := addFolder(t)
	labels := map[string]asm.Value{"big": asm.BoolVal(false)}

	// An empty image: the first assistant goes in on its own.
	bare, err := Load(dir, "en")
	if err != nil {
		t.Fatal(err)
	}
	bare.Labels = labels
	empty, err := bare.Build(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustLayout(t, empty.Bytes).Users(); len(got) != 0 {
		t.Fatalf("the empty image holds %v", got)
	}

	one, err := Add(dir, "en", empty.Bytes, 0x0190, labels,
		wizard.Answers{{Source: "User", Step: "level"}: {asm.IntVal(0x1234)}})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustLayout(t, one.Bytes).Users(); len(got) != 1 || got[0] != 0x0190 {
		t.Fatalf("after the first add: %v", got)
	}

	// A second assistant is added after it, and the first keeps its
	// answer: the image is read back rather than defaulted.
	two, err := Add(dir, "en", one.Bytes, 0x0191, labels,
		wizard.Answers{{Source: "Other", Step: "mark"}: {asm.IntVal(0x5A)}})
	if err != nil {
		t.Fatal(err)
	}
	got := mustLayout(t, two.Bytes).Users()
	if len(got) != 2 || got[0] != 0x0190 || got[1] != 0x0191 {
		t.Fatalf("after the second add: %v", got)
	}
	c, err := Load(dir, "en", got...)
	if err != nil {
		t.Fatal(err)
	}
	c.Labels = labels
	back, _, err := c.ReadBack(two.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if v := back[wizard.Key{Source: "User", Step: "level"}]; len(v) != 1 || v[0].I != 0x1234 {
		t.Errorf("the first assistant lost its answer: %v", v)
	}
	if v := back[wizard.Key{Source: "Other", Step: "mark"}]; len(v) != 1 || v[0].I != 0x5A {
		t.Errorf("the second assistant's answer: %v", v)
	}

	// Adding the same one twice is refused, and so is an unknown id.
	if _, err := Add(dir, "en", two.Bytes, 0x0190, labels, nil); !errors.Is(err, ErrAlreadyThere) {
		t.Errorf("adding twice: %v", err)
	}
	if _, err := Add(dir, "en", two.Bytes, 0x0999, labels, nil); err == nil {
		t.Error("an id the folder does not have was accepted")
	}
}

func TestAddTooBig(t *testing.T) {
	dir := addFolder(t)
	// The synthetic user assistant fills the image when big is set.
	labels := map[string]asm.Value{"big": asm.BoolVal(true)}
	bare, err := Load(dir, "en")
	if err != nil {
		t.Fatal(err)
	}
	bare.Labels = map[string]asm.Value{"big": asm.BoolVal(false)}
	empty, err := bare.Build(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Add(dir, "en", empty.Bytes, 0x0190, labels, nil); !errors.Is(err, ErrTooBig) {
		t.Errorf("an assistant that does not fit: %v", err)
	}
}

func mustLayout(t *testing.T, img []byte) *Layout {
	t.Helper()
	l, err := ParseLayout(img)
	if err != nil {
		t.Fatal(err)
	}
	return l
}
