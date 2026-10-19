// Command veconfig-gui is the graphical VE.Bus configuration tool.
package main

import (
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/widget"
)

func main() {
	a := app.New()
	w := a.NewWindow("VEConfigure Ported")
	w.SetContent(widget.NewLabel("Hello from VEConfigure Ported"))
	w.ShowAndRun()
}
