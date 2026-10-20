// Command openveconfig-gui is the graphical VE.Bus configuration tool.
package main

import (
	_ "embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
)

// appID identifies the app to Fyne, which keeps its preferences under it.
const appID = "io.github.bolchisb.openveconfig"

// appName is the product name. It is not translated.
const appName = "OpenVeConfig"

// iconFile is the app's icon, shown on its windows and in the taskbar.
// rsrc_windows_amd64.syso carries it into the .exe for Explorer.
const iconFile = "Icon.png"

//go:generate go run github.com/tc-hib/go-winres@v0.3.3 simply --icon Icon.png --manifest none --arch amd64 --out rsrc

//go:embed Icon.png
var appIcon []byte

func main() {
	a := app.NewWithID(appID)
	a.SetIcon(fyne.NewStaticResource(iconFile, appIcon))
	setLanguage(a.Preferences().String(prefLanguage))
	newUI(a).win.ShowAndRun()
}
