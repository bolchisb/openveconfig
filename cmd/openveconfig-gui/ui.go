package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"golang.org/x/text/language/display"

	"github.com/bolchisb/openveconfig/pkg/vebus"
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
	// settings is the settings model the pages edit (settingsmodel.go).
	settings *settingsModel
	// unit is the address of the device whose settings Get reads.
	unit byte
	conn *connBar // the connection dialog

	// The shell (shell.go): the pages in sidebar order, the page shown and
	// the area that shows it.
	pages             []*page
	overview, general *page
	current           *page
	pageArea          *fyne.Container
	sidebar           *widget.List
	header            *header
	changes           *changesBar
	start, shell      fyne.CanvasObject
	recents           *fyne.Container // the recent files on the start screen
	menu              *appMenu
}

// newUI builds the main window of a. It shows the start screen while no
// device is connected and no settings are loaded, else the header, the
// sidebar with the pages and, while there are unsent changes, the changes
// bar.
func newUI(a fyne.App) *ui {
	applyTheme(a)
	u := &ui{app: a, win: a.NewWindow(appName), settings: &settingsModel{}}
	u.conn = newConnBar(u)
	u.overview = newPage(tr("Overview"), tr("The connection, the device whose settings are read, and its live values."),
		theme.HomeIcon(), vebus.TabNone, overviewContent(u))
	sp := settingsPages(u)
	u.general = sp[0]
	u.pages = append([]*page{u.overview, sp[0], gridPage(u)}, sp[1:]...)
	u.header = newHeader(u)
	u.changes = newChangesBar(u)
	u.pageArea = container.NewStack()
	u.shell = container.NewBorder(nil, nil, container.NewHBox(newSidebar(u), widget.NewSeparator()), nil,
		container.NewBorder(nil, u.changes.root, nil, nil, container.NewStack(newColourRect(colorNamePage, fyne.Size{}, ""), u.pageArea)))
	u.menu = newAppMenu(u)
	u.start = startScreen(u)
	u.win.SetContent(container.NewBorder(u.header.root, nil, nil, nil, container.NewStack(u.start, u.shell)))
	u.win.SetPadded(false)
	u.win.SetMainMenu(u.menu.main)
	for key, f := range map[fyne.KeyName]func(){
		fyne.KeyO: func() { openSettingsFile(u) },
		fyne.KeyS: func() { saveSettingsFile(u) },
		fyne.KeyF: func() { findSetting(u) },
	} {
		u.win.Canvas().AddShortcut(shortcut(key), func(fyne.Shortcut) { f() })
	}
	u.win.Resize(fyne.NewSize(1100, 720))
	u.win.SetOnClosed(func() { u.setSession(nil) })
	u.onSession(func(s *session) {
		title := appName
		if s != nil && s.demo {
			title = tr("{{.App}} - DEMO, no hardware", args{"App": appName})
		}
		u.win.SetTitle(title)
		u.update()
	})
	u.settings.onChange(func(loaded bool) {
		// A newly read or opened model lands on its first settings page.
		if loaded && u.settings.dev != nil && u.current == u.overview {
			u.showPage(u.general)
		}
		u.update()
	})
	u.showPage(u.overview)
	u.recentsChanged()
	u.update()
	return u
}

// update shows the state of the session and of the model in the shell.
func (u *ui) update() {
	if u.sess == nil && u.settings.dev == nil {
		u.shell.Hide()
		u.start.Show()
	} else {
		u.start.Hide()
		u.shell.Show()
	}
	u.sidebar.Refresh()
	u.header.update(u)
	u.changes.update(u)
	u.menu.update(u)
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

// shortcut returns the keyboard shortcut of key with the platform's
// shortcut modifier (Ctrl, or Cmd on macOS).
func shortcut(key fyne.KeyName) fyne.Shortcut {
	return &desktop.CustomShortcut{KeyName: key, Modifier: fyne.KeyModifierShortcutDefault}
}

// appMenu is the window menu, with the items whose state changes.
type appMenu struct {
	main                               *fyne.MainMenu
	recent                             *fyne.MenuItem
	connect, get, send, find, defaults *fyne.MenuItem
	review                             *fyne.MenuItem
	remote, monitor                    *fyne.MenuItem
}

func newAppMenu(u *ui) *appMenu {
	m := &appMenu{}
	item := func(label string, f func()) *fyne.MenuItem { return fyne.NewMenuItem(label, f) }
	open := item(tr("Open settings file…"), func() { openSettingsFile(u) })
	open.Shortcut = shortcut(fyne.KeyO)
	save := item(tr("Save settings file"), func() { saveSettingsFile(u) })
	save.Shortcut = shortcut(fyne.KeyS)
	m.recent = item(tr("Recent files"), nil)
	m.recent.ChildMenu = fyne.NewMenu("")
	file := fyne.NewMenu(tr("File"), open, save, m.recent, fyne.NewMenuItemSeparator(),
		&fyne.MenuItem{Label: tr("Quit"), IsQuit: true})

	m.connect = item(tr("Connect…"), func() {
		if u.sess != nil {
			u.setSession(nil)
		} else {
			u.conn.show()
		}
	})
	m.get = item(tr("Get settings"), func() { getSettings(u) })
	m.send = item(tr("Send settings"), func() { sendSettings(u) })
	m.review = item(tr("Review changes…"), func() { reviewChanges(u) })
	m.find = item(tr("Find setting…"), func() { findSetting(u) })
	m.find.Shortcut = shortcut(fyne.KeyF)
	m.defaults = item(tr("Reset all to defaults…"), func() { setAllToDefault(u) })
	device := fyne.NewMenu(tr("Device"), m.connect, fyne.NewMenuItemSeparator(), m.get, m.send, m.review,
		fyne.NewMenuItemSeparator(), m.find, fyne.NewMenuItemSeparator(), m.defaults)

	m.remote = item(tr("Remote panel"), func() { showRemotePanel(u) })
	m.monitor = item(tr("VE.Bus monitor"), func() { showVEBusMonitor(u) })
	tools := fyne.NewMenu(tr("Tools"), m.remote, m.monitor)

	language := item(tr("Language"), nil)
	language.ChildMenu = languageMenu(u)
	look := item(tr("Theme"), nil)
	look.ChildMenu = themeMenu(u)
	options := fyne.NewMenu(tr("Options"), language, look)
	m.main = fyne.NewMainMenu(file, device, tools, options)
	return m
}

// update enables the items that can act on the session and the model.
func (m *appMenu) update(u *ui) {
	connected, loaded := u.sess != nil, u.settings.dev != nil
	changed := false
	set := func(it *fyne.MenuItem, enabled bool) {
		if it.Disabled == enabled {
			it.Disabled = !enabled
			changed = true
		}
	}
	set(m.get, connected)
	set(m.send, connected && loaded)
	set(m.review, loaded && len(u.settings.dev.ChangedIDs()) > 0)
	set(m.find, loaded)
	set(m.defaults, loaded)
	set(m.remote, connected)
	set(m.monitor, connected)
	label := tr("Connect…")
	if connected {
		label = tr("Disconnect")
	}
	if m.connect.Label != label {
		m.connect.Label = label
		changed = true
	}
	if changed {
		m.main.Refresh()
	}
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
