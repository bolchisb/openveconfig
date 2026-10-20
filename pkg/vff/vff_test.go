package vff

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bolchisb/openveconfig/internal/vfftest"
)

// The fixtures are synthetic (internal/vfftest). No Victron firmware is
// committed (#2).

func TestParseFormat7(t *testing.T) {
	f, err := Parse(vfftest.File7())
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Parts) != 3 || len(f.Warnings) != 0 {
		t.Fatalf("%d parts, warnings %v", len(f.Parts), f.Warnings)
	}
	m := f.Parts[0]
	if m.Format != 7 || m.MagicEncoded || !m.Main() || m.Meta.Get("device") != "Multi Test 12V" || m.Meta.Get("Foo") != "bar" ||
		len(m.EEPROM) != 2 || len(m.Code) != 3 || m.Descriptor[0] != 'C' || len(m.Descriptor) != 33 {
		t.Errorf("part 1: %+v", m)
	}
	if v, ok := f.Version(); !ok || v != 2623457 {
		t.Errorf("version %d %v", v, ok)
	}
	g := f.Groups()
	if len(g) != 2 || len(g[0]) != 2 || g[0][1].Name() != "Bootblock" || len(g[1]) != 1 {
		t.Errorf("groups %v", g)
	}
	if end := f.Parts[2].Offset + f.Parts[2].Length; end != len(vfftest.File7()) {
		t.Errorf("parts end at %d", end)
	}
}

func TestParseFormat1(t *testing.T) {
	f, err := Parse(vfftest.File1(6))
	if err != nil {
		t.Fatal(err)
	}
	p := f.Parts[0]
	if len(f.Parts) != 1 || !p.MagicEncoded || !p.Main() || len(p.EEPROM) != 4 || len(p.Code) != 2 ||
		p.Meta.Int("CL") != 2 || p.Meta.Int("EL") != 4 || len(p.Descriptor) != 39 || len(p.ML) != 1 {
		t.Errorf("part %+v", p)
	}
	if _, ok := f.Version(); ok {
		t.Error("a format-1 file has no Version")
	}
	// A partial block at the end.
	if _, err := Parse(vfftest.File1(6)[:len(vfftest.File1(6))-1]); !errors.Is(err, ErrTruncated) {
		t.Errorf("partial block: %v", err)
	}
}

func TestParseFormat2(t *testing.T) {
	b := vfftest.Header(2, false)
	var data []byte
	for i := range 3 {
		data = append(data, vfftest.Blob(vfftest.BlockLen, byte(i))...)
	}
	var sum uint32
	for _, c := range data {
		sum += uint32(c)
	}
	b = binary.LittleEndian.AppendUint32(b, sum)
	b = vfftest.Rec(b, []byte("Part=Main Version=1120121 EL=1 CL=2"), true, true)
	b = vfftest.Rec(b, []byte("Cabc"), true, true)
	b = vfftest.Rec(b, nil, true, true)
	f, err := Parse(append(b, data...))
	if err != nil {
		t.Fatal(err)
	}
	if p := f.Parts[0]; len(p.EEPROM) != 1 || len(p.Code) != 2 || string(p.Descriptor) != "Cabc" {
		t.Errorf("part %+v", p)
	}
}

func TestParseRejects(t *testing.T) {
	good := vfftest.File7()
	codeAt := len(good) - (2+73+1)*1 // the last record of the file: Code of part 3
	for name, tt := range map[string]struct {
		mutate func([]byte) []byte
		want   error
	}{
		"payload byte": {func(b []byte) []byte { b[codeAt+5]++; return b }, &RecordError{}},
		"payload and check byte": {func(b []byte) []byte {
			b[codeAt+5]++
			b[len(b)-1]++
			return b
		}, &ChecksumError{}},
		"truncated": {func(b []byte) []byte { return b[:len(b)-10] }, ErrTruncated},
		"bad magic": {func(b []byte) []byte { b[3] = 'x'; return b }, ErrNotVFF},
		"format 8":  {func(b []byte) []byte { b[30] = 8; return b }, ErrNewer},
		"format 0":  {func(b []byte) []byte { b[30] = 0; return b }, ErrFormat},
		"trailing":  {func(b []byte) []byte { return append(b, 1, 2, 3) }, ErrNotVFF},
		"empty":     {func([]byte) []byte { return nil }, ErrNotVFF},
		"bootblock first": {func([]byte) []byte {
			return vfftest.Part3(vfftest.Spec3{Format: 7, Meta: "Part=Bootblock,Version=115,CL=1", Desc: []byte("C"), CL: vfftest.Records(1, 73, 30)})
		}, ErrFirstNotMain},
		"fewer records than declared": {func([]byte) []byte {
			b := vfftest.Part3(vfftest.Spec3{Format: 7, Meta: "Part=Main,Version=1,EL=0,CL=3", Desc: []byte("C"), CL: vfftest.Records(2, 73, 30)})
			return b
		}, ErrTruncated},
	} {
		b := tt.mutate(append([]byte(nil), good...))
		_, err := Parse(b)
		switch want := tt.want.(type) {
		case *RecordError:
			var re *RecordError
			if !errors.As(err, &re) || re.Part != 2 {
				t.Errorf("%s: %v, want a record error in part 3", name, err)
			}
		case *ChecksumError:
			var ce *ChecksumError
			if !errors.As(err, &ce) || ce.Part != 2 {
				t.Errorf("%s: %v, want a checksum error in part 3", name, err)
			}
		default:
			if !errors.Is(err, want) {
				t.Errorf("%s: %v, want %v", name, err, want)
			}
		}
	}
}

func TestWarnings(t *testing.T) {
	b := vfftest.Part3(vfftest.Spec3{Format: 7, Meta: "Part=Main,EL=0,CL=1,odd", Desc: []byte("C"), CL: vfftest.Records(1, 73, 1)})
	f, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	w := strings.Join(f.Warnings, "\n")
	if !strings.Contains(w, `"odd" has no '='`) || !strings.Contains(w, "no Version") {
		t.Errorf("warnings %v", f.Warnings)
	}
	// An encoded magic with a wrong check byte is only a warning.
	g := vfftest.File1(5)
	g[29]++
	f, err = Parse(g)
	if err != nil || len(f.Warnings) != 1 {
		t.Errorf("encoded magic check byte: %v, %v", err, f)
	}
}

func TestParseMeta(t *testing.T) {
	m := parseMeta(`Part=Main,"Device=MK2 20Mhz" X="a""b" ,Empty=`, nil)
	for k, want := range map[string]string{"part": "Main", "Device": "MK2 20Mhz", "Empty": "", "Missing": ""} {
		if got := m.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if !m.Has("empty") || m.Has("Missing") || len(m) != 4 {
		t.Errorf("items %v", m)
	}
	if m := parseMeta(`"Q=a""b"`, nil); m.Get("Q") != `a"b` {
		t.Errorf("quoted quote: %q", m.Get("Q"))
	}
}

// TestRealFiles validates the .vff files under $VFF_TESTDATA, if set, for
// example a VictronConnect firmware cache. Firmware files are not
// committed (#2). It logs no identifiers beyond the counts.
func TestRealFiles(t *testing.T) {
	dir := os.Getenv("VFF_TESTDATA")
	if dir == "" {
		t.Skip("VFF_TESTDATA not set")
	}
	n := 0
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".vff") {
			return err
		}
		n++
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		f, err := Parse(data)
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(p), err)
			return nil
		}
		if len(f.Warnings) > 0 {
			t.Logf("%s: %v", filepath.Base(p), f.Warnings)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no .vff files under VFF_TESTDATA")
	}
	t.Logf("%d files", n)
}
