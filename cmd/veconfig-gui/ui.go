package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"golang.org/x/text/language/display"
)

// ui is the application and its main window. Its fields and methods are
// used on the UI goroutine only; code on other goroutines goes through
// fyne.Do.
type ui struct {
	app fyne.App
	win fyne.Window
	// sess is the open connection; nil when disconnected.
	sess      *session
	listeners []func(s *session)
}

// newUI builds the main window of a.
func newUI(a fyne.App) *ui {
	u := &ui{app: a, win: a.NewWindow(appName)}
	tabs := container.NewAppTabs(append(settingsTabs(u), gridTab(u))...)
	top := container.NewVBox(demoBanner(u), connectionBar(u))
	u.win.SetContent(container.NewBorder(top, monitorPanel(u), nil, nil, tabs))
	u.win.SetMainMenu(mainMenu(u))
	u.win.Resize(fyne.NewSize(900, 600))
	u.win.SetOnClosed(func() { u.setSession(nil) })
	u.onSession(func(s *session) {
		title := appName
		if s != nil && s.demo {
			title = tr("{{.App}} - DEMO, no hardware", args{"App": appName})
		}
		u.win.SetTitle(title)
	})
	return u
}

// onSession registers f to be called on the UI goroutine whenever a session
// opens (s != nil) or closes (s == nil). f is not called for the session
// that is open when it registers; read u.sess for that.
func (u *ui) onSession(f func(s *session)) { u.listeners = append(u.listeners, f) }

// setSession makes s the open session, closing the previous one after the
// listeners have heard of the change. s may be nil.
func (u *ui) setSession(s *session) {
	old := u.sess
	if old == s {
		return
	}
	u.sess = s
	for _, f := range u.listeners {
		f(s)
	}
	if old != nil {
		old.close()
	}
}

// demoBanner returns a coloured strip that is visible while a demo session
// is open.
func demoBanner(u *ui) fyne.CanvasObject {
	bg := canvas.NewRectangle(theme.Color(theme.ColorNameWarning))
	text := canvas.NewText(tr("DEMO MODE: simulated devices, no hardware is connected. Nothing is written to a real device."),
		theme.Color(theme.ColorNameForegroundOnWarning))
	text.TextStyle.Bold = true
	text.Alignment = fyne.TextAlignCenter
	banner := container.NewStack(bg, container.NewPadded(text))
	banner.Hide()
	u.onSession(func(s *session) {
		if s != nil && s.demo {
			banner.Show()
		} else {
			banner.Hide()
		}
	})
	return banner
}

// mainMenu returns the window menu.
func mainMenu(u *ui) *fyne.MainMenu {
	file := fyne.NewMenu(tr("File"), &fyne.MenuItem{Label: tr("Quit"), IsQuit: true})
	tools := fyne.NewMenu(tr("Tools"),
		fyne.NewMenuItem(tr("Remote panel"), func() { showRemotePanel(u) }),
		fyne.NewMenuItem(tr("VE.Bus monitor"), func() { showVEBusMonitor(u) }),
	)
	language := fyne.NewMenuItem(tr("Language"), nil)
	language.ChildMenu = languageMenu(u)
	options := fyne.NewMenu(tr("Options"), language)
	return fyne.NewMainMenu(file, tools, options)
}

// languageMenu lists the OS default and every language with a catalog. A
// choice is saved and applies at the next start.
func languageMenu(u *ui) *fyne.Menu {
	m := fyne.NewMenu("")
	add := func(label, tag string) {
		item := fyne.NewMenuItem(label, nil)
		item.Checked = u.app.Preferences().String(prefLanguage) == tag
		item.Action = func() {
			u.app.Preferences().SetString(prefLanguage, tag)
			for _, it := range m.Items {
				it.Checked = it == item
			}
			m.Refresh()
			showMessage(u, tr("Language"), tr("The new language is used after you restart the program."))
		}
		m.Items = append(m.Items, item)
	}
	add(tr("System default"), "")
	for _, t := range languages() {
		add(display.Self.Name(t), t.String())
	}
	return m
}
