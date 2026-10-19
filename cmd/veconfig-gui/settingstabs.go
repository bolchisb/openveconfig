package main

import (
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// settingsTabs returns the tabs of the device settings. Stub.
func settingsTabs(u *ui) []*container.TabItem {
	return []*container.TabItem{
		container.NewTabItem(tr("Settings"), widget.NewLabel(tr("Not implemented yet."))),
	}
}
