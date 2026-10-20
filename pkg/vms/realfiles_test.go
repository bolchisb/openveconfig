package vms

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRealFileFindings reads the .vms files of $VMS_TESTDATA, as
// TestRealFiles does (#72). Every file must give a layout. It logs, without
// identifiers, what #72 needs to settle:
//   - the header's AC-inputs value against the highest AC input any unit
//     uses (equal to it + 1 for a count, to it for an index);
//   - the fields in which each second-input pseudo unit differs from its
//     unit, and whether FromLayout writes the same pseudo unit.
func TestRealFileFindings(t *testing.T) {
	dir := os.Getenv("VMS_TESTDATA")
	if dir == "" {
		t.Skip("VMS_TESTDATA not set")
	}
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".vms") {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no .vms files under VMS_TESTDATA")
	}
	for _, p := range paths {
		t.Run(filepath.Base(p), func(t *testing.T) {
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			f, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("version %d, setup type %d, L2 option %d, AC inputs field %d, highest AC input used %s, group mask %04b",
				f.Version, f.SetupType, f.L2Option, f.ACInputs, highestInput(f), f.GroupMask)
			for i, u := range f.Units {
				if u.Second != nil {
					t.Logf("unit %d (response 0x%02X, cell %d): pseudo unit in cell %d differs in: %s",
						i, u.ResponseChar, u.Cell, u.SecondCell, pseudoDiff(u, u.Second))
				}
			}
			l, err := f.Layout(nil)
			if err != nil {
				t.Fatalf("Layout: %v", err)
			}
			g := FromLayout(l)
			for i, u := range f.Units {
				if u.Second == nil {
					continue
				}
				if i >= len(g.Units) || g.Units[i].Second == nil {
					t.Logf("unit %d: FromLayout writes no pseudo unit", i)
					continue
				}
				if d := pseudoDiff(u.Second, g.Units[i].Second); d != "nothing" {
					t.Logf("unit %d: FromLayout's pseudo unit differs from the file's in: %s", i, d)
				}
				if g.Units[i].SecondCell != u.SecondCell {
					t.Logf("unit %d: FromLayout puts the second input in cell %d, the file in %d", i, g.Units[i].SecondCell, u.SecondCell)
				}
			}
		})
	}
}

// highestInput returns the highest AC input index (0-3) of any unit's or
// second input's cell, or "none".
func highestInput(f *File) string {
	hi := -1
	for _, u := range f.Units {
		cells := []uint32{u.Cell}
		if u.Second != nil {
			cells = append(cells, u.SecondCell)
		}
		for _, c := range cells {
			if c != NoCell {
				hi = max(hi, int(c%10))
			}
		}
	}
	if hi < 0 {
		return "none"
	}
	return fmt.Sprint(hi)
}

// pseudoDiff names the fields in which b differs from a, without values;
// display fields are left out.
func pseudoDiff(a, b *Unit) string {
	var d []string
	add := func(name string, differs bool) {
		if differs {
			d = append(d, name)
		}
	}
	add("long ID", a.LongID != b.LongID)
	add("version word", a.VersionWord != b.VersionWord)
	for i := range a.SystemRecord {
		add(fmt.Sprintf("record byte %d", i), a.SystemRecord[i] != b.SystemRecord[i])
	}
	add("response char", a.ResponseChar != b.ResponseChar)
	add("letter", a.Letter != b.Letter)
	add("config protocol", a.ConfigProtocol != b.ConfigProtocol)
	add("external BF", a.ExternalBF != b.ExternalBF)
	add("name", a.Name != b.Name)
	if len(d) == 0 {
		return "nothing"
	}
	return strings.Join(d, ", ")
}
