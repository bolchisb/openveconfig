package main

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/serialport"
)

// Preferences that remember the last connection choice.
const (
	prefLastPort = "connection.port" // serial port name
	prefLastDemo = "connection.demo" // true when the demo was chosen
)

// probeTimeout is how long auto-detect waits for the 'V' reply on a port.
// As in openveconfig-cli it is longer than mk2.DefaultTimeout, because an
// RS232 MK2.2 takes its power from DTR, which is only raised on open.
const probeTimeout = time.Second

// portChoice is one entry of the port list.
type portChoice struct {
	label string
	port  string // serial port name; empty for the demo
	demo  bool
}

// connBar is the content of the connection dialog: the port list, the
// connect, auto-detect and refresh actions, and the connection status.
type connBar struct {
	u       *ui
	choices []portChoice // parallel to ports.Options
	ports   *widget.Select
	refresh *widget.Button
	detect  *widget.Button
	connect *widget.Button
	status  *widget.Label
	dlg     *dialog.CustomDialog // nil until first shown
}

// newConnBar returns the connection dialog of u, not yet shown.
func newConnBar(u *ui) *connBar {
	b := &connBar{u: u}
	b.ports = widget.NewSelect(nil, nil)
	b.ports.PlaceHolder = tr("Choose a port")
	b.refresh = widget.NewButtonWithIcon(tr("Refresh"), theme.ViewRefreshIcon(), b.refreshPorts)
	b.detect = widget.NewButtonWithIcon(tr("Auto-detect"), theme.SearchIcon(), b.autoDetect)
	b.connect = widget.NewButton(tr("Connect"), b.toggle)
	b.status = widget.NewLabel("")
	b.status.Wrapping = fyne.TextWrapWord
	u.onSession(b.update)
	b.update(nil)
	b.refreshPorts()
	return b
}

// show opens the connection dialog.
func (b *connBar) show() {
	if b.dlg == nil {
		hint := widget.NewLabel(tr("Choose the serial port of the MK2 or MK3 interface, let the program look for it, " +
			"or choose the demo, which simulates two devices."))
		hint.Wrapping = fyne.TextWrapWord
		row := container.NewBorder(nil, nil, widget.NewLabel(tr("Port:")), nil, b.ports)
		content := container.NewVBox(hint, row, container.NewHBox(b.refresh, b.detect), b.status)
		b.dlg = dialog.NewCustomWithoutButtons(tr("Connect to a device"), content, b.u.win)
		b.dlg.SetButtons([]fyne.CanvasObject{widget.NewButton(tr("Close"), b.dlg.Hide), b.connect})
		b.dlg.Resize(fyne.NewSize(560, b.dlg.MinSize().Height))
	}
	b.dlg.Show()
}

// refreshPorts rebuilds the port list and selects the last choice, or else
// the first likely interface.
func (b *connBar) refreshPorts() {
	b.choices = []portChoice{{label: tr("Demo (no hardware)"), demo: true}}
	ports, err := serialport.List()
	if err != nil {
		showError(b.u, &uiError{err: err, msg: tr("Cannot list the serial ports: {{.Err}}", args{"Err": err})})
	}
	for _, p := range ports {
		b.choices = append(b.choices, portChoice{label: portLabel(p), port: p.Name})
	}
	labels := make([]string, len(b.choices))
	for i, c := range b.choices {
		labels[i] = c.label
	}
	b.ports.ClearSelected()
	b.ports.SetOptions(labels)
	b.ports.SetSelectedIndex(b.preferred(ports))
}

// preferred returns the index of the choice to select after a refresh, or
// -1 for none.
func (b *connBar) preferred(ports []serialport.PortInfo) int {
	prefs := b.u.app.Preferences()
	if prefs.Bool(prefLastDemo) {
		return 0
	}
	if last := prefs.String(prefLastPort); last != "" {
		if i := b.indexOf(last); i >= 0 {
			return i
		}
	}
	for _, p := range ports {
		if p.Kind != serialport.Unknown {
			return b.indexOf(p.Name)
		}
	}
	return -1
}

// indexOf returns the index of the choice for serial port name, or -1.
func (b *connBar) indexOf(name string) int {
	return slices.IndexFunc(b.choices, func(c portChoice) bool { return !c.demo && c.port == name })
}

// portLabel names a port in the list, with a hint when its USB identity
// matches a known interface. The hint is only a guess (see serialport).
func portLabel(p serialport.PortInfo) string {
	a := args{"Port": p.Name}
	switch p.Kind {
	case serialport.MK3USB:
		return tr("{{.Port}} (likely an MK3-USB)", a)
	case serialport.PL2303:
		return tr("{{.Port}} (likely a USB-RS232 adapter for an MK2)", a)
	default:
		return p.Name
	}
}

// toggle connects to the selected port, or disconnects.
func (b *connBar) toggle() {
	if b.u.sess != nil {
		b.u.setSession(nil)
		return
	}
	i := b.ports.SelectedIndex()
	if i < 0 {
		showMessage(b.u, tr("Connect"), tr("Choose a port or the demo first, or use auto-detect."))
		return
	}
	b.open(b.choices[i])
}

// open remembers c and connects to it in the background.
func (b *connBar) open(c portChoice) {
	u := b.u
	prefs := u.app.Preferences()
	prefs.SetBool(prefLastDemo, c.demo)
	if !c.demo {
		prefs.SetString(prefLastPort, c.port)
	}
	runWithProgress(u, tr("Connecting"), func(ctx context.Context, progress func(done, total int)) error {
		s, err := connect(ctx, c.port, c.demo, progress)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			s.close()
			return err
		}
		fyne.Do(func() { u.setSession(s) })
		return nil
	})
}

// autoDetect probes every port, likely interfaces first, and connects to
// the first one where an interface answers.
func (b *connBar) autoDetect() {
	u := b.u
	runWithProgress(u, tr("Looking for an interface"), func(ctx context.Context, progress func(done, total int)) error {
		ports, err := serialport.List()
		if err != nil {
			return &uiError{err: err, msg: tr("Cannot list the serial ports: {{.Err}}", args{"Err": err})}
		}
		var likely, others []serialport.PortInfo
		for _, p := range ports {
			if p.Kind != serialport.Unknown {
				likely = append(likely, p)
			} else {
				others = append(others, p)
			}
		}
		ports = slices.Concat(likely, others)
		var denied error // the first permission error, which explains a miss
		for i, p := range ports {
			progress(i, len(ports))
			err := probe(ctx, p.Name)
			if err == nil {
				fyne.Do(func() {
					b.refreshPorts()
					if j := b.indexOf(p.Name); j >= 0 {
						b.ports.SetSelectedIndex(j)
						b.open(b.choices[j])
					}
				})
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if denied == nil && isPermission(err) {
				denied = openFailure(p.Name, err)
			}
		}
		if denied != nil {
			return denied
		}
		return &uiError{msg: tr("No interface found. Check that the interface is plugged in and its driver is installed, " +
			"then try again. You can also choose the port yourself.")}
	})
}

// probe sends one 'V' request at 2400 baud on port name and returns nil if
// an interface answers. Like openveconfig-cli ports --probe, it does not use
// NegotiateBaud, whose recovery sends 'R' at 115200 and must not reach
// unknown devices.
func probe(ctx context.Context, name string) error {
	p, err := serialport.Open(name)
	if err != nil {
		return err
	}
	c := mk2.NewClient(p, &mk2.Options{Timeout: probeTimeout})
	defer c.Close()
	_, err = c.Version(ctx)
	return err
}

// update shows the state of session s, or nil when disconnected. The
// dialog closes when a session opens: the Overview page shows it.
func (b *connBar) update(s *session) {
	controls := []fyne.Disableable{b.ports, b.refresh, b.detect}
	if s == nil {
		b.status.SetText(tr("Not connected."))
		b.connect.SetText(tr("Connect"))
		b.connect.Importance = widget.HighImportance
		b.connect.Refresh()
		for _, w := range controls {
			w.Enable()
		}
		return
	}
	b.status.SetText(describe(s))
	b.connect.SetText(tr("Disconnect"))
	b.connect.Importance = widget.MediumImportance
	b.connect.Refresh()
	for _, w := range controls {
		w.Disable()
	}
	if b.dlg != nil {
		b.dlg.Hide()
	}
}

// describe returns the connection status text of s.
func describe(s *session) string {
	where := s.portName
	if s.demo {
		where = tr("the demo simulator")
	}
	text := tr("Connected to {{.Port}}: {{.Type}} interface, version {{.Version}}, {{.Baud}} baud.", args{
		"Port":    where,
		"Type":    s.interfaceType(),
		"Version": strconv.FormatUint(uint64(s.iface.Version), 10),
		"Baud":    strconv.Itoa(s.baud),
	})
	devices := make([]string, len(s.devices))
	for i, a := range s.devices {
		devices[i] = tr("address {{.Address}}, firmware {{.Firmware}}", args{"Address": a, "Firmware": s.firmwareText(a)})
	}
	return text + "\n" + trn("{{.Count}} device found:", "{{.Count}} devices found:", len(s.devices)) +
		" " + strings.Join(devices, "; ")
}
