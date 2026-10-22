package main

// The dialog Add assistant of the Assistants page: the assistants of the
// package folder in a list that a search narrows, one selected at a time,
// with Add as its action: the walk through its questions ends in a
// summary that puts it into the open file.

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// assistantPicker is the open dialog Add assistant.
type assistantPicker struct {
	ap       *assistantsPage
	search   *widget.Entry
	count    *widget.RichText
	list     *widget.List
	body     *fyne.Container // the list, or why there is nothing in it
	shown    []availableAssistant
	selected int // index in shown; -1 for none
	add      *widget.Button
	note     *widget.RichText
	dlg      *dialog.CustomDialog
}

// showAdd opens the dialog Add assistant.
func (ap *assistantsPage) showAdd() {
	p := &assistantPicker{ap: ap, selected: -1}
	ap.picker = p
	p.search = widget.NewEntry()
	p.search.SetPlaceHolder(tr("Search by name or ID"))
	p.search.ActionItem = widget.NewIcon(theme.SearchIcon())
	p.search.OnChanged = func(string) { p.refresh() }
	p.search.OnSubmitted = func(string) { p.openSelected() }
	p.count = captionText("", theme.ColorNamePlaceHolder)

	p.list = widget.NewList(func() int { return len(p.shown) },
		func() fyne.CanvasObject {
			name := widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
			name.Truncation = fyne.TextTruncateEllipsis
			detail := captionText("", theme.ColorNamePlaceHolder)
			icon := container.NewGridWrap(fyne.NewSquareSize(32),
				container.NewStack(newColourRect(colorNamePage, fyne.Size{}, theme.SizeNameInputRadius),
					container.NewPadded(widget.NewIcon(theme.NewPrimaryThemedResource(theme.StorageIcon())))))
			return container.NewBorder(nil, nil, container.NewCenter(icon), nil,
				container.New(layout.NewCustomPaddedVBoxLayout(-6), name, detail))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			a := p.shown[i]
			text := o.(*fyne.Container).Objects[0].(*fyne.Container)
			text.Objects[0].(*widget.Label).SetText(displayTitle(a.title))
			setCaptionText(text.Objects[1].(*widget.RichText), trn("ID {{.ID}} · {{.Count}} version in the folder",
				"ID {{.ID}} · {{.Count}} versions in the folder", a.versions, args{"ID": assistantID(a.info.ID)}))
		})
	p.list.OnSelected = func(i widget.ListItemID) { p.selected = i; p.updateButtons() }
	p.list.OnUnselected = func(widget.ListItemID) { p.selected = -1; p.updateButtons() }
	p.body = container.NewStack()

	p.add = widget.NewButtonWithIcon(tr("Add"), theme.ContentAddIcon(), p.openSelected)
	p.add.Importance = widget.HighImportance
	closeButton := widget.NewButton(tr("Close"), func() { p.dlg.Hide() })
	p.note = mutedText("")

	frame := container.NewStack(newThemeRect(colorNamePage, theme.ColorNameSeparator, -1, fyne.NewSize(560, 340)), p.body)
	content := container.NewBorder(container.NewVBox(p.search, p.count), p.note, nil, nil, frame)
	p.dlg = dialog.NewCustomWithoutButtons(tr("Add assistant"), content, ap.u.win)
	p.dlg.SetButtons([]fyne.CanvasObject{closeButton, p.add})
	p.dlg.SetOnClosed(func() {
		if ap.picker == p {
			ap.picker = nil
		}
	})
	p.refresh()
	p.dlg.Show()
	ap.u.win.Canvas().Focus(p.search)
}

// displayTitle starts a package's title with a capital letter, as the
// other titles in the list do; some packages name their assistant in
// lower case.
func displayTitle(t string) string {
	r, n := utf8.DecodeRuneInString(t)
	if r == utf8.RuneError {
		return t
	}
	return string(unicode.ToUpper(r)) + t[n:]
}

// matches reports whether a matches the search q: part of its title, or
// of its ID as written in the list.
func matches(a availableAssistant, q string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	return q == "" || strings.Contains(strings.ToLower(a.title), q) || strings.Contains(strings.ToLower(assistantID(a.info.ID)), q)
}

// refresh shows the assistants of the folder read last that match the
// search, keeping the selection when it still matches.
func (p *assistantPicker) refresh() {
	s := p.ap.scan
	var keep uint16
	had := p.selected >= 0 && p.selected < len(p.shown)
	if had {
		keep = p.shown[p.selected].info.ID
	}
	p.shown = nil
	if s != nil {
		for _, a := range s.available {
			if matches(a, p.search.Text) {
				p.shown = append(p.shown, a)
			}
		}
	}
	var count string
	switch {
	case s == nil:
		p.body.Objects = []fyne.CanvasObject{container.NewCenter(mutedText(tr("Reading the folder…")))}
	case s.err != nil:
		p.body.Objects = []fyne.CanvasObject{emptyState(theme.FolderIcon(), tr("No assistant folder"),
			tr("This folder cannot be read. Choose the folder into which VEConfigure downloaded its assistants, or download them under Assistant Tools."))}
	case len(s.available) == 0:
		p.body.Objects = []fyne.CanvasObject{emptyState(theme.FolderIcon(), tr("No assistant packages"),
			tr("This folder has no assistant packages. Choose the folder into which VEConfigure downloaded its assistants, or download them under Assistant Tools."))}
	case len(p.shown) == 0:
		p.body.Objects = []fyne.CanvasObject{emptyState(theme.SearchIcon(), tr("No assistant matches “{{.Query}}”", args{"Query": strings.TrimSpace(p.search.Text)}),
			tr("Search for part of the name, such as “generator”, or for the ID, such as 0x0190."))}
	default:
		p.body.Objects = []fyne.CanvasObject{p.list}
		count = trn("{{.Count}} assistant", "{{.Count}} assistants", len(p.shown))
		if q := strings.TrimSpace(p.search.Text); q != "" {
			count = trn("{{.Shown}} of {{.Count}} assistant", "{{.Shown}} of {{.Count}} assistants", len(s.available), args{"Shown": len(p.shown)})
		}
	}
	if s != nil && s.err == nil && s.bad > 0 {
		bad := trn("{{.Count}} file in this folder is not an assistant package that can be read.",
			"{{.Count}} files in this folder are not assistant packages that can be read.", s.bad)
		if count != "" {
			count += " · "
		}
		count += bad
	}
	setCaptionText(p.count, count)
	p.body.Refresh()

	p.selected = -1
	p.list.UnselectAll()
	for i, a := range p.shown {
		if had && a.info.ID == keep {
			p.list.Select(i)
		}
	}
	if p.selected < 0 && len(p.shown) > 0 {
		p.list.Select(0)
	}
	p.list.Refresh()
	p.updateButtons()
}

// updateButtons shows whether Add can run, and says under the list what
// it still wants: an assistant to be chosen, or a file to put it into.
func (p *assistantPicker) updateButtons() {
	note := tr("Add walks through the assistant's questions and its summary puts it into the open " +
		"file, which you then save as usual.")
	switch {
	case p.ap.u.settings.file == nil:
		note = tr("Open a settings file first: an assistant is added to a file, which you then save as usual.")
	case p.selected < 0 || p.selected >= len(p.shown):
		note = tr("Choose an assistant to add. Its questions are walked through, and its summary puts it " +
			"into the open file, which you then save as usual.")
	}
	setMutedText(p.note, note)
	if p.canAdd() {
		p.add.Enable()
	} else {
		p.add.Disable()
	}
}

// canAdd reports whether an assistant is chosen and there is a file to
// put it into.
func (p *assistantPicker) canAdd() bool {
	return p.selected >= 0 && p.selected < len(p.shown) && p.ap.u.settings.file != nil
}

// openSelected walks through the chosen assistant over the dialog, which
// stays open under it; its summary puts the assistant into the file.
func (p *assistantPicker) openSelected() {
	if !p.canAdd() || p.ap.scan == nil {
		return
	}
	previewAssistant(p.ap.u, p.ap.scan.dir, p.shown[p.selected])
}
