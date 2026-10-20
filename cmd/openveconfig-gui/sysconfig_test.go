package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/simulator"
	"github.com/bolchisb/openveconfig/pkg/vebus/system"
	"github.com/bolchisb/openveconfig/pkg/vebus/system/bus"
)

// The System Configurator tests run on simulated buses with short
// timeouts: units restart in 50 ms, system replies time out after 100 ms
// and the session waits 100 ms after a reset. A scan still takes about
// 0.6 s per unit plus 1.2 s.

// systemTestSession returns a demo session on a simulated bus of devs,
// without the connection steps, and the simulator.
func systemTestSession(t *testing.T, devs []simulator.Device) (*session, *simulator.Simulator) {
	t.Helper()
	orig := systemOptions
	t.Cleanup(func() { systemOptions = orig })
	systemOptions = bus.Options{ResetWait: 100 * time.Millisecond, PostResetWindow: 5 * time.Second}
	sim := simulator.New(simulator.Config{Devices: devs, VersionInterval: -1, ResetDuration: 50 * time.Millisecond})
	s := &session{demo: true, line: sim.Port(), lock: make(chan struct{}, 1), firmware: map[byte]uint32{},
		client: mk2.NewClient(sim.Port(), &mk2.Options{Timeout: 100 * time.Millisecond, RawTimeout: 100 * time.Millisecond, IgnoreMissingWriteAck: true})}
	t.Cleanup(func() { s.close() })
	return s, sim
}

// openConfigurator opens the System Configurator of a new UI on s and gets
// the configuration.
func openConfigurator(t *testing.T, s *session) (*ui, *sysConfig) {
	t.Helper()
	u := newTestUI(t)
	u.setSession(s)
	c := newSysConfig(u, s)
	t.Cleanup(c.win.Close)
	c.win.Resize(fyne.NewSize(1100, 700))
	c.getConfiguration()
	c.ops.Wait()
	return u, c
}

// placeAll puts the units of the last scan into cells, in order.
func placeAll(t *testing.T, c *sysConfig, cells ...system.Cell) {
	t.Helper()
	if len(c.busUnits) != len(cells) {
		t.Fatalf("%d units found, want %d", len(c.busUnits), len(cells))
	}
	for i, un := range c.busUnits {
		c.place(un, cells[i])
	}
}

// grid returns the long IDs in every cell of c's layout, second inputs
// marked with a negative sign.
func grid(c *sysConfig) map[system.Cell][]int64 {
	g := map[system.Cell][]int64{}
	add := func(cell system.Cell) {
		for _, e := range c.layout.Entries(cell) {
			id := int64(e.Unit.LongID)
			if e.Second {
				id = -id
			}
			g[cell] = append(g[cell], id)
		}
	}
	for p := range c.layout.Setup.Phases() {
		for a := range c.layout.ACInputs {
			add(system.Cell{Phase: p, AC: a})
		}
	}
	add(system.Unconfigured)
	return g
}

func sameGrid(a, b map[system.Cell][]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !slices.Equal(v, b[k]) {
			return false
		}
	}
	return true
}

func TestSystemConfiguratorParallelFromScratch(t *testing.T) {
	s, sim := systemTestSession(t, simulator.SystemFactory(2))
	_, c := openConfigurator(t, s)
	// New units all hold the factory record: inconsistent, all unplaced.
	if !c.inconsistent || len(c.layout.Entries(system.Unconfigured)) != 2 || !c.send.Disabled() {
		t.Fatalf("after scan: inconsistent %v, unconfigured %v, send disabled %v",
			c.inconsistent, c.layout.Entries(system.Unconfigured), c.send.Disabled())
	}
	l1 := system.Cell{Phase: 0, AC: 0}
	placeAll(t, c, l1, l1)
	if c.send.Disabled() {
		t.Fatalf("send disabled: %q, issues %v", c.reason.String(), c.issueList())
	}
	want := grid(c)
	c.write(c.layout, true) // new units: the assistant flag is clear
	c.ops.Wait()
	if c.inconsistent || !sameGrid(grid(c), want) {
		t.Errorf("after the write: inconsistent %v, grid %v, want %v", c.inconsistent, grid(c), want)
	}
	for _, un := range sim.Units() {
		if r := system.Record(un.ActiveRecord); r.PresenceMask() != 0b1001 || un.Resets == 0 {
			t.Errorf("unit %08X uses %s, %d resets", un.LongID, r.Hex(), un.Resets)
		}
	}
}

func TestSystemConfiguratorThreePhaseFromScratch(t *testing.T) {
	s, _ := systemTestSession(t, simulator.SystemFactory(3))
	_, c := openConfigurator(t, s)
	if err := c.layout.Reshape(system.ThreePhase, 1); err != nil {
		t.Fatal(err)
	}
	placeAll(t, c, system.Cell{Phase: 0}, system.Cell{Phase: 1}, system.Cell{Phase: 2})
	want := grid(c)
	c.write(c.layout, true)
	c.ops.Wait()
	if c.layout.Setup != system.ThreePhase || !sameGrid(grid(c), want) {
		t.Errorf("after the write: %v, grid %v, want %v", c.layout.Setup, grid(c), want)
	}
	// Discovering again shows the same layout.
	c.getConfiguration()
	c.ops.Wait()
	if c.layout.Setup != system.ThreePhase || !sameGrid(grid(c), want) {
		t.Errorf("after a new scan: %v, grid %v, want %v", c.layout.Setup, grid(c), want)
	}
}

func TestSystemConfiguratorBlockingRules(t *testing.T) {
	// Every blocking rule has its own explanation.
	for _, r := range []system.Rule{system.RuleMixedKinds, system.RuleFirmware, system.RuleFamily,
		system.RuleInverterInputs, system.RuleMaxUnits, system.RuleMaxPerPhase, system.RuleLetterPerPhase,
		system.RuleMP2Inputs, system.RuleQP2Inputs, system.RuleGroupWithoutMulti, system.RuleEmptyPhase,
		system.RuleUnplaced, system.RuleConfigProtocol, system.RuleBusMismatch} {
		if issueText(system.Issue{Rule: r, Message: "untranslated"}) == "untranslated" {
			t.Errorf("rule %s has no explanation", r)
		}
	}

	// A mixed-firmware bus: E2 is shown and blocks sending.
	s, _ := systemTestSession(t, simulator.SystemMixedFirmware())
	_, c := openConfigurator(t, s)
	issues := c.issueList()
	if !slices.ContainsFunc(issues, func(is system.Issue) bool { return is.Rule == system.RuleFirmware }) || !c.send.Disabled() {
		t.Errorf("issues %v, send disabled %v", issues, c.send.Disabled())
	}
	shown := false
	for _, o := range c.issues.Objects {
		shown = shown || findText(o, "All units must run the same firmware version")
	}
	if !shown {
		t.Error("E2 is not shown with its explanation")
	}
}

func TestSystemConfiguratorFile(t *testing.T) {
	s, _ := systemTestSession(t, simulator.SystemThreePhase())
	u, c := openConfigurator(t, s)
	c.rename(c.busUnits[0], "north")
	want := grid(c)
	data, err := c.fileData()
	if err != nil {
		t.Fatal(err)
	}
	// A new window without a connection shows the same grid.
	c2 := newSysConfig(u, nil)
	t.Cleanup(c2.win.Close)
	if err := c2.loadFile("site.vms", data); err != nil {
		t.Fatal(err)
	}
	if !sameGrid(grid(c2), want) || c2.layout.Units()[0].Name != "north" || !c2.send.Disabled() {
		t.Errorf("loaded grid %v, want %v; first unit %q", grid(c2), want, c2.layout.Units()[0].Name)
	}
	// Loaded while connected, the units of the bus take the file's place.
	if err := c.loadFile("site.vms", data); err != nil {
		t.Fatal(err)
	}
	if !sameGrid(grid(c), want) || c.layout.Units()[0] != c.busUnits[0] || c.send.Disabled() {
		t.Errorf("loaded on the bus: grid %v, own units %v, send disabled %v", grid(c), c.layout.Units()[0] == c.busUnits[0], c.send.Disabled())
	}
	// The name is kept on this computer by long ID.
	if got := u.app.Preferences().String(unitNamePref(c.busUnits[0].LongID)); got != "north" {
		t.Errorf("saved name %q", got)
	}
}

func TestSystemConfiguratorDragAndDrop(t *testing.T) {
	s, _ := systemTestSession(t, simulator.SystemFactory(2))
	_, c := openConfigurator(t, s)
	c.win.Show()
	var tile *unitTile
	for _, cv := range c.cells {
		if !cv.cell.Configured() {
			tile = findTile(cv.root)
		}
	}
	target := c.cells[0] // L1 AC1
	if tile == nil || target.cell != (system.Cell{Phase: 0, AC: 0}) {
		t.Fatalf("tile %v, first cell %v", tile, target.cell)
	}
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(target.root).Add(fyne.NewPos(5, 5))
	un := tile.entry.Unit
	tile.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{AbsolutePosition: pos}})
	tile.DragEnd()
	if cell, _, _ := c.layout.CellOf(un); cell != target.cell || c.selected != un {
		t.Errorf("dropped in %v (selected %v), want %v", cell, c.selected == un, target.cell)
	}
	// The keyboard alternative: choose the unit, then the cell.
	other := c.busUnits[1]
	if other == un {
		other = c.busUnits[0]
	}
	c.panel.units.SetSelectedIndex(slices.Index(c.panel.shown, other))
	c.panel.cell.SetSelectedIndex(slices.Index(c.panel.cellOpts, target.cell))
	if cell, _, _ := c.layout.CellOf(other); cell != target.cell {
		t.Errorf("keyboard: %v, want %v", cell, target.cell)
	}
}

func TestSystemConfiguratorOpenSettings(t *testing.T) {
	s, _ := systemTestSession(t, simulator.SystemParallel2())
	u, c := openConfigurator(t, s)
	un := c.busUnits[1]
	c.selectUnit(un)
	c.openSettings(un)
	waitFor(t, "settings", func() bool {
		var d bool
		fyne.DoAndWait(func() { d = u.settings.dev != nil })
		return d
	})
	if u.unit != 3 || u.settings.dev.Address != 3 {
		t.Errorf("unit %d, settings of %d; want the unit at address 3", u.unit, u.settings.dev.Address)
	}
}

// findTile returns the first unit tile under o.
func findTile(o fyne.CanvasObject) *unitTile {
	switch x := o.(type) {
	case *unitTile:
		return x
	case *fyne.Container:
		for _, c := range x.Objects {
			if t := findTile(c); t != nil {
				return t
			}
		}
	}
	return nil
}

// findText reports whether a label under o contains text.
func findText(o fyne.CanvasObject, text string) bool {
	switch x := o.(type) {
	case *widget.Label:
		return strings.Contains(x.Text, text)
	case *fyne.Container:
		return slices.ContainsFunc(x.Objects, func(c fyne.CanvasObject) bool { return findText(c, text) })
	}
	return false
}
