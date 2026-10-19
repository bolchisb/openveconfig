// Command veconfig-gui is the graphical VE.Bus configuration tool.
package main

import "fyne.io/fyne/v2/app"

// appID identifies the app to Fyne, which keeps its preferences under it.
const appID = "io.github.bolchisb.victron-veconfigure-ported"

// appName is the product name. It is not translated.
const appName = "VEConfigure Ported"

func main() {
	a := app.NewWithID(appID)
	setLanguage(a.Preferences().String(prefLanguage))
	newUI(a).win.ShowAndRun()
}
