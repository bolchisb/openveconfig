package vms

import (
	"errors"
	"testing"

	"github.com/bolchisb/openveconfig/pkg/vebus/system"
)

// parallel2 is a synthetic two-unit parallel system on L1 AC1 with the
// records the layout gives it.
func parallel2() *File {
	unit := func(id uint32, rec [RecordLen]byte, left uint32, name string) *Unit {
		return &Unit{LongID: id, VersionWord: 2699481, SystemRecord: rec, ResponseChar: 0x02, Letter: 1,
			ConfigProtocol: 1, Left: left, Name: name, Cell: CellCode(0, 0)}
	}
	// The second unit is first in the file but second by icon position.
	return &File{SetupType: SetupSingle, L2Option: L2Fixed, ACInputs: 1, GroupMask: 0xF, SelectedCell: NoCell,
		Units: []*Unit{
			unit(0x10000002, [RecordLen]byte{0xFF, 0xFF, 0x09, 0, 0, 0, 0, 0xF0, 0x03}, 100, "B"),
			unit(0x10000001, [RecordLen]byte{0x02, 0x02, 0x09, 0, 0, 0, 0, 0xF0, 0x00}, 0, "A"),
		}}
}

func TestLayout(t *testing.T) {
	f := parallel2()
	l, err := f.Layout(nil)
	if err != nil {
		t.Fatal(err)
	}
	if l.Setup != system.SinglePhase || l.ACInputs != 1 || l.L2 != system.Fixed || l.Grouped != [4]bool{true, true, true, true} {
		t.Errorf("setup %v, %d AC inputs, %v, grouped %v", l.Setup, l.ACInputs, l.L2, l.Grouped)
	}
	es := l.Entries(system.Cell{Phase: 0, AC: 0})
	if len(es) != 2 || es[0].Unit.Name != "A" || es[1].Unit.Name != "B" {
		t.Fatalf("L1 AC1 holds %v", es)
	}
	if is := l.Check(); system.HasErrors(is) {
		t.Errorf("check: %v", is)
	}
	// The layout gives every unit the record the file holds.
	for _, r := range l.Records(false) {
		if r.Record != r.Unit.Record {
			t.Errorf("%s: layout gives %s, file holds %s", r.Unit.Label(), r.Record.Hex(), r.Unit.Record.Hex())
		}
	}
}

func TestLayoutKnownUnits(t *testing.T) {
	bus := &system.Unit{LongID: 0x10000001, VersionWord: 2699482, ResponseChar: 0x02}
	named := &system.Unit{LongID: 0x10000002, Name: "kept"}
	l, err := parallel2().Layout([]*system.Unit{bus, named})
	if err != nil {
		t.Fatal(err)
	}
	us := l.Units()
	if len(us) != 2 || us[0] != bus || us[1] != named {
		t.Fatalf("units %v, want the known ones", us)
	}
	if bus.Name != "A" || bus.Firmware() != 2699482 || named.Name != "kept" {
		t.Errorf("known units: %q firmware %d, %q", bus.Name, bus.Firmware(), named.Name)
	}
}

func TestLayoutUnplacedAndErrors(t *testing.T) {
	f := parallel2()
	f.Units[0].Cell = 0xFF
	l, err := f.Layout(nil)
	if err != nil {
		t.Fatal(err)
	}
	if es := l.Entries(system.Unconfigured); len(es) != 1 || es[0].Unit.Name != "B" {
		t.Errorf("unconfigured: %v", es)
	}

	for name, change := range map[string]func(*File){
		"cell not in setup": func(f *File) { f.Units[0].Cell = CellCode(1, 0) },
		"bad cell code":     func(f *File) { f.Units[0].Cell = 0x1FF },
		"no AC input":       func(f *File) { f.ACInputs = 0 },
		"duplicate unit":    func(f *File) { f.Units[0].LongID = f.Units[1].LongID },
	} {
		f := parallel2()
		change(f)
		known := []*system.Unit{{LongID: 0x10000001}}
		if _, err := f.Layout(known); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err %v, want ErrFormat", name, err)
		}
	}
}
