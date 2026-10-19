package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// monitorPanel returns the compact live readings shown under the tabs. Stub.
func monitorPanel(u *ui) fyne.CanvasObject {
	return widget.NewLabel(tr("Not implemented yet."))
}

// showVEBusMonitor opens the VE.Bus monitor. Stub.
func showVEBusMonitor(u *ui) {
	showMessage(u, tr("VE.Bus monitor"), tr("Not implemented yet."))
}
