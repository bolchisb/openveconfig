package main

// The Help menu: the program's documentation and About, as in the original
// tool's Help menu. The documentation is the project's own (the original's
// help file is Victron's and is not shipped).

import (
	"net/url"
	"runtime/debug"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// projectURL is the home of the project and its documentation.
const projectURL = "https://github.com/bolchisb/openveconfig"

// helpMenu returns the Help menu.
func helpMenu(u *ui) *fyne.Menu {
	return fyne.NewMenu(tr("Help"),
		fyne.NewMenuItem(tr("Help on {{.App}}", args{"App": appName}), func() { openProjectPage(u, "#readme") }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(tr("About"), func() { showAbout(u) }))
}

// openProjectPage opens the project page at fragment in the browser.
func openProjectPage(u *ui, fragment string) {
	link, _ := url.Parse(projectURL + fragment)
	if err := u.app.OpenURL(link); err != nil {
		showError(u, err)
	}
}

// buildVersion returns the version of the program: the module version of a
// release build, or the commit of a local one.
func buildVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return tr("unknown")
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return tr("development build {{.Commit}}", args{"Commit": s.Value[:7]})
		}
	}
	return tr("development build")
}

// showAbout shows the program's name, version, licence and the notice that
// it is an independent project.
func showAbout(u *ui) {
	link, _ := url.Parse(projectURL)
	note := widget.NewLabel(tr("An independent community project. It is not affiliated with, endorsed by, or supported by Victron Energy. " +
		"\"Victron Energy\", \"VE.Bus\" and \"VEConfigure\" are trademarks of their respective owner. " +
		"The software is provided as is, without any warranty; writing wrong settings can damage equipment."))
	note.Wrapping = fyne.TextWrapWord
	title := widget.NewLabel(appName)
	title.TextStyle.Bold = true
	content := container.NewVBox(
		title,
		widget.NewLabel(tr("Version: {{.Version}}", args{"Version": buildVersion()})),
		widget.NewLabel(tr("Licence: MIT")),
		widget.NewHyperlink(projectURL, link),
		note,
	)
	showTableDialog(u, tr("About"), content, fyne.NewSize(480, 340), tr("Close"), nil, nil)
}
