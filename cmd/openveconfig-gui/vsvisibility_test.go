package main

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// TestVSGroupShown checks which virtual switch pages each usage shows, as
// the original does: VS options also for the dedicated usages 3 and 4
// when the unit can change the period time on Udc (flag 35).
func TestVSGroupShown(t *testing.T) {
	with35 := vsDevice()
	without35 := vebus.NewDevice(nil)
	without35.AddSetting(60, mk2.SettingInfo{Sc: 1, Max: 0x0004}, 0) // flag 34 only
	for _, tt := range []struct {
		d     *vebus.Device
		usage uint16
		want  string // shown groups: U usage, A on, B off, O options, 2 VS2
	}{
		{with35, 0, "U"}, {with35, 1, "UABO"}, {with35, 2, "UABO"}, {with35, 3, "UO2"}, {with35, 4, "UO2"},
		{with35, 5, "UABO2"}, {with35, 6, "UABO2"}, {without35, 3, "U2"}, {without35, 4, "U2"}, {without35, 1, "UABO"},
	} {
		got := ""
		for _, g := range []struct{ code, group string }{
			{"U", "Usage"}, {"A", "On conditions"}, {"B", "Off conditions"}, {"O", "Options"}, {"2", "Virtual switch 2"},
		} {
			if vsGroupShown(g.group, tt.usage, tt.d) {
				got += g.code
			}
		}
		if got != tt.want {
			t.Errorf("usage %d (flag 35 %v): %s, want %s", tt.usage, tt.d.Supported(vebus.FlagID(35)), got, tt.want)
		}
	}
}

// TestVSOptionsDedicated checks the options page in a dedicated usage:
// no minimum on time, and the period time follows Udc only.
func TestVSOptionsDedicated(t *testing.T) {
	u := newTestUI(t)
	p := newSubPages(u, vebus.TabVirtualSwitch)
	d := vsDevice()
	u.settings.load(d, nil)
	opt := vsSub(t, p, "Options")
	minimum, onVS, onUdc := rowOf(t, opt, vebus.SettingID(43)), rowOf(t, opt, vebus.FlagID(34)), rowOf(t, opt, vebus.FlagID(35))
	if !minimum.root.Visible() || onVS.focus.(*widget.Check).Disabled() {
		t.Fatal("usage 1: the minimum on time or the VS period check is not available")
	}
	d.SetRaw(vebus.SettingID(15), 3)
	u.settings.changed()
	if !onVS.focus.(*widget.Check).Disabled() || onUdc.focus.(*widget.Check).Disabled() {
		t.Errorf("usage 3: VS period check disabled %v, Udc check disabled %v; want true, false",
			onVS.focus.(*widget.Check).Disabled(), onUdc.focus.(*widget.Check).Disabled())
	}
	if visibleUnder(opt.content, minimum.root) {
		t.Error("usage 3: the minimum on time is shown")
	}
}

// visibleUnder reports whether o is in the tree under root with every
// object on the way, o included, visible.
func visibleUnder(root, o fyne.CanvasObject) bool {
	if !root.Visible() {
		return false
	}
	if root == o {
		return true
	}
	switch x := root.(type) {
	case *fyne.Container:
		for _, c := range x.Objects {
			if visibleUnder(c, o) {
				return true
			}
		}
	case fyne.Widget:
		for _, c := range test.WidgetRenderer(x).Objects() {
			if visibleUnder(c, o) {
				return true
			}
		}
	}
	return false
}
