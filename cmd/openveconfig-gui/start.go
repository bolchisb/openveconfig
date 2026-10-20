package main

// The start screen, shown while nothing is open, and the recent files.

import (
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// prefRecentFiles is the preference that holds the URIs of the settings
// files opened or saved last, newest first.
const prefRecentFiles = "files.recent"

// maxRecent is the number of recent files shown. Twice as many are kept,
// so that files that were moved or deleted can be skipped.
const maxRecent = 5

// recentFiles returns the recent settings files that still exist.
func recentFiles(prefs fyne.Preferences) []fyne.URI {
	var out []fyne.URI
	for _, s := range prefs.StringList(prefRecentFiles) {
		uri, err := storage.ParseURI(s)
		if err != nil {
			continue
		}
		if ok, _ := storage.Exists(uri); ok {
			out = append(out, uri)
		}
		if len(out) == maxRecent {
			break
		}
	}
	return out
}

// addRecent records uri as the newest recent settings file.
func addRecent(u *ui, uri fyne.URI) {
	prefs := u.app.Preferences()
	list := slices.DeleteFunc(prefs.StringList(prefRecentFiles), func(s string) bool { return s == uri.String() })
	list = append([]string{uri.String()}, list...)
	prefs.SetStringList(prefRecentFiles, list[:min(len(list), 2*maxRecent)])
	u.recentsChanged()
}

// recentsChanged shows the recent files on the start screen and in the
// File menu.
func (u *ui) recentsChanged() {
	files := recentFiles(u.app.Preferences())
	u.recents.Objects = nil
	var items []*fyne.MenuItem
	for _, uri := range files {
		b := widget.NewButtonWithIcon(uri.Name(), theme.DocumentIcon(), func() { openRecentFile(u, uri) })
		b.Importance = widget.LowImportance
		b.Alignment = widget.ButtonAlignLeading
		dir, _ := storage.Parent(uri)
		where := ""
		if dir != nil {
			where = dir.Path()
		}
		u.recents.Add(container.NewBorder(nil, nil, b, nil, mutedText(where)))
		items = append(items, fyne.NewMenuItem(uri.Name(), func() { openRecentFile(u, uri) }))
	}
	if len(files) == 0 {
		u.recents.Add(mutedText(tr("Settings files you open or save are listed here.")))
		none := fyne.NewMenuItem(tr("No recent files"), nil)
		none.Disabled = true
		items = append(items, none)
	}
	u.recents.Refresh()
	u.menu.recent.ChildMenu.Items = items
	u.menu.main.Refresh()
}

// startScreen returns what the main window shows while no device is
// connected and no settings are loaded.
func startScreen(u *ui) fyne.CanvasObject {
	card := func(icon fyne.Resource, title, desc string, action *widget.Button) fyne.CanvasObject {
		text := widget.NewRichText(
			&widget.TextSegment{Text: title, Style: widget.RichTextStyle{SizeName: theme.SizeNameSubHeadingText, TextStyle: fyne.TextStyle{Bold: true}}},
			&widget.TextSegment{Text: desc, Style: widget.RichTextStyle{ColorName: theme.ColorNamePlaceHolder}})
		text.Wrapping = fyne.TextWrapWord
		img := container.NewGridWrap(fyne.NewSquareSize(32), widget.NewIcon(icon))
		body := container.NewBorder(container.NewHBox(img), container.NewHBox(action), nil, nil, text)
		return container.NewStack(newColourRect(colorNameSurface, fyne.NewSize(0, 200), theme.SizeNameCardRadius),
			container.New(layout.NewCustomPaddedLayout(12, 12, 12, 12), body))
	}
	connect := widget.NewButton(tr("Com port…"), func() { u.conn.show() })
	connect.Importance = widget.HighImportance
	open := widget.NewButton(tr("Load settings…"), func() { openSettingsFile(u) })
	demo := widget.NewButton(tr("Start the demo"), func() { u.conn.open(portChoice{demo: true}) })
	cards := container.NewGridWithColumns(3,
		card(theme.ComputerIcon(), tr("Connect a device"),
			tr("Read and change the settings of an inverter/charger through an MK2 or MK3 interface."), connect),
		card(theme.FolderOpenIcon(), tr("Open a settings file"),
			tr("Edit a .rvsc or .rvms file without a device, then save it or send it to a device."), open),
		card(theme.MediaPlayIcon(), tr("Try the demo"),
			tr("Look around with two simulated devices. No hardware is needed and nothing is written to a real device."), demo))
	title := widget.NewRichText(
		&widget.TextSegment{Text: appName, Style: widget.RichTextStyle{SizeName: theme.SizeNameHeadingText, TextStyle: fyne.TextStyle{Bold: true}}},
		&widget.TextSegment{Text: tr("Configure VE.Bus inverter/chargers: read, change and send their settings, or edit settings files."),
			Style: widget.RichTextStyle{ColorName: theme.ColorNamePlaceHolder}})
	title.Wrapping = fyne.TextWrapWord
	u.recents = container.NewVBox()
	recent := widget.NewLabel(tr("Recent files"))
	recent.TextStyle.Bold = true
	content := container.NewVBox(title, cards, recent, u.recents)
	return container.NewStack(newColourRect(colorNamePage, fyne.Size{}, ""),
		container.NewVScroll(container.New(layout.NewCustomPaddedLayout(32, 16, 0, 0), container.New(columnLayout{width: 960}, content))))
}

// columnLayout lays its objects over each other in a column of at most
// width, centred.
type columnLayout struct{ width float32 }

func (l columnLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	w := min(size.Width, l.width)
	for _, o := range objs {
		o.Move(fyne.NewPos((size.Width-w)/2, 0))
		o.Resize(fyne.NewSize(w, size.Height))
	}
}

func (l columnLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var s fyne.Size
	for _, o := range objs {
		s = s.Max(o.MinSize())
	}
	return s
}
