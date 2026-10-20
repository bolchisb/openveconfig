package main

// The grid code choice. The list of grid codes is long and its entries are
// long, so a drop-down list would cover the window. The row shows the
// chosen code on a button instead; the button opens a dialog of fixed size
// with a search field and a scrolling list.

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// codeItem is one grid code of the picker.
type codeItem struct {
	label           string // the whole text, as the change list and report show it
	title, subtitle string // the number and region; the standard
	locked          bool   // needs the installer password
}

// codePicker chooses one of its items. Its methods follow widget.Select's,
// which it replaces.
type codePicker struct {
	button    *widget.Button
	Options   []string // the labels of the items
	items     []codeItem
	selected  int
	onChanged func(i int)
	// win is the window the dialog opens on; title is the dialog's title.
	win   fyne.Window
	title string
	// caption shows the subtitle of the chosen item.
	caption func(text string)
}

func newCodePicker(win fyne.Window, title string, onChanged func(i int)) *codePicker {
	p := &codePicker{selected: -1, onChanged: onChanged, win: win, title: title, caption: func(string) {}}
	p.button = widget.NewButtonWithIcon("", theme.MenuDropDownIcon(), p.open)
	p.button.IconPlacement = widget.ButtonIconTrailingText
	p.button.Alignment = widget.ButtonAlignLeading
	p.show()
	return p
}

// SetItems replaces the items; the choice is cleared.
func (p *codePicker) SetItems(items []codeItem) {
	p.items, p.Options, p.selected = items, nil, -1
	for _, it := range items {
		p.Options = append(p.Options, it.label)
	}
	p.show()
}

// SelectedIndex returns the index of the chosen item, -1 for none.
func (p *codePicker) SelectedIndex() int { return p.selected }

// SetSelectedIndex chooses item i and, when the choice changes, calls
// onChanged, as widget.Select does.
func (p *codePicker) SetSelectedIndex(i int) {
	if i < -1 || i >= len(p.items) || i == p.selected {
		return
	}
	p.selected = i
	p.show()
	if p.onChanged != nil {
		p.onChanged(i)
	}
}

func (p *codePicker) Enable()        { p.button.Enable() }
func (p *codePicker) Disable()       { p.button.Disable() }
func (p *codePicker) Disabled() bool { return p.button.Disabled() }

// show puts the chosen item on the button and its subtitle in the caption.
func (p *codePicker) show() {
	text, sub := tr("Choose…"), ""
	if p.selected >= 0 {
		text, sub = p.items[p.selected].title, p.items[p.selected].subtitle
	}
	p.button.SetText(text)
	p.caption(sub)
}

// open shows the dialog with the items, filtered by the search field.
func (p *codePicker) open() {
	var shown []int // indexes of the items that match the search
	filter := func(q string) {
		q = strings.ToLower(strings.TrimSpace(q))
		shown = shown[:0]
		for i, it := range p.items {
			if q == "" || strings.Contains(strings.ToLower(it.label), q) {
				shown = append(shown, i)
			}
		}
	}
	filter("")
	var dlg dialog.Dialog
	list := widget.NewList(
		func() int { return len(shown) },
		func() fyne.CanvasObject {
			title := widget.NewLabel("")
			title.TextStyle.Bold = true
			title.Truncation = fyne.TextTruncateEllipsis
			sub := widget.NewLabel("")
			sub.SizeName = theme.SizeNameCaptionText
			sub.Truncation = fyne.TextTruncateEllipsis
			lock := widget.NewIcon(theme.AccountIcon())
			current := widget.NewIcon(theme.ConfirmIcon())
			return container.NewBorder(nil, nil, current, lock, container.New(&tightRows{}, title, sub))
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			it := p.items[shown[id]]
			row := o.(*fyne.Container)
			texts := row.Objects[0].(*fyne.Container).Objects
			texts[0].(*widget.Label).SetText(it.title)
			texts[1].(*widget.Label).SetText(it.subtitle)
			// Hidden, the mark keeps its place, so the texts stay aligned.
			mark := row.Objects[1].(*widget.Icon)
			if shown[id] == p.selected {
				mark.SetResource(theme.ConfirmIcon())
			} else {
				mark.SetResource(nil)
			}
			setShown(row.Objects[2], it.locked)
		})
	list.OnSelected = func(id widget.ListItemID) {
		dlg.Hide()
		p.SetSelectedIndex(shown[id])
	}
	search := widget.NewEntry()
	search.SetPlaceHolder(tr("Search by number, country or standard"))
	search.OnChanged = func(q string) {
		filter(q)
		list.UnselectAll()
		list.Refresh()
	}
	legend := container.NewHBox(widget.NewIcon(theme.AccountIcon()), widget.NewLabel(tr("needs password")))
	content := container.NewBorder(search, legend, nil, nil, list)
	dlg = dialog.NewCustom(p.title, tr("Cancel"), content, p.win)
	dlg.Resize(fyne.NewSize(560, 520))
	dlg.Show()
	if p.selected >= 0 {
		list.ScrollTo(p.selected)
	}
	p.win.Canvas().Focus(search)
}

// tightRows stacks its objects with no space between them, for a title
// and the line under it.
type tightRows struct{}

func (tightRows) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var s fyne.Size
	for _, o := range objs {
		m := o.MinSize()
		s.Width = max(s.Width, m.Width)
		s.Height += m.Height - 2*theme.Padding()
	}
	return s.AddWidthHeight(0, 2*theme.Padding())
}

func (tightRows) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for _, o := range objs {
		h := o.MinSize().Height
		o.Resize(fyne.NewSize(size.Width, h))
		o.Move(fyne.NewPos(0, y))
		y += h - 2*theme.Padding()
	}
}
