package rvsc_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bolchisb/openveconfig/internal/rvsctest"
	"github.com/bolchisb/openveconfig/pkg/assistant"
	"github.com/bolchisb/openveconfig/pkg/rvsc"
	"github.com/bolchisb/openveconfig/pkg/vebus/system"
)

// backup is a synthetic .rvms of n standalone units with firmware 2699481.
func backup(t *testing.T, n int) []byte {
	t.Helper()
	f := rvsctest.File{ToolVersion: "0.01", InfoFirmware: 2699481, InfoTail: []byte{0x13, 0x37}}
	for i := range n {
		f.Units = append(f.Units, rvsctest.Unit{
			LongID: 0x10000001 + uint32(i), Firmware: 2699481, AssistantFlag: 0xe0,
			Serial: "HQ0000000" + string(rune('A'+i)), SaveTime: 1700000000,
			Settings: []uint16{1, 2, 3}, Assistant: []byte{0x00, 0x0b},
			Identity: &rvsctest.Identity{Letter: 0, ConfigProtocol: 1, ResponseChar: 0x02},
		})
	}
	return f.Bytes()
}

// layoutOf returns a layout of the file's units with each unit i in cells[i].
func layoutOf(t *testing.T, f *rvsc.File, s system.Setup, cells ...system.Cell) *system.Layout {
	t.Helper()
	units, err := f.SystemUnits()
	if err != nil {
		t.Fatal(err)
	}
	l := system.NewLayout()
	if err := l.Reshape(s, 1); err != nil {
		t.Fatal(err)
	}
	for i, u := range units {
		l.Add(u)
		if i < len(cells) {
			if err := l.Place(u, cells[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	return l
}

func TestSetLayout(t *testing.T) {
	l1, l2, l3 := system.Cell{Phase: 0}, system.Cell{Phase: 1}, system.Cell{Phase: 2}
	for _, tt := range []struct {
		name  string
		n     int
		setup system.Setup
		cells []system.Cell
	}{
		{"parallel", 2, system.SinglePhase, []system.Cell{l1, l1}},
		{"three phase", 3, system.ThreePhase, []system.Cell{l1, l2, l3}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := backup(t, tt.n)
			f, err := rvsc.Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			l := layoutOf(t, f, tt.setup, tt.cells...)
			if err := f.SetLayout(l); err != nil {
				t.Fatal(err)
			}
			out, err := f.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			g, err := rvsc.Parse(out)
			if err != nil {
				t.Fatal(err)
			}
			// It loads back with the same layout.
			units, _ := g.SystemUnits()
			back, bad := system.FromRecords(units)
			if bad != nil {
				t.Fatalf("records do not agree: %s", bad.Detail)
			}
			if back.Setup != tt.setup {
				t.Errorf("setup %v, want %v", back.Setup, tt.setup)
			}
			for i, u := range units {
				c, _, _ := back.CellOf(u)
				if c != tt.cells[i] {
					t.Errorf("unit %d in %v, want %v", i, c, tt.cells[i])
				}
			}
			// Only the record bytes changed.
			old, _ := rvsc.Parse(data)
			ou, _ := old.Units()
			nu, _ := g.Units()
			for i := range ou {
				a, b := old.Sections[i+2].Payload, g.Sections[i+2].Payload
				rec := ou[i].SystemRecord()
				at := bytes.Index(a, rec[:])
				if len(a) != len(b) || !bytes.Equal(a[:at], b[:at]) || !bytes.Equal(a[at+9:], b[at+9:]) {
					t.Errorf("unit %d: bytes other than the record changed", i)
				}
				if nu[i].SystemRecord()[7]&0x10 != 0 {
					t.Errorf("unit %d: the assistant flag changed", i)
				}
			}
			if !bytes.Equal(old.Sections[1].Payload, g.Sections[1].Payload) {
				t.Error("BareSettingInfo changed")
			}
		})
	}
}

func TestSetLayoutRefuses(t *testing.T) {
	l1 := system.Cell{Phase: 0}
	for name, mk := range map[string]func(f *rvsc.File) *system.Layout{
		"unplaced unit": func(f *rvsc.File) *system.Layout { return layoutOf(t, f, system.SinglePhase, l1) },
		"other firmware": func(f *rvsc.File) *system.Layout {
			l := layoutOf(t, f, system.SinglePhase, l1, l1)
			for _, u := range l.Units() {
				u.VersionWord = 2699482
			}
			return l
		},
		"unit not in the file": func(f *rvsc.File) *system.Layout {
			l := layoutOf(t, f, system.SinglePhase, l1, l1)
			l.Units()[1].LongID = 0x2000
			return l
		},
		"fewer units than the file": func(f *rvsc.File) *system.Layout {
			l := layoutOf(t, f, system.SinglePhase, l1, l1)
			l.Remove(l.Units()[1])
			return l
		},
	} {
		data := backup(t, 2)
		f, _ := rvsc.Parse(data)
		if err := f.SetLayout(mk(f)); !errors.Is(err, rvsc.ErrLayout) {
			t.Errorf("%s: err %v, want ErrLayout", name, err)
		}
		if out, _ := f.MarshalBinary(); !bytes.Equal(out, data) {
			t.Errorf("%s: the file changed", name)
		}
	}
}

func TestAssistantImage(t *testing.T) {
	data := backup(t, 1)
	f, _ := rvsc.Parse(data)
	units, _ := f.Units()
	u := units[0]
	// The builder's invented area (00 0B) is not in the run form.
	if _, err := u.AssistantImage(); !errors.Is(err, assistant.ErrFormat) {
		t.Fatalf("invented area: %v, want ErrFormat", err)
	}
	img := append([]byte{1, 2, 3, 4, 5, 6}, bytes.Repeat([]byte{0xFF}, 2810)...)
	before, _ := u.Setting(1)
	u.SetAssistantImage(img)
	out, err := f.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	g, err := rvsc.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	gu, _ := g.Units()
	got, err := gu[0].AssistantImage()
	if after, _ := gu[0].Setting(1); err != nil || !bytes.Equal(got, img) || after != before {
		t.Errorf("after SetAssistantImage: %v, %d bytes, setting 1 %d (was %d)", err, len(got), after, before)
	}
}

// TestRealAssistantAreas expands the assistant area of every unit of the
// files in $RVSC_TESTDATA, if set (files are not committed, #2). A
// device-form area must compact back to the same bytes. It logs no
// identifiers.
func TestRealAssistantAreas(t *testing.T) {
	dir := os.Getenv("RVSC_TESTDATA")
	if dir == "" {
		t.Skip("RVSC_TESTDATA not set")
	}
	n := 0
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		ext := strings.ToLower(filepath.Ext(p))
		if err != nil || d.IsDir() || ext != ".rvsc" && ext != ".rvms" {
			return err
		}
		data, _ := os.ReadFile(p)
		f, err := rvsc.Parse(data)
		if err != nil {
			return nil // TestRealFiles reports it
		}
		units, _ := f.Units()
		for i, u := range units {
			n++
			img, err := u.AssistantImage()
			if err != nil {
				t.Errorf("%s unit %d: %v", filepath.Base(p), i, err)
				continue
			}
			if len(img) != assistant.ImageSize {
				t.Logf("%s unit %d: image of %d bytes", filepath.Base(p), i, len(img))
			}
			if !u.UploadForm && !bytes.Equal(assistant.Compact(img), u.AssistantArea()) {
				t.Errorf("%s unit %d: the device-form area does not compact back to its bytes", filepath.Base(p), i)
			}
		}
		return nil
	})
	if n == 0 {
		t.Fatal("no units under RVSC_TESTDATA")
	}
	t.Logf("%d units", n)
}
