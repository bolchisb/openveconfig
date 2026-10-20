package main

// The main window around the pages: the header, the sidebar and the changes
// bar, and the small widgets they are drawn with. The Overview page is in
// overview.go.

import (
	"image/color"
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// sidebarWidth is the width of the sidebar.
const sidebarWidth = 200

// page is a page of the main window, listed in the sidebar.
type page struct {
	title, desc string
	icon        fyne.Resource
	// tab is the settings tab whose unsent changes the sidebar counts in the
	// page's badge (tabChanges); vebus.TabNone for a page without settings.
	tab vebus.Tab
	// empty reports that the loaded model has none of the page's settings;
	// nil for a page that always has something to show.
	empty func() bool
	view  fyne.CanvasObject // title, description and content
	// scroll scrolls the whole page; its content has no scroll of its own.
	scroll *container.Scroll
}

// newPage returns a page that shows content under its title and a one-line
// description, all in one scroll area, so content must not scroll itself.
// Add it to ui.pages to list it in the sidebar.
func newPage(title, desc string, icon fyne.Resource, tab vebus.Tab, content fyne.CanvasObject) *page {
	head := widget.NewRichText(
		&widget.TextSegment{Text: title, Style: widget.RichTextStyle{SizeName: theme.SizeNameHeadingText, TextStyle: fyne.TextStyle{Bold: true}}},
		&widget.TextSegment{Text: desc, Style: widget.RichTextStyle{ColorName: theme.ColorNamePlaceHolder}})
	head.Wrapping = fyne.TextWrapWord
	scroll := container.NewVScroll(container.New(layout.NewCustomPaddedLayout(0, 12, 0, 12), container.NewVBox(head, content)))
	view := container.New(layout.NewCustomPaddedLayout(8, 0, 12, 0), scroll)
	return &page{title: title, desc: desc, icon: icon, tab: tab, view: view, scroll: scroll}
}

// unavailable reports whether the loaded model has nothing to show on p.
func (p *page) unavailable(u *ui) bool { return p.empty != nil && u.settings.dev != nil && p.empty() }

// changes returns the number of unsent changes on p.
func (p *page) changes(u *ui) int {
	if d := u.settings.dev; d != nil && p.tab != vebus.TabNone {
		return tabChanges(d, p.tab)
	}
	return 0
}

// showPage shows p and selects it in the sidebar.
func (u *ui) showPage(p *page) {
	u.current = p
	u.pageArea.Objects = []fyne.CanvasObject{p.view}
	u.pageArea.Refresh()
	if i := slices.Index(u.pages, p); i >= 0 {
		u.sidebar.Select(i)
	}
}

// newSidebar returns the list of u.pages, each with a badge that counts its
// unsent changes, and the tools under it.
func newSidebar(u *ui) fyne.CanvasObject {
	u.sidebar = widget.NewList(
		func() int { return len(u.pages) },
		func() fyne.CanvasObject {
			icon, badge := widget.NewIcon(nil), newPill("", colorNameChanged, true)
			// Objects: the label, the icon and the badge.
			return container.New(layout.NewBorderLayout(nil, nil, icon, badge), widget.NewLabel(""), icon, badge)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			p, row := u.pages[i], o.(*fyne.Container)
			label, icon, badge := row.Objects[0].(*widget.Label), row.Objects[1].(*widget.Icon), row.Objects[2].(*pill)
			off := p.unavailable(u)
			label.Text = p.title
			label.Importance = widget.MediumImportance
			res := p.icon
			if off {
				label.Importance = widget.LowImportance
				res = theme.NewDisabledResource(res)
			}
			label.Refresh()
			icon.SetResource(res)
			if n := p.changes(u); n > 0 {
				badge.setText(formatNumber(float64(n), 0))
				badge.Show()
			} else {
				badge.Hide()
			}
			row.Refresh()
		})
	u.sidebar.HideSeparators = true
	u.sidebar.OnSelected = func(i widget.ListItemID) { u.showPage(u.pages[i]) }

	tool := func(label string, icon fyne.Resource, f func()) *widget.Button {
		b := widget.NewButtonWithIcon(label, icon, f)
		b.Importance = widget.LowImportance
		b.Alignment = widget.ButtonAlignLeading
		return b
	}
	tools := container.NewVBox(widget.NewSeparator(), mutedText(tr("Tools")),
		tool(tr("Remote panel"), theme.ComputerIcon(), func() { showRemotePanel(u) }),
		tool(tr("VE.Bus monitor"), theme.VisibilityIcon(), func() { showVEBusMonitor(u) }))
	return container.NewStack(newColourRect(colorNameSurface, fyne.NewSize(sidebarWidth, 0), ""),
		container.NewPadded(container.NewBorder(nil, tools, nil, nil, u.sidebar)))
}

// header is the row at the top of the main window: what is being edited,
// where it comes from, and the connect button.
type header struct {
	root     fyne.CanvasObject
	identity *widget.Label
	conn     *pill // the session: the port and interface, or the demo
	file     *pill // the open settings file
	connect  *widget.Button
}

func newHeader(u *ui) *header {
	h := &header{identity: widget.NewLabel(""), conn: newPill("", theme.ColorNameSuccess, false),
		file: newPill("", theme.ColorNamePrimary, false)}
	h.identity.TextStyle.Bold = true
	h.identity.Truncation = fyne.TextTruncateEllipsis
	h.connect = widget.NewButton("", func() {
		if u.sess != nil {
			u.setSession(nil)
		} else {
			u.conn.show()
		}
	})
	name := widget.NewRichText(&widget.TextSegment{Text: appName,
		Style: widget.RichTextStyle{ColorName: theme.ColorNamePlaceHolder, TextStyle: fyne.TextStyle{Bold: true}}})
	right := container.NewHBox(h.conn, h.file, h.connect)
	row := container.NewBorder(nil, nil, name, right, h.identity)
	h.root = container.NewVBox(container.NewStack(newColourRect(colorNameSurface, fyne.Size{}, ""),
		container.New(layout.NewCustomPaddedLayout(4, 4, 8, 8), row)), widget.NewSeparator())
	return h
}

// update shows the session and the loaded model.
func (h *header) update(u *ui) {
	s, m := u.sess, u.settings
	switch {
	case m.dev == nil:
		h.identity.SetText("")
	case m.file != nil:
		h.identity.SetText(tr("Unit {{.Serial}} · firmware {{.Firmware}}",
			args{"Serial": m.file.unit.Serial, "Firmware": strconv.FormatUint(uint64(m.dev.Firmware), 10)}))
	default:
		h.identity.SetText(tr("Device at address {{.Address}} · firmware {{.Firmware}}",
			args{"Address": m.dev.Address, "Firmware": strconv.FormatUint(uint64(m.dev.Firmware), 10)}))
	}
	switch {
	case s == nil:
		h.conn.Hide()
		h.connect.SetText(tr("Connect…"))
	case s.demo:
		h.conn.set(tr("Demo · simulated devices"), theme.ColorNameWarning, true)
		h.conn.Show()
		h.connect.SetText(tr("Disconnect"))
	default:
		h.conn.set(tr("{{.Port}} · {{.Interface}} · connected", args{"Port": s.portName, "Interface": s.interfaceType()}),
			theme.ColorNameSuccess, false)
		h.conn.Show()
		h.connect.SetText(tr("Disconnect"))
	}
	if m.file != nil {
		h.file.setText(tr("File: {{.File}}", args{"File": m.file.name}))
		h.file.Show()
	} else {
		h.file.Hide()
	}
}

// changesBar is the bar under the pages while the model has unsent
// changes: what they are and what can be done with them.
type changesBar struct {
	root    fyne.CanvasObject
	summary *widget.Label
	reason  *widget.RichText // why the primary action cannot be done now
	review  *widget.Button
	discard *widget.Button
	primary *widget.Button
}

func newChangesBar(u *ui) *changesBar {
	b := &changesBar{summary: widget.NewLabel(""), reason: mutedText("")}
	b.summary.TextStyle.Bold = true
	b.review = widget.NewButtonWithIcon(tr("Review changes"), theme.ListIcon(), func() { reviewChanges(u) })
	b.discard = widget.NewButtonWithIcon(tr("Discard"), theme.ContentUndoIcon(), func() { discardChanges(u) })
	b.primary = widget.NewButton("", nil)
	b.primary.Importance = widget.HighImportance
	row := container.NewBorder(nil, nil, container.NewHBox(newPill("", colorNameChanged, true), b.summary),
		container.NewHBox(b.reason, b.review, b.discard, b.primary))
	b.root = container.NewVBox(widget.NewSeparator(), container.NewStack(newColourRect(colorNameSurface, fyne.Size{}, ""),
		container.New(layout.NewCustomPaddedLayout(4, 4, 8, 8), row)))
	b.root.Hide()
	return b
}

// update shows the unsent changes of the model, or hides the bar.
func (b *changesBar) update(u *ui) {
	d := u.settings.dev
	var c changeCounts
	if d != nil {
		c = countChanges(d)
	}
	if c.total() == 0 && c.invalid == 0 {
		b.root.Hide()
		return
	}
	parts := []string{trn("{{.Count}} unsaved change", "{{.Count}} unsaved changes", c.total())}
	if c.reset > 0 {
		parts = append(parts, trn("{{.Count}} needs a device reset", "{{.Count}} need a device reset", c.reset))
	}
	if c.invalid > 0 {
		parts = append(parts, trn("{{.Count}} invalid value", "{{.Count}} invalid values", c.invalid))
	}
	b.summary.SetText(strings.Join(parts, " · "))
	label, do, reason := primaryAction(u, c)
	b.primary.SetText(label)
	b.primary.OnTapped = do
	if reason == "" {
		b.primary.Enable()
	} else {
		b.primary.Disable()
	}
	setMutedText(b.reason, reason)
	b.root.Show()
}

// primaryAction returns the action the changes bar offers for the loaded
// model, whose changes are counted in c: save the file it was loaded from,
// or send it to the device it was read from. reason says why that cannot be
// done now, or is "".
func primaryAction(u *ui, c changeCounts) (label string, do func(), reason string) {
	m, s := u.settings, u.sess
	if m.file != nil {
		label, do = tr("Save file"), func() { saveSettingsFile(u) }
		if c.invalid > 0 {
			reason = tr("Correct the invalid values to save.")
		}
		return label, do, reason
	}
	label, do = tr("Send to device"), func() { sendSettings(u) }
	switch {
	case s == nil:
		reason = tr("Connect to the device these settings were read from to send them.")
	case !slices.Contains(s.devices, m.dev.Address):
		reason = tr("The device these settings were read from is not on the bus.")
	case c.invalid > 0:
		reason = tr("Correct the invalid values to send.")
	case c.gridWords > 0 || c.gridCode:
		reason = tr("Undo the grid code and grid parameter changes to send.")
	}
	return label, do, reason
}

// mutedText returns wrapped text in the muted colour of hints.
func mutedText(text string) *widget.RichText {
	t := widget.NewRichText(&widget.TextSegment{Text: text, Style: widget.RichTextStyle{ColorName: theme.ColorNamePlaceHolder}})
	t.Wrapping = fyne.TextWrapWord
	return t
}

// setMutedText changes the text of t, made by mutedText, and hides t while
// it is empty.
func setMutedText(t *widget.RichText, text string) {
	t.Segments[0].(*widget.TextSegment).Text = text
	t.Wrapping = fyne.TextWrapOff
	t.Hidden = text == ""
	t.Refresh()
}

// pill is a short text on a rounded background in a theme colour: solid,
// with the matching text colour, or a tint with text in the colour. Given
// more height than it needs, it stays at its own height, centred.
type pill struct {
	widget.BaseWidget
	text   string
	colour fyne.ThemeColorName
	solid  bool
}

func newPill(text string, colour fyne.ThemeColorName, solid bool) *pill {
	p := &pill{text: text, colour: colour, solid: solid}
	p.ExtendBaseWidget(p)
	return p
}

func (p *pill) setText(text string) {
	p.text = text
	p.Refresh()
}

func (p *pill) set(text string, colour fyne.ThemeColorName, solid bool) {
	p.text, p.colour, p.solid = text, colour, solid
	p.Refresh()
}

// pillTint is the opacity of the background of a pill that is not solid.
const pillTint = 0x40

// onColour returns the theme colour of text on a solid background of c.
func onColour(c fyne.ThemeColorName) fyne.ThemeColorName {
	switch c {
	case theme.ColorNamePrimary:
		return theme.ColorNameForegroundOnPrimary
	case theme.ColorNameError:
		return theme.ColorNameForegroundOnError
	case theme.ColorNameSuccess:
		return theme.ColorNameForegroundOnSuccess
	}
	return theme.ColorNameForegroundOnWarning
}

func (p *pill) CreateRenderer() fyne.WidgetRenderer {
	r := &pillRenderer{p: p, bg: canvas.NewRectangle(color.Transparent), text: canvas.NewText("", color.Black)}
	r.text.TextStyle.Bold = true
	r.Refresh()
	return r
}

type pillRenderer struct {
	p    *pill
	bg   *canvas.Rectangle
	text *canvas.Text
}

func (r *pillRenderer) pad() fyne.Size {
	th := r.p.Theme()
	return fyne.NewSize(th.Size(theme.SizeNameInnerPadding), th.Size(theme.SizeNamePadding)/2)
}

func (r *pillRenderer) Layout(size fyne.Size) {
	h := r.MinSize().Height
	r.bg.Move(fyne.NewPos(0, (size.Height-h)/2))
	r.bg.Resize(fyne.NewSize(size.Width, h))
	r.bg.CornerRadius = h / 2
	ts := r.text.MinSize()
	r.text.Move(fyne.NewPos((size.Width-ts.Width)/2, (size.Height-ts.Height)/2))
	r.text.Resize(ts)
}

func (r *pillRenderer) MinSize() fyne.Size {
	ts, pad := r.text.MinSize(), r.pad()
	return fyne.NewSize(max(ts.Width+2*pad.Width, ts.Height+2*pad.Height), ts.Height+2*pad.Height)
}

func (r *pillRenderer) Refresh() {
	th := r.p.Theme()
	v := fyne.CurrentApp().Settings().ThemeVariant()
	c := th.Color(r.p.colour, v)
	if r.p.solid {
		r.bg.FillColor = c
		r.text.Color = th.Color(onColour(r.p.colour), v)
	} else {
		// Text stays in the text colour; the status colour is the tint.
		r.bg.FillColor = withAlpha(color.NRGBAModel.Convert(c).(color.NRGBA), pillTint)
		r.text.Color = th.Color(theme.ColorNameForeground, v)
	}
	r.text.Text = r.p.text
	r.text.TextSize = th.Size(theme.SizeNameCaptionText)
	r.Layout(r.p.Size())
	r.bg.Refresh()
	r.text.Refresh()
}

func (r *pillRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.bg, r.text} }

func (r *pillRenderer) Destroy() {}

// colourRect fills its area with a theme colour, following theme changes.
type colourRect struct {
	widget.BaseWidget
	colour fyne.ThemeColorName
	min    fyne.Size
	radius fyne.ThemeSizeName // of the corners; "" for square corners
}

// newColourRect returns a colourRect of at least min.
func newColourRect(colour fyne.ThemeColorName, min fyne.Size, radius fyne.ThemeSizeName) *colourRect {
	r := &colourRect{colour: colour, min: min, radius: radius}
	r.ExtendBaseWidget(r)
	return r
}

func (r *colourRect) CreateRenderer() fyne.WidgetRenderer {
	rr := &colourRectRenderer{r: r, rect: canvas.NewRectangle(color.Transparent)}
	rr.Refresh()
	return rr
}

type colourRectRenderer struct {
	r    *colourRect
	rect *canvas.Rectangle
}

func (rr *colourRectRenderer) Layout(size fyne.Size) { rr.rect.Resize(size) }

func (rr *colourRectRenderer) MinSize() fyne.Size { return rr.r.min }

func (rr *colourRectRenderer) Refresh() {
	th := rr.r.Theme()
	rr.rect.FillColor = th.Color(rr.r.colour, fyne.CurrentApp().Settings().ThemeVariant())
	rr.rect.CornerRadius = 0
	if rr.r.radius != "" {
		rr.rect.CornerRadius = th.Size(rr.r.radius)
	}
	rr.rect.Refresh()
}

func (rr *colourRectRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{rr.rect} }

func (rr *colourRectRenderer) Destroy() {}
