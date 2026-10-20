package main

// The System Configurator window: find the units of a VE.Bus system,
// arrange them on a grid of phases × AC inputs, check the layout, send it
// to the units, and load or save it as a .vms file. The rules are in
// pkg/vebus/system, the bus operations in pkg/vebus/system/bus and the file
// format in pkg/vms.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/vebus/system"
	"github.com/bolchisb/openveconfig/pkg/vebus/system/bus"
	"github.com/bolchisb/openveconfig/pkg/vms"
)

// systemConfigurators holds the open System Configurator of each ui. Used
// on the UI goroutine only.
var systemConfigurators = map[*ui]*sysConfig{}

// systemOptions are the options of the system sessions. Tests shorten the
// waits.
var systemOptions bus.Options

var vmsFilter = storage.NewExtensionFileFilter([]string{".vms"})

// unitNamePref returns the preference that holds the PC-side name of the
// unit longID. Units do not store names.
func unitNamePref(longID uint32) string { return fmt.Sprintf("system.unit.%08X.name", longID) }

// showSystemConfigurator opens the System Configurator for the open
// session, or brings the open one to the front. Without a session it can
// still open, arrange and save files. It closes when the session changes,
// and gets the configuration of the units when it opens on a session.
func showSystemConfigurator(u *ui) {
	if c := systemConfigurators[u]; c != nil {
		c.win.RequestFocus()
		return
	}
	c := newSysConfig(u, u.sess)
	systemConfigurators[u] = c
	c.win.SetOnClosed(func() {
		c.close()
		delete(systemConfigurators, u)
	})
	u.onSession(func(*session) {
		if systemConfigurators[u] == c {
			c.win.Close()
		}
	})
	c.win.Show()
	if c.sess != nil {
		c.getConfiguration()
	}
}

// sysConfig is the System Configurator window. Its fields are used on the
// UI goroutine only, except bs.
type sysConfig struct {
	u      *ui
	sess   *session // nil when opened without a connection
	win    fyne.Window
	ctx    context.Context // ends when the window closes
	cancel context.CancelFunc
	// bs is the system session on sess's client, opened by the first
	// operation. It is used inside sess.Do only.
	bs *bus.Session

	layout       *system.Layout
	busUnits     []*system.Unit  // the units of the last scan; nil before one
	active       map[uint32]byte // short address each unit answered with
	inconsistent bool            // the last scan's records did not agree
	file         string          // the name of the file the layout comes from
	selected     *system.Unit
	flashing     map[uint32]bool

	status *widget.Label
	grid   *fyne.Container
	cells  []*cellView
	issues *fyne.Container
	reason *widget.RichText // why sending is not possible now
	get    *widget.Button
	send   *widget.Button
	panel  *unitPanel

	drag *cellView // the cell under a unit being dragged
	// ops counts the operations of run that have not finished, done
	// included.
	ops sync.WaitGroup
}

func newSysConfig(u *ui, s *session) *sysConfig {
	c := &sysConfig{u: u, sess: s, win: u.app.NewWindow(tr("System Configurator")), layout: system.NewLayout(),
		active: map[uint32]byte{}, flashing: map[uint32]bool{}}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.status = widget.NewLabel("")
	c.status.Wrapping = fyne.TextWrapWord
	c.get = widget.NewButtonWithIcon(tr("Get configuration"), theme.DownloadIcon(), c.getConfiguration)
	setup := widget.NewButtonWithIcon(tr("Setup…"), theme.SettingsIcon(), c.showSetup)
	open := widget.NewButtonWithIcon(tr("Open file…"), theme.FolderOpenIcon(), c.openFile)
	save := widget.NewButtonWithIcon(tr("Save file…"), theme.DocumentSaveIcon(), c.saveFile)
	c.send = widget.NewButtonWithIcon(tr("Send configuration"), theme.UploadIcon(), c.sendConfiguration)
	c.send.Importance = widget.HighImportance
	c.reason = mutedText("")
	bar := container.NewBorder(nil, nil, container.NewHBox(c.get, setup, open, save), container.NewHBox(c.reason, c.send))

	c.grid = container.NewVBox()
	c.issues = container.NewVBox()
	c.panel = newUnitPanel(c)
	issuesHead := widget.NewLabelWithStyle(tr("System check"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	left := container.NewVScroll(container.NewVBox(c.status, c.grid, issuesHead, c.issues))
	split := container.NewHSplit(left, container.NewVScroll(c.panel.root))
	split.Offset = 0.7
	c.win.SetContent(container.NewStack(newColourRect(colorNamePage, fyne.Size{}, ""),
		container.New(layout.NewCustomPaddedLayout(8, 8, 12, 12), container.NewBorder(bar, nil, nil, nil, split))))
	c.win.Resize(fyne.NewSize(1100, 700))
	c.refresh()
	return c
}

// close stops what the window started: the operation in progress, the
// keep-alive and the flashing LEDs.
func (c *sysConfig) close() {
	c.cancel()
	if c.sess == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c.sess.Do(ctx, func(context.Context, *mk2.Client) error {
			if c.bs != nil {
				c.bs.Close()
			}
			return nil
		})
	}()
}

// openBus opens c.bs on cl if it is not open. Call it inside sess.Do.
func (c *sysConfig) openBus(ctx context.Context, cl *mk2.Client) error {
	if c.bs != nil {
		return nil
	}
	bs, err := bus.Open(ctx, cl, systemOptions)
	if err != nil {
		return err
	}
	c.bs = bs
	return nil
}

// activeAddresses returns the short address of each unit of the last scan
// of bs. Call it inside sess.Do.
func activeAddresses(bs *bus.Session) map[uint32]byte {
	m := map[uint32]byte{}
	for _, u := range bs.Units() {
		if a, ok := bs.ActiveAddress(u.LongID); ok {
			m[u.LongID] = a
		}
	}
	return m
}

// run runs op on the system session in the background, with a dialog that
// shows its steps and can cancel it, and then calls done on the UI
// goroutine with op's error. Cancelling a write leads to its rollback.
func (c *sysConfig) run(title string, op func(ctx context.Context, bs *bus.Session, p bus.Progress) error, done func(err error)) {
	ctx, cancel := context.WithCancel(c.ctx)
	step := widget.NewLabel(tr("Checking the interface…"))
	bar := widget.NewProgressBarInfinite()
	d := dialog.NewCustomWithoutButtons(title, container.NewVBox(step, bar), c.win)
	var stop *widget.Button
	stop = widget.NewButton(tr("Cancel"), func() {
		stop.Disable()
		cancel()
	})
	d.SetButtons([]fyne.CanvasObject{stop})
	d.Resize(fyne.NewSize(480, d.MinSize().Height))
	d.Show()
	progress := func(e bus.Event) {
		text := stepText(e)
		fyne.Do(func() { step.SetText(text) })
	}
	c.ops.Add(1)
	go func() {
		err := c.sess.Do(ctx, func(ctx context.Context, cl *mk2.Client) error {
			if err := c.openBus(ctx, cl); err != nil {
				return err
			}
			return op(ctx, c.bs, progress)
		})
		cancel()
		fyne.Do(func() {
			defer c.ops.Done()
			bar.Stop()
			d.Hide()
			done(err)
		})
	}()
}

// showFailure shows the error of a system operation, unless the user
// cancelled it before anything was written.
func (c *sysConfig) showFailure(title string, err error, details string) {
	var be *bus.Error
	if !errors.As(err, &be) && errors.Is(err, context.Canceled) {
		return
	}
	msg := failureText(err)
	if details != "" {
		msg += "\n\n" + details
	}
	text := widget.NewLabel(msg)
	text.Wrapping = fyne.TextWrapWord
	d := dialog.NewCustom(title, tr("OK"), container.NewVScroll(text), c.win)
	d.Resize(fyne.NewSize(560, 360))
	d.Show()
}

// getConfiguration finds the units and shows the layout their records
// give.
func (c *sysConfig) getConfiguration() {
	if c.sess == nil {
		return
	}
	var rep *bus.ScanReport
	var active map[uint32]byte
	c.run(tr("Get configuration"), func(ctx context.Context, bs *bus.Session, p bus.Progress) error {
		var err error
		rep, err = bs.Scan(ctx, p)
		active = activeAddresses(bs)
		return err
	}, func(err error) {
		if rep != nil {
			c.applyScan(rep, active)
		}
		if err != nil {
			c.showFailure(tr("Get configuration"), err, "")
		}
	})
}

// applyScan shows the result of a scan.
func (c *sysConfig) applyScan(rep *bus.ScanReport, active map[uint32]byte) {
	prefs := c.u.app.Preferences()
	for _, un := range rep.Units {
		if un.Name == "" {
			un.Name = prefs.String(unitNamePref(un.LongID))
		}
	}
	c.busUnits, c.active, c.layout, c.file = rep.Units, active, rep.Layout, ""
	c.inconsistent = rep.Inconsistent != nil
	status := trn("{{.Count}} unit found.", "{{.Count}} units found.", len(rep.Units))
	if len(rep.Lost) > 0 {
		status += " " + tr("These units were found but did not answer when their configuration was read: {{.Units}}.",
			args{"Units": unitLabels(rep.Lost)})
	}
	c.status.SetText(status)
	c.refresh()
}

// issueList returns the findings of the system check and, after a scan,
// of the bus check.
func (c *sysConfig) issueList() []system.Issue {
	var out []system.Issue
	if c.inconsistent {
		out = append(out, system.Issue{Rule: system.RuleInconsistent, Severity: system.Warning})
	}
	out = append(out, c.layout.Check()...)
	if c.busUnits != nil {
		ids := make([]uint32, len(c.busUnits))
		for i, un := range c.busUnits {
			ids[i] = un.LongID
		}
		if is := c.layout.CheckBus(ids); is != nil {
			out = append(out, *is)
		}
	}
	return out
}

// sendReason says why the layout cannot be sent now, or is "".
func (c *sysConfig) sendReason(issues []system.Issue) string {
	switch {
	case c.sess == nil:
		return tr("Connect to the system to send the configuration.")
	case c.busUnits == nil:
		return tr("Get the configuration first.")
	case system.HasErrors(issues):
		return tr("Correct the errors of the system check to send.")
	}
	return ""
}

// refresh shows the layout, the check and the selected unit.
func (c *sysConfig) refresh() {
	if c.selected != nil && !slices.Contains(c.layout.Units(), c.selected) {
		c.selected = nil
	}
	c.buildGrid()
	issues := c.issueList()
	c.issues.Objects = nil
	for _, is := range issues {
		icon, colour := theme.WarningIcon(), theme.ColorNameWarning
		if is.Severity == system.Error {
			icon, colour = theme.ErrorIcon(), theme.ColorNameError
		}
		text := widget.NewLabel(issueText(is))
		text.Wrapping = fyne.TextWrapWord
		c.issues.Add(container.NewBorder(nil, nil, widget.NewIcon(theme.NewColoredResource(icon, colour)), nil, text))
	}
	if len(issues) == 0 {
		c.issues.Add(widget.NewLabel(tr("No errors or warnings.")))
	}
	c.issues.Refresh()
	reason := c.sendReason(issues)
	setMutedText(c.reason, reason)
	if reason == "" {
		c.send.Enable()
	} else {
		c.send.Disable()
	}
	if c.sess == nil {
		c.get.Disable()
		if c.status.Text == "" {
			c.status.SetText(tr("Not connected. You can open, arrange and save a system file."))
		}
	}
	c.panel.update()
}

// buildGrid rebuilds the grid of the layout and the unconfigured area.
func (c *sysConfig) buildGrid() {
	l := c.layout
	phaseColumn := func(o fyne.CanvasObject) fyne.CanvasObject {
		return container.NewGridWrap(fyne.NewSize(48, o.MinSize().Height), o)
	}
	var heads []fyne.CanvasObject
	for a := range l.ACInputs {
		heads = append(heads, columnHead(tr("AC input {{.Input}}", args{"Input": strconv.Itoa(a + 1)})))
	}
	rows := []fyne.CanvasObject{container.NewBorder(nil, nil, phaseColumn(widget.NewLabel("")), nil,
		container.NewGridWithColumns(l.ACInputs, heads...))}
	c.cells = nil
	for p := range l.Setup.Phases() {
		var cells []fyne.CanvasObject
		for a := range l.ACInputs {
			cv := c.newCellView(system.Cell{Phase: p, AC: a})
			cells = append(cells, cv.root)
		}
		rows = append(rows, container.NewBorder(nil, nil, phaseColumn(columnHead(phaseName(p))), nil,
			container.NewGridWithColumns(l.ACInputs, cells...)))
	}
	unconf := c.newCellView(system.Unconfigured)
	rows = append(rows, columnHead(tr("Unconfigured")), unconf.root)
	c.grid.Objects = []fyne.CanvasObject{container.NewVBox(rows...)}
	c.grid.Refresh()
}

// cellView shows the units of one cell, or of the unconfigured area. Units
// are dropped on it.
type cellView struct {
	cell system.Cell
	root fyne.CanvasObject
	bg   *colourRect
}

func (c *sysConfig) newCellView(cell system.Cell) *cellView {
	cv := &cellView{cell: cell, bg: newColourRect(colorNameSurface, fyne.NewSize(0, 64), theme.SizeNameInputRadius)}
	box := container.NewVBox()
	for _, e := range c.layout.Entries(cell) {
		box.Add(newUnitTile(c, e))
	}
	if len(box.Objects) == 0 {
		hint := tr("Drop units here.")
		if !cell.Configured() {
			hint = tr("Every unit is placed.")
		}
		box.Add(mutedText(hint))
	}
	cv.root = container.NewStack(cv.bg, container.NewPadded(box))
	c.cells = append(c.cells, cv)
	return cv
}

func (cv *cellView) highlight(on bool) {
	cv.bg.colour = colorNameSurface
	if on {
		cv.bg.colour = theme.ColorNameFocus
	}
	cv.bg.Refresh()
}

// cellAt returns the cell view under the absolute position pos, or nil.
func (c *sysConfig) cellAt(pos fyne.Position) *cellView {
	d := fyne.CurrentApp().Driver()
	for _, cv := range c.cells {
		p, s := d.AbsolutePositionForObject(cv.root), cv.root.Size()
		if pos.X >= p.X && pos.Y >= p.Y && pos.X < p.X+s.Width && pos.Y < p.Y+s.Height {
			return cv
		}
	}
	return nil
}

// place moves un to cell and shows the result.
func (c *sysConfig) place(un *system.Unit, cell system.Cell) {
	if err := c.layout.Place(un, cell); err != nil {
		return // a cell the setup does not have; the grid does not show one
	}
	c.selected = un
	c.refresh()
}

func (c *sysConfig) selectUnit(un *system.Unit) {
	c.selected = un
	c.refresh()
}

// unitTile is a unit on the grid, or the second AC input of a dual-input
// unit. Tap it to select the unit; drag a unit to another cell.
type unitTile struct {
	widget.BaseWidget
	c       *sysConfig
	entry   system.Entry
	content fyne.CanvasObject
	bg      *colourRect
}

func newUnitTile(c *sysConfig, e system.Entry) *unitTile {
	t := &unitTile{c: c, entry: e}
	un := e.Unit
	t.bg = newColourRect(colorNamePage, fyne.Size{}, theme.SizeNameInputRadius)
	if c.selected == un {
		t.bg.colour = theme.ColorNameSelection
	}
	var lines []fyne.CanvasObject
	if e.Second {
		lines = append(lines, mutedText(tr("AC input 2 of {{.Unit}}", args{"Unit": un.Label()})))
	} else {
		name := widget.NewLabelWithStyle(un.Label(), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		name.Truncation = fyne.TextTruncateEllipsis
		info := tr("{{.Kind}} · firmware {{.Firmware}}", args{"Kind": unitKindText(un),
			"Firmware": strconv.FormatUint(uint64(un.Firmware()), 10)})
		if a, ok := c.active[un.LongID]; ok {
			info = tr("{{.Info}} · address {{.Address}}", args{"Info": info, "Address": strconv.Itoa(int(a))})
		}
		lines = append(lines, name, mutedText(info))
		if c.flashing[un.LongID] {
			lines = append(lines, newPill(tr("Identifying"), theme.ColorNameWarning, true))
		}
	}
	t.content = container.NewStack(t.bg, container.New(layout.NewCustomPaddedLayout(2, 2, 6, 6), container.NewVBox(lines...)))
	t.ExtendBaseWidget(t)
	return t
}

func (t *unitTile) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(t.content) }

func (t *unitTile) Tapped(*fyne.PointEvent) { t.c.selectUnit(t.entry.Unit) }

// Dragged highlights the cell under the pointer. A second input moves with
// its unit, so it is not dragged.
func (t *unitTile) Dragged(ev *fyne.DragEvent) {
	if t.entry.Second {
		return
	}
	cv := t.c.cellAt(ev.AbsolutePosition)
	if cv != t.c.drag {
		if t.c.drag != nil {
			t.c.drag.highlight(false)
		}
		if cv != nil {
			cv.highlight(true)
		}
		t.c.drag = cv
	}
}

// DragEnd places the unit in the cell under the pointer.
func (t *unitTile) DragEnd() {
	cv := t.c.drag
	t.c.drag = nil
	if cv == nil {
		return
	}
	cv.highlight(false)
	t.c.place(t.entry.Unit, cv.cell)
}

// unitPanel shows the selected unit: its identity and record, and the
// actions on it. Its unit and cell choosers are the keyboard alternative
// to tapping and dragging.
type unitPanel struct {
	c        *sysConfig
	root     *fyne.Container
	units    *widget.Select
	shown    []*system.Unit // parallel to units.Options
	info     *widget.Label
	cell     *widget.Select
	cellOpts []system.Cell // parallel to cell.Options
	name     *widget.Entry
	rename   *widget.Button
	identify *widget.Button
	settings *widget.Button
	updating bool // update is setting the choosers
}

func newUnitPanel(c *sysConfig) *unitPanel {
	p := &unitPanel{c: c, info: widget.NewLabel(""), name: widget.NewEntry()}
	p.info.Wrapping = fyne.TextWrapWord
	p.units = widget.NewSelect(nil, func(string) {
		if i := p.units.SelectedIndex(); !p.updating && i >= 0 {
			c.selectUnit(p.shown[i])
		}
	})
	p.units.PlaceHolder = tr("Choose a unit")
	p.cell = widget.NewSelect(nil, func(string) {
		if i := p.cell.SelectedIndex(); !p.updating && i >= 0 && c.selected != nil {
			c.place(c.selected, p.cellOpts[i])
		}
	})
	p.name.SetPlaceHolder(tr("Name on this computer"))
	p.rename = widget.NewButton(tr("Rename"), func() { c.rename(c.selected, p.name.Text) })
	p.name.OnSubmitted = func(s string) { c.rename(c.selected, s) }
	p.identify = widget.NewButtonWithIcon("", theme.VisibilityIcon(), func() {
		if un := c.selected; un != nil {
			c.identify(un, !c.flashing[un.LongID])
		}
	})
	p.settings = widget.NewButtonWithIcon(tr("Open settings"), theme.SettingsIcon(), func() { c.openSettings(c.selected) })
	head := widget.NewLabelWithStyle(tr("Unit"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	p.root = container.NewVBox(head, p.units, p.info,
		widget.NewLabel(tr("Place on")), p.cell,
		widget.NewLabel(tr("Name")), container.NewBorder(nil, nil, nil, p.rename, p.name),
		p.identify, p.settings)
	return p
}

// update shows the selected unit.
func (p *unitPanel) update() {
	c := p.c
	p.updating = true
	defer func() { p.updating = false }()
	p.shown = c.layout.Units()
	opts := make([]string, len(p.shown))
	for i, un := range p.shown {
		opts[i] = un.Label()
	}
	p.units.SetOptions(opts)
	selectIndex(p.units, slices.Index(p.shown, c.selected))

	un := c.selected
	p.cellOpts = []system.Cell{system.Unconfigured}
	for ph := range c.layout.Setup.Phases() {
		for a := range c.layout.ACInputs {
			p.cellOpts = append(p.cellOpts, system.Cell{Phase: ph, AC: a})
		}
	}
	cellOpts := make([]string, len(p.cellOpts))
	for i, cell := range p.cellOpts {
		cellOpts[i] = cellName(cell)
	}
	p.cell.SetOptions(cellOpts)
	controls := []fyne.Disableable{p.cell, p.name, p.rename, p.identify, p.settings}
	if un == nil {
		p.info.SetText(tr("Choose a unit here or tap it on the grid."))
		p.cell.ClearSelected()
		p.name.SetText("")
		p.identify.SetText(tr("Identify"))
		for _, w := range controls {
			w.Disable()
		}
		return
	}
	for _, w := range controls {
		w.Enable()
	}
	cell, _, _ := c.layout.CellOf(un)
	selectIndex(p.cell, slices.Index(p.cellOpts, cell))
	p.name.SetText(un.Name)
	address := tr("unknown")
	if a, ok := c.active[un.LongID]; ok {
		address = strconv.Itoa(int(a))
	}
	p.info.SetText(tr("Long ID {{.LongID}}\nFirmware {{.Firmware}}, compatibility ID {{.Compat}}\n{{.Kind}}\nAddress {{.Address}}\nConfiguration {{.Record}}",
		args{"LongID": fmt.Sprintf("%08X", un.LongID), "Firmware": strconv.FormatUint(uint64(un.Firmware()), 10),
			"Compat": un.CompatibilityID(), "Kind": unitKindText(un), "Address": address, "Record": un.Record.Hex()}))
	if c.flashing[un.LongID] {
		p.identify.SetText(tr("Stop identifying"))
	} else {
		p.identify.SetText(tr("Identify (flash the LEDs)"))
	}
	_, onBus := c.active[un.LongID]
	if c.sess == nil || !onBus {
		p.identify.Disable()
		p.settings.Disable()
	}
}

// selectIndex selects option i of s, or nothing when i < 0.
func selectIndex(s *widget.Select, i int) {
	if i < 0 {
		s.ClearSelected()
		return
	}
	s.SetSelectedIndex(i)
}

// rename gives un a name on this computer, kept by long ID.
func (c *sysConfig) rename(un *system.Unit, name string) {
	if un == nil {
		return
	}
	un.Name = name
	c.u.app.Preferences().SetString(unitNamePref(un.LongID), name)
	c.refresh()
}

// identify starts or stops flashing the LEDs of un.
func (c *sysConfig) identify(un *system.Unit, on bool) {
	id := un.LongID
	go func() {
		err := c.sess.Do(c.ctx, func(ctx context.Context, cl *mk2.Client) error {
			if err := c.openBus(ctx, cl); err != nil {
				return err
			}
			return c.bs.FlashLEDs(ctx, id, on)
		})
		fyne.Do(func() {
			if err != nil {
				c.showFailure(tr("Identify"), err, "")
				return
			}
			if on {
				c.flashing[id] = true
			} else {
				delete(c.flashing, id)
			}
			c.refresh()
		})
	}()
}

// openSettings reads the settings of un in the main window.
func (c *sysConfig) openSettings(un *system.Unit) {
	a, ok := c.active[un.LongID]
	if !ok {
		return
	}
	c.u.unit = a
	c.u.win.RequestFocus()
	getSettings(c.u)
}

// sendConfiguration checks the units, asks for confirmation and sends the
// layout.
func (c *sysConfig) sendConfiguration() {
	var dry *bus.WriteReport
	l := c.layout
	c.run(tr("Send configuration"), func(ctx context.Context, bs *bus.Session, p bus.Progress) error {
		var err error
		dry, err = bs.Write(ctx, l, bus.WriteOptions{}, p)
		return err
	}, func(err error) {
		if err != nil {
			c.showFailure(tr("Send configuration"), err, "")
			return
		}
		c.confirmSend(l, dry)
	})
}

// confirmSend shows what a send changes and sends l on confirmation. When
// assistants are removed that needs a separate tick.
func (c *sysConfig) confirmSend(l *system.Layout, dry *bus.WriteReport) {
	lines := container.NewVBox()
	for _, r := range dry.Units {
		change := tr("unchanged")
		if r.Old != r.New {
			change = tr("changes")
		}
		lines.Add(widget.NewLabel(tr("{{.Unit}} · {{.Cell}} · {{.Change}}",
			args{"Unit": r.Unit.Label(), "Cell": cellName(r.Cell), "Change": change})))
	}
	intro := widget.NewLabel(trn("Sending writes the configuration to {{.Count}} unit and restarts the system. All units switch off for a moment, so loads lose power.",
		"Sending writes the configuration to {{.Count}} units and restarts the system. All units switch off for a moment, so loads lose power.", len(dry.Units)))
	intro.Wrapping = fyne.TextWrapWord
	content := container.NewVBox(intro, lines)
	var removeOK *widget.Check
	var d *dialog.ConfirmDialog
	if len(dry.AssistantsRemoved) > 0 {
		warn := widget.NewLabel(tr("Sending this configuration removes all assistants from {{.Units}}.",
			args{"Units": unitLabels(dry.AssistantsRemoved)}))
		warn.Wrapping = fyne.TextWrapWord
		warn.Importance = widget.DangerImportance
		removeOK = widget.NewCheck(tr("Remove the assistants"), nil)
		content.Add(warn)
		content.Add(removeOK)
	}
	d = dialog.NewCustomConfirm(tr("Send configuration"), tr("Send"), tr("Cancel"), content, func(ok bool) {
		if ok {
			c.write(l, removeOK != nil && removeOK.Checked)
		}
	}, c.win)
	d.SetConfirmImportance(widget.DangerImportance)
	d.Resize(fyne.NewSize(560, d.MinSize().Height))
	d.Show()
}

// write sends l and shows the outcome. The units are found again after the
// restart, and the layout their records give is shown.
func (c *sysConfig) write(l *system.Layout, removeAssistants bool) {
	var rep *bus.WriteReport
	var active map[uint32]byte
	c.run(tr("Send configuration"), func(ctx context.Context, bs *bus.Session, p bus.Progress) error {
		var err error
		rep, err = bs.Write(ctx, l, bus.WriteOptions{Confirm: true, RemoveAssistants: removeAssistants}, p)
		active = activeAddresses(bs)
		return err
	}, func(err error) {
		if rep != nil && rep.After != nil {
			c.applyScan(rep.After, active)
		}
		if err != nil {
			c.showFailure(tr("Send configuration"), err, writeDetails(rep))
			return
		}
		showMessageOn(c.win, tr("Send configuration"), tr("The configuration was sent. Every unit restarted with it."))
	})
}

// writeDetails lists what happened to each unit that was written.
func writeDetails(rep *bus.WriteReport) string {
	if rep == nil {
		return ""
	}
	var s string
	for _, r := range rep.Units {
		if r.Writes == 0 {
			continue
		}
		s += tr("{{.Unit}}: {{.Status}}", args{"Unit": r.Unit.Label(), "Status": statusText(r.Status)}) + "\n"
	}
	return s
}

// showMessageOn shows a message dialog over w.
func showMessageOn(w fyne.Window, title, msg string) {
	d := dialog.NewInformation(title, msg, w)
	d.SetDismissText(tr("OK"))
	d.Show()
}

// showSetup lets the user choose the phases, AC inputs, L2 option and
// which AC inputs switch as a group.
func (c *sysConfig) showSetup() {
	l := c.layout
	names := make([]string, len(setups))
	for i, s := range setups {
		names[i] = setupName(s)
	}
	setup := widget.NewSelect(names, nil)
	setup.SetSelectedIndex(slices.Index(setups, l.Setup))
	inputs := widget.NewSelect([]string{"1", "2", "3", "4"}, nil)
	inputs.SetSelectedIndex(l.ACInputs - 1)
	l2Names := make([]string, len(l2Options))
	for i, o := range l2Options {
		l2Names[i] = l2Name(o)
	}
	l2 := widget.NewSelect(l2Names, nil)
	l2.SetSelectedIndex(slices.Index(l2Options, l.L2))
	var groups [system.MaxACInputs]*widget.Check
	groupBox := container.NewVBox()
	for i := range groups {
		groups[i] = widget.NewCheck(tr("AC input {{.Input}} switches as a group", args{"Input": strconv.Itoa(i + 1)}), nil)
		groups[i].SetChecked(l.Grouped[i])
		groupBox.Add(groups[i])
	}
	sync := func() {
		if setups[max(setup.SelectedIndex(), 0)].Phases() > 1 {
			l2.Enable()
		} else {
			l2.Disable()
		}
		for i, g := range groups {
			g.Hidden = i > inputs.SelectedIndex()
		}
		groupBox.Refresh()
	}
	setup.OnChanged = func(string) { sync() }
	inputs.OnChanged = func(string) { sync() }
	sync()
	form := widget.NewForm(
		widget.NewFormItem(tr("Phases"), setup),
		widget.NewFormItem(tr("AC inputs"), inputs),
		widget.NewFormItem(tr("L2 and L3"), l2),
		widget.NewFormItem(tr("Grouping"), groupBox))
	d := dialog.NewCustomConfirm(tr("System setup"), tr("Apply"), tr("Cancel"), form, func(ok bool) {
		if !ok {
			return
		}
		if err := l.Reshape(setups[setup.SelectedIndex()], inputs.SelectedIndex()+1); err != nil {
			showMessageOn(c.win, tr("System setup"), err.Error())
			return
		}
		l.L2 = l2Options[l2.SelectedIndex()]
		for i, g := range groups {
			l.Grouped[i] = g.Checked
		}
		c.refresh()
	}, c.win)
	d.Resize(fyne.NewSize(480, d.MinSize().Height))
	d.Show()
}

// openFile loads a .vms file. Units found on the bus take the file's place
// for the same long ID, so the check uses what the units report.
func (c *sysConfig) openFile() {
	dlg := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil {
			showMessageOn(c.win, tr("Error"), err.Error())
			return
		}
		if r == nil {
			return
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err == nil {
			err = c.loadFile(r.URI().Name(), data)
		}
		if err != nil {
			showMessageOn(c.win, tr("Error"), tr("Cannot open {{.File}}: {{.Err}}", args{"File": r.URI().Name(), "Err": err}))
		}
	}, c.win)
	dlg.SetFilter(vmsFilter)
	dlg.Show()
}

// loadFile shows the layout of the .vms file data.
func (c *sysConfig) loadFile(name string, data []byte) error {
	f, err := vms.Parse(data)
	if err != nil {
		return err
	}
	l, err := f.Layout(c.busUnits)
	if err != nil {
		return err
	}
	c.layout, c.file, c.inconsistent = l, name, false
	c.status.SetText(tr("Layout from {{.File}}.", args{"File": name}))
	c.refresh()
	return nil
}

// fileData returns the layout as a .vms file.
func (c *sysConfig) fileData() ([]byte, error) { return vms.FromLayout(c.layout).MarshalBinary() }

// saveFile saves the layout as a .vms file.
func (c *sysConfig) saveFile() {
	data, err := c.fileData()
	if err != nil {
		showMessageOn(c.win, tr("Error"), err.Error())
		return
	}
	dlg := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil {
			showMessageOn(c.win, tr("Error"), err.Error())
			return
		}
		if w == nil {
			return
		}
		_, err = w.Write(data)
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			showMessageOn(c.win, tr("Error"), err.Error())
			return
		}
		c.file = w.URI().Name()
	}, c.win)
	dlg.SetFilter(vmsFilter)
	name := c.file
	if name == "" {
		name = "system.vms"
	}
	dlg.SetFileName(name)
	dlg.Show()
}
