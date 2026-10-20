package main

import (
	"errors"
	"fmt"

	"github.com/bolchisb/openveconfig/pkg/gridcode"
	"github.com/bolchisb/openveconfig/pkg/rvsc"
	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// settingsModel is the settings model that the settings tabs edit. There is
// one per window (ui.settings); every tab that shows settings, the grid tab
// included, works on it. It is used on the UI goroutine only.
type settingsModel struct {
	// dev is the model being edited: read from a device (file == nil) or
	// loaded from a settings file. nil until one is loaded. A Device is not
	// safe for concurrent use: code on other goroutines must not touch it,
	// except an op started on the UI goroutine that owns it until it hands
	// back through fyne.Do (see sendSettings).
	dev *vebus.Device
	// file is the settings file dev was loaded from, or nil.
	file *settingsFile
	// grid is the grid-info table of file or of the device dev was read
	// from (not known when it was not read).
	grid gridcode.Model

	listeners []func(loaded bool)
	// navigators show the control of an entry; see onFind.
	navigators []func(e vebus.Entry) bool
}

// onChange registers f to be called on the UI goroutine after the model
// changes: with loaded set when a new model replaces dev (rebuild what
// shows it), and unset after a value of dev was edited (show the new
// values).
func (m *settingsModel) onChange(f func(loaded bool)) { m.listeners = append(m.listeners, f) }

// load makes d, loaded from file (nil for a device), the model and tells
// the listeners.
func (m *settingsModel) load(d *vebus.Device, file *settingsFile) {
	var grid gridcode.Model
	if file != nil {
		if table, err := file.f.GridInfo(); err == nil {
			grid = gridcode.TableModel(table)
		}
	}
	m.loadGrid(d, file, grid)
}

// loadFromDevice makes d, read from a device whose grid-info table is
// grid, the model and tells the listeners.
func (m *settingsModel) loadFromDevice(d *vebus.Device, grid gridcode.Model) {
	m.loadGrid(d, nil, grid)
}

func (m *settingsModel) loadGrid(d *vebus.Device, file *settingsFile, grid gridcode.Model) {
	m.dev, m.file, m.grid = d, file, grid
	m.notify(true)
}

// changed tells the listeners that values of dev were edited. Call it after
// every change to dev.
func (m *settingsModel) changed() { m.notify(false) }

func (m *settingsModel) notify(loaded bool) {
	for _, f := range m.listeners {
		f(loaded)
	}
}

// onFind registers f, which shows the control of entry e and reports
// whether it could; Find uses the first that can.
func (m *settingsModel) onFind(f func(e vebus.Entry) bool) { m.navigators = append(m.navigators, f) }

// settingsFile is an open .rvsc/.rvms file and the unit of it being edited.
type settingsFile struct {
	name  string // base name, for display and as the default save name
	f     *rvsc.File
	info  *rvsc.SettingInfoTable
	units []*rvsc.Unit
	unit  *rvsc.Unit
}

// parseSettingsFile parses a .rvsc/.rvms file. Its first unit is selected.
func parseSettingsFile(name string, data []byte) (*settingsFile, error) {
	f, err := rvsc.Parse(data)
	if err != nil {
		return nil, &uiError{err: err, msg: tr("{{.File}} is not a valid settings file: {{.Err}}", args{"File": name, "Err": err})}
	}
	info, err := f.SettingInfo()
	if err == nil {
		var units []*rvsc.Unit
		if units, err = f.Units(); err == nil && len(units) > 0 {
			return &settingsFile{name: name, f: f, info: info, units: units, unit: units[0]}, nil
		}
	}
	if err == nil {
		return nil, &uiError{msg: tr("{{.File}} holds no unit settings.", args{"File": name})}
	}
	return nil, &uiError{err: err, msg: tr("{{.File}} is not a valid settings file: {{.Err}}", args{"File": name, "Err": err})}
}

// device builds the model of the selected unit.
func (sf *settingsFile) device() (*vebus.Device, error) {
	d, err := vebus.FromRVSC(sf.info, sf.unit, nil)
	if err != nil {
		return nil, &uiError{err: err, msg: tr("Cannot read the settings of {{.File}}: {{.Err}}", args{"File": sf.name, "Err": err})}
	}
	return d, nil
}

// save stores the values of d, the model of the selected unit, in the file
// and returns the file's bytes. The caller loads a fresh model afterwards,
// so the saved values count as unchanged.
//
// A changed grid code and changed grid parameters are saved with newly
// stamped checker words (checkerWords); without grid changes the checker
// words stay as they are.
func (sf *settingsFile) save(d *vebus.Device) ([]byte, error) {
	pair, stamp, err := sf.checkerWords(d)
	if err == nil {
		_, err = vebus.SaveRVSC(d, sf.unit, vebus.SaveOptions{AllowGridCode: true})
	}
	if err == nil && stamp {
		err = errors.Join(sf.unit.SetSetting(int(firstGridWord), pair.A), sf.unit.SetSetting(int(lastGridWord), pair.B))
	}
	if err != nil {
		return nil, &uiError{err: err, msg: tr("The settings cannot be saved: {{.Err}}", args{"Err": err})}
	}
	return sf.f.MarshalBinary()
}

// checkerWords returns the checker words to save with d, the model of the
// selected unit, and whether to save them: when d's grid code or grid
// words differ from the file and d has the checker words. They are
// stamped (gridcode.Stamp) from the file's pair, d's grid code and that
// code's parameter-set version in the file's grid-info table, and must
// pass both consistency checks.
func (sf *settingsFile) checkerWords(d *vebus.Device) (gridcode.Pair, bool, error) {
	if len(changedGridSettings(d)) == 0 || !d.Supported(vebus.SettingID(firstGridWord)) {
		return gridcode.Pair{}, false, nil
	}
	var old gridcode.Pair
	a, errA := sf.unit.Setting(int(firstGridWord))
	b, errB := sf.unit.Setting(int(lastGridWord))
	code, errC := d.Raw(vebus.SettingID(gridCodeSetting))
	table, errT := sf.f.GridInfo()
	if err := errors.Join(errA, errB, errC, errT); err != nil {
		return old, false, err
	}
	old = gridcode.Pair{A: a, B: b}
	g := gridcode.Code(code)
	v, err := gridcode.ParamSetVersion(table, g)
	if err != nil {
		return old, false, err
	}
	p, err := gridcode.Stamp(old, g, v)
	if err != nil {
		return old, false, err
	}
	if header, version := p.CheckShown(g, v); !p.Consistent(g, v) || !header || !version {
		return old, false, fmt.Errorf("the new check words %#04x/%#04x do not fit grid code %d", p.A, p.B, g)
	}
	return p, true, nil
}
