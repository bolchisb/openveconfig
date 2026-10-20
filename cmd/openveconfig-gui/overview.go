package main

// The Overview page: the connection, the unit whose settings Get reads,
// and the live values of the connected device as tiles (monitor.go).

import (
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// overviewContent returns the content of the Overview page: the
// connection, the unit whose settings Get reads, and the live values.
func overviewContent(u *ui) fyne.CanvasObject {
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	var addrs []byte // parallel to units.Options
	units := widget.NewSelect(nil, nil)
	units.OnChanged = func(string) {
		if i := units.SelectedIndex(); i >= 0 && i < len(addrs) {
			u.unit = addrs[i]
		}
	}
	unitBox := container.NewGridWrap(fyne.NewSize(200, units.MinSize().Height), units)
	get := widget.NewButtonWithIcon(tr("Get settings"), theme.DownloadIcon(), func() { getSettings(u) })
	connect := widget.NewButton(tr("Com port…"), func() { u.conn.show() })
	connect.Importance = widget.HighImportance
	empty := emptyState(theme.ComputerIcon(), tr("No live values"),
		tr("Live values are shown here while a device is connected."))
	update := func() {
		s := u.sess
		if s == nil {
			status.SetText(tr("Not connected. The settings shown come from a file or from an earlier connection."))
			unitBox.Hide()
			get.Hide()
			connect.Show()
			empty.Show()
			return
		}
		status.SetText(describe(s))
		addrs = s.devices
		opts := make([]string, len(addrs))
		for i, a := range addrs {
			opts[i] = tr("Unit at address {{.Address}}", args{"Address": a})
		}
		if !slices.Equal(opts, units.Options) {
			units.SetOptions(opts)
		}
		units.SetSelectedIndex(slices.Index(addrs, u.unit))
		unitBox.Hidden = len(addrs) < 2
		unitBox.Refresh()
		get.Show()
		get.Importance = widget.MediumImportance
		if u.settings.dev == nil {
			get.Importance = widget.HighImportance
		}
		get.Refresh()
		connect.Hide()
		empty.Hide()
	}
	u.onSession(func(s *session) {
		if s != nil {
			u.unit = s.target
		}
		update()
	})
	u.settings.onChange(func(bool) { update() })
	update()
	conn := widget.NewCard("", tr("Connection"), container.NewVBox(status, container.NewHBox(unitBox, get, connect)))
	heading := widget.NewLabelWithStyle(tr("Live values"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	heading.SizeName = theme.SizeNameSubHeadingText
	return container.NewVBox(conn, heading, empty, monitorPanel(u))
}

// emptyState returns a card that says why there is nothing to show: an
// icon, a title and a hint.
func emptyState(icon fyne.Resource, title, hint string) fyne.CanvasObject {
	t := widget.NewLabelWithStyle(title, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	h := mutedText(hint)
	h.Segments[0].(*widget.TextSegment).Style.Alignment = fyne.TextAlignCenter
	img := container.NewGridWrap(fyne.NewSquareSize(40), widget.NewIcon(theme.NewDisabledResource(icon)))
	return widget.NewCard("", "", container.NewVBox(container.NewCenter(img), t, h))
}
