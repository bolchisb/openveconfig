package rvsc

import (
	"errors"
	"fmt"

	"github.com/bolchisb/openveconfig/pkg/vebus/system"
)

// ErrLayout means a layout cannot be put into a file: the file holds other
// units, or the layout has errors.
var ErrLayout = errors.New("rvsc: the layout does not fit the file")

// SystemUnit returns the unit as a unit of a VE.Bus system: its identity
// and its system record (reverse-engineered, #56).
func (u *Unit) SystemUnit() *system.Unit {
	return &system.Unit{
		LongID: u.LongID, VersionWord: u.FirmwareWord, Letter: u.Letter, ConfigProtocol: u.ConfigProtocol,
		IdentifyFlag: u.IdentifyFlag, ResponseChar: u.ResponseChar, ExternalBF: u.ExternalBF(),
		Record: system.Record(u.SystemRecord()), Name: u.Serial,
	}
}

// SetLayout puts layout l into f, a file downloaded from the same units:
// each unit's system record becomes the record l gives it (Records(false),
// so the assistant-reset flag keeps the file's value and matches the
// assistant area, which is not touched). Units are matched by long ID.
// Only the nine record bytes of each unit change, and the section
// checksums when the file is written.
//
// It refuses (ErrLayout) when the layout's check has errors, or when the
// file's units are not exactly the layout's units with the same firmware
// word. Nothing is changed then.
//
// Settings 76-80 are inferred to mirror the system record (#6); until that
// is checked on hardware (#3) they are left as they are, so front ends
// must say so. Whether a GX applies the record on restore is also a
// hardware check (#3).
func (f *File) SetLayout(l *system.Layout) error {
	if is := l.Check(); system.HasErrors(is) {
		for _, i := range is {
			if i.Severity == system.Error {
				return fmt.Errorf("%w: %s", ErrLayout, i.Message)
			}
		}
	}
	units, err := f.Units()
	if err != nil {
		return err
	}
	recs := l.Records(false)
	if len(recs) != len(units) {
		return fmt.Errorf("%w: the file has %d units, the layout %d", ErrLayout, len(units), len(recs))
	}
	byID := map[uint32]*Unit{}
	for _, u := range units {
		if byID[u.LongID] != nil {
			return fmt.Errorf("%w: unit %08X is twice in the file", ErrLayout, u.LongID)
		}
		byID[u.LongID] = u
	}
	for _, r := range recs {
		u := byID[r.Unit.LongID]
		switch {
		case u == nil:
			return fmt.Errorf("%w: unit %s is not in the file", ErrLayout, r.Unit.Label())
		case u.FirmwareWord != r.Unit.VersionWord:
			return fmt.Errorf("%w: unit %s has firmware word %08X, the file %08X", ErrLayout, r.Unit.Label(), r.Unit.VersionWord, u.FirmwareWord)
		}
	}
	for _, r := range recs {
		byID[r.Unit.LongID].SetSystemRecord(r.Record)
	}
	return nil
}

// SystemUnits returns the units of f as system units, in file order, for
// system.FromRecords.
func (f *File) SystemUnits() ([]*system.Unit, error) {
	units, err := f.Units()
	if err != nil {
		return nil, err
	}
	out := make([]*system.Unit, len(units))
	for i, u := range units {
		out[i] = u.SystemUnit()
	}
	return out, nil
}
