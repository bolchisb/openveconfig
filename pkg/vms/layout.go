package vms

import (
	"fmt"
	"slices"

	"github.com/bolchisb/openveconfig/pkg/vebus/system"
)

// Layout returns the system layout of the file: its setup choices and every
// unit in its cell, in the order Cells gives (ours). The header values map
// one to one: the setup type and L2 option are stored as system.Setup and
// system.L2Option store them, ACInputs is read as a count (1-4, still to be
// confirmed on a real file), and bit i of GroupMask switches AC input i as a
// group.
//
// A unit of known with the file unit's long ID is used in its place, so the
// layout holds the identity the unit reported on the bus; it takes the
// file's name when it has none. Other units are made from the file's
// record. The second-input cells of the file are not used: system.Layout
// places second inputs by its own rule.
func (f *File) Layout(known []*system.Unit) (*system.Layout, error) {
	l := system.NewLayout()
	if err := l.Reshape(system.Setup(f.SetupType), int(f.ACInputs)); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	l.L2 = system.L2Option(f.L2Option)
	for i := range l.Grouped {
		l.Grouped[i] = f.GroupMask>>i&1 != 0
	}

	units := map[*Unit]*system.Unit{}
	for _, fu := range f.Units {
		i := slices.IndexFunc(known, func(u *system.Unit) bool { return u.LongID == fu.LongID })
		var u *system.Unit
		if i >= 0 {
			u = known[i]
			if u.Name == "" {
				u.Name = fu.Name
			}
		} else {
			u = &system.Unit{
				LongID: fu.LongID, VersionWord: fu.VersionWord, Letter: fu.Letter,
				ConfigProtocol: fu.ConfigProtocol, ResponseChar: fu.ResponseChar,
				ExternalBF: fu.ExternalBF, Record: system.Record(fu.SystemRecord), Name: fu.Name,
			}
		}
		if err := l.Add(u); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrFormat, err)
		}
		units[fu] = u
	}

	cells := f.Cells()
	codes := make([]uint32, 0, len(cells))
	for code := range cells {
		codes = append(codes, code)
	}
	slices.Sort(codes)
	for _, code := range codes {
		if code > 0xFF {
			return nil, fmt.Errorf("%w: invalid cell code %d", ErrFormat, code)
		}
		c, err := system.CellFromCode(byte(code))
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrFormat, err)
		}
		for _, s := range cells[code] {
			if s.Second {
				continue
			}
			if err := l.Place(units[s.Unit], c); err != nil {
				return nil, fmt.Errorf("%w: unit %08X: %v", ErrFormat, s.Unit.LongID, err)
			}
		}
	}
	return l, nil
}

// FromLayout returns a new file of layout l, the inverse of Layout: every
// unit with its cell, in the layout's order, and the display fields set by
// Arrange. A placed unit stores the record the layout gives it
// (Records(false)), an unplaced one its current record.
//
// A dual-input unit gets a pseudo unit for its second input, with the
// unit's identity and record and no name (ours: what the original stores
// there is not known; Layout does not read it).
func FromLayout(l *system.Layout) *File {
	f := &File{SetupType: byte(l.Setup), L2Option: byte(l.L2), ACInputs: uint32(l.ACInputs), SelectedCell: NoCell}
	for i, g := range l.Grouped {
		if g {
			f.GroupMask |= 1 << i
		}
	}
	recs := map[*system.Unit]system.Record{}
	for _, r := range l.Records(false) {
		recs[r.Unit] = r.Record
	}
	for _, u := range l.Units() {
		rec, ok := recs[u]
		if !ok {
			rec = u.Record
		}
		c, _, _ := l.CellOf(u)
		fu := &Unit{LongID: u.LongID, VersionWord: u.VersionWord, SystemRecord: rec, ResponseChar: u.ResponseChar,
			Letter: u.Letter, ConfigProtocol: u.ConfigProtocol, ExternalBF: u.ExternalBF, Name: u.Name, Cell: uint32(c.Code())}
		if u.DualInput() {
			second := *fu
			second.Name = ""
			fu.Second, fu.SecondCell = &second, uint32(l.SecondCell(u).Code())
		}
		f.Units = append(f.Units, fu)
	}
	f.Arrange()
	return f
}
