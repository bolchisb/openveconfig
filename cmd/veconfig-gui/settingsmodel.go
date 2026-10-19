package main

import (
	"github.com/bolchisb/victron-veconfigure-ported/pkg/rvsc"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/vebus"
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
	m.dev, m.file = d, file
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
func (sf *settingsFile) save(d *vebus.Device) ([]byte, error) {
	if _, err := vebus.SaveRVSC(d, sf.unit, vebus.SaveOptions{}); err != nil {
		return nil, &uiError{err: err, msg: tr("The settings cannot be saved: {{.Err}}", args{"Err": err})}
	}
	return sf.f.MarshalBinary()
}
