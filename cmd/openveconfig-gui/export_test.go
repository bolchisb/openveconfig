package main

import (
	"strings"
	"testing"

	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// TestSettingsReport checks the report of Export and Print settings: the
// pages with their settings as the tabs show them, and the grid code with
// its parameters.
func TestSettingsReport(t *testing.T) {
	u, _, _, d := gridSetup(t, 2699481)
	if err := d.SetRaw(vebus.SettingID(gridCodeSetting), 17); err != nil {
		t.Fatal(err)
	}
	text := reportText(u)
	for _, want := range []string{
		"\nGeneral\n", "    AC1 input current limit: 16.0 A\n",
		"    Country / grid code standard: 17 – Europe: EN50549-1:2019",
		"\n  Reconnect after trip\n", "    Waiting time: ",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q:\n%s", want, text)
		}
	}
	if h := reportHTML(u); !strings.Contains(h, "<h2>General</h2>") || !strings.Contains(h, "<td>AC1 input current limit</td><td>16.0 A</td>") {
		t.Errorf("HTML report: %s", h)
	}
}
