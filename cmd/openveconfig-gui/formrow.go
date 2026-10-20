package main

// The setting row and the page layout around it, shared by the settings
// pages and the grid page: a form of cards whose rows line up in one label
// column per page, at most formMaxWidth wide, and the numeric entry that
// edits a value in place.

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	// formMaxWidth is the widest a form gets, so that its controls stay
	// near their labels in a wide window.
	formMaxWidth = 800
	// labelMinWidth and labelMaxWidth bound the label column; a longer
	// label wraps.
	labelMinWidth, labelMaxWidth = 140, 280
	// controlWidth is the width of the control column; the markers and
	// the revert button start after it.
	controlWidth = 240
	// numberWidth is the width of a numeric entry; selectMaxWidth the most
	// a choice list gets.
	numberWidth    = 120
	selectMaxWidth = 360
	// dotSize is the diameter of the changed dot; cardGap the space
	// between cards.
	dotSize = 8
	cardGap = 12
	// sideBySideWidth is the page width from which a page's visual stands
	// to the right of the form instead of above it (a window of about
	// 1150 px); visualMinWidth and visualMaxWidth bound its column.
	sideBySideWidth                = 920
	visualMinWidth, visualMaxWidth = 300, 480
)

// formColumns is the label column width shared by the rows of one page, or
// of one column of a two-column card, and the width kept for the control
// (controlWidth when 0).
type formColumns struct{ label, control float32 }

// controlWidth returns the width kept for the control.
func (c *formColumns) controlWidth() float32 {
	if c.control > 0 {
		return c.control
	}
	return controlWidth
}

// fit sets the label column to the widest of labels, within labelMinWidth
// and labelMaxWidth. A label is measured bold, as it is shown when changed.
func (c *formColumns) fit(labels []string) {
	th := theme.Current()
	size, pad := th.Size(theme.SizeNameText), th.Size(theme.SizeNameInnerPadding)
	w := float32(labelMinWidth)
	for _, l := range labels {
		w = max(w, fyne.MeasureText(l, size, fyne.TextStyle{Bold: true}).Width+2*pad)
	}
	c.label = min(w, labelMaxWidth)
}

// fitWithin is fit with the label column at most maxWidth wide.
func (c *formColumns) fitWithin(labels []string, maxWidth float32) {
	c.fit(labels)
	c.label = min(c.label, maxWidth)
}

// formRow is one row of a form: a dot shown while the value differs from
// the one read, the label with an optional muted help line under it, the
// control with its unit, a muted caption (the allowed range) and an error
// line under the control, and at the end the reset and password markers
// and a revert button.
//
// A header row is the head of a card whose control (a check box) switches
// the card's other rows on and off: the dot, the control, the card title
// and the markers on one line.
type formRow struct {
	cols    *formColumns
	header  bool
	dot     *themeRect
	label   *widget.Label
	help    *widget.RichText
	control fyne.CanvasObject
	main    *fyne.Container // the control and the unit
	unit    *widget.Label
	caption *widget.RichText
	errText *widget.RichText
	reset   *fyne.Container
	passwd  *fyne.Container
	revert  *widget.Button
	trail   *fyne.Container // markers and revert
	root    *fyne.Container
	// changed and invalid make the revert button show: the value differs
	// from the one read, or an entry holds text that was refused.
	changed, invalid bool
}

// newFormRow returns a row labelled label, with help under it unless it is
// "", showing control at width (its own width when 0) followed by unit.
func newFormRow(cols *formColumns, label, help string, control fyne.CanvasObject, width float32, unit string) *formRow {
	r := newRowParts(cols, label, control, unit)
	if width > 0 {
		control = container.New(fixedWidth(width), control)
	}
	r.main = container.NewHBox(control, r.unit)
	r.label.Wrapping = fyne.TextWrapWord
	r.help = captionText(help, theme.ColorNamePlaceHolder)
	r.help.Wrapping = fyne.TextWrapWord
	r.help.Hidden = help == ""
	r.root = container.New(&rowLayout{r: r}, r.dot, r.label, r.help, r.main, r.caption, r.errText, r.trail)
	return r
}

// newFormHeader returns the header row of a card titled title, whose
// control, at the right, is check. Its caption shows under the title.
func newFormHeader(cols *formColumns, title string, check fyne.CanvasObject) *formRow {
	r := newRowParts(cols, title, check, "")
	r.header = true
	r.label.TextStyle.Bold = true
	r.label.SizeName = theme.SizeNameSubHeadingText
	r.root = container.NewVBox(
		container.NewHBox(container.NewCenter(r.dot), r.label, layout.NewSpacer(), container.NewCenter(r.trail), container.NewCenter(check)),
		r.caption)
	return r
}

func newRowParts(cols *formColumns, label string, control fyne.CanvasObject, unit string) *formRow {
	r := &formRow{cols: cols, control: control, label: widget.NewLabel(label), unit: widget.NewLabel(unit)}
	r.dot = newThemeRect(colorNameChanged, "", dotSize/2, fyne.NewSquareSize(dotSize))
	r.dot.Hide()
	r.unit.Hidden = unit == ""
	r.caption = captionText("", theme.ColorNamePlaceHolder)
	r.caption.Hide()
	r.errText = captionText("", theme.ColorNameError)
	r.errText.Wrapping = fyne.TextWrapWord
	r.errText.Hide()
	r.reset = rowMarker(theme.ViewRefreshIcon(), tr("needs reset"))
	r.passwd = rowMarker(theme.AccountIcon(), tr("needs password"))
	r.reset.Hide()
	r.passwd.Hide()
	r.revert = widget.NewButtonWithIcon("", theme.ContentUndoIcon(), nil)
	r.revert.Importance = widget.LowImportance
	r.revert.Hide()
	r.trail = container.NewHBox(r.reset, r.passwd, r.revert)
	return r
}

// rowMarker is a small muted icon with a short text.
func rowMarker(icon fyne.Resource, text string) *fyne.Container {
	return container.NewHBox(widget.NewIcon(theme.NewColoredResource(icon, theme.ColorNamePlaceHolder)), captionText(text, theme.ColorNamePlaceHolder))
}

// captionText returns text in the caption size and the given colour.
func captionText(text string, colour fyne.ThemeColorName) *widget.RichText {
	return widget.NewRichText(&widget.TextSegment{Text: text,
		Style: widget.RichTextStyle{ColorName: colour, SizeName: theme.SizeNameCaptionText}})
}

// setCaptionText changes the text of t, made by captionText, and hides t
// while it is empty.
func setCaptionText(t *widget.RichText, text string) {
	seg := t.Segments[0].(*widget.TextSegment)
	if seg.Text == text && t.Hidden == (text == "") {
		return
	}
	seg.Text = text
	t.Hidden = text == ""
	t.Refresh()
}

// setChanged shows whether the value differs from the one read: the dot,
// the label in bold and the revert button.
func (r *formRow) setChanged(changed bool) {
	r.changed = changed
	if r.dot.Visible() != changed {
		r.dot.Hidden = !changed
		r.dot.Refresh()
	}
	if !r.header && r.label.TextStyle.Bold != changed {
		r.label.TextStyle.Bold = changed
		r.label.Refresh()
	}
	r.showRevert()
}

func (r *formRow) showRevert() {
	if v := r.changed || r.invalid; v != r.revert.Visible() {
		r.revert.Hidden = !v
		r.trail.Refresh()
	}
}

// setMarkers shows the reset and password markers.
func (r *formRow) setMarkers(reset, password bool) {
	if reset != r.reset.Visible() || password != r.passwd.Visible() {
		r.reset.Hidden, r.passwd.Hidden = !reset, !password
		r.trail.Refresh()
	}
}

// setCaption sets the muted line under the control; "" hides it.
func (r *formRow) setCaption(text string) { setCaptionText(r.caption, text) }

// setError shows msg in the error colour under the control; "" hides it.
func (r *formRow) setError(msg string) {
	if r.invalid == (msg != "") && r.errText.Segments[0].(*widget.TextSegment).Text == msg {
		return
	}
	setCaptionText(r.errText, msg)
	r.invalid = msg != ""
	r.showRevert()
	r.root.Refresh()
}

// setUnit sets the unit shown after the control.
func (r *formRow) setUnit(unit string) {
	if r.unit.Text != unit && r.main != nil {
		r.unit.SetText(unit)
		r.unit.Hidden = unit == ""
		r.main.Refresh()
	}
}

// setEnabled enables or disables the control; the label of a disabled row
// is muted.
func (r *formRow) setEnabled(on bool) {
	if c, ok := r.control.(fyne.Disableable); ok && c.Disabled() == on {
		if on {
			c.Enable()
		} else {
			c.Disable()
		}
	}
	imp := widget.MediumImportance
	if !on {
		imp = widget.LowImportance
	}
	if r.label.Importance != imp {
		r.label.Importance = imp
		r.label.Refresh()
	}
}

// rowLayout lays out a formRow: dot | label and help | control, unit,
// caption and error | markers and revert.
type rowLayout struct {
	r *formRow
	// width is the width of the last layout, for the wrapping of the
	// error line in MinSize.
	width float32
}

// geometry returns the x of the label and of the control, the height of
// the control, the height of its first line (of radio buttons one under the
// other, the first) and the y of the label, centred on that line.
func (l *rowLayout) geometry() (labelX, controlX, mainH, firstH, labelY float32) {
	th := theme.Current()
	pad := th.Size(theme.SizeNamePadding)
	labelX = dotSize + pad
	controlX = labelX + l.r.cols.label + 2*pad
	mainH = l.r.main.MinSize().Height
	line := fyne.MeasureText(lineSample, th.Size(theme.SizeNameText), fyne.TextStyle{}).Height + 2*th.Size(theme.SizeNameInnerPadding)
	firstH = min(mainH, line+pad)
	labelY = max(0, (firstH-line)/2)
	return
}

// lineSample is measured for the height of a line of text; it is not shown.
const lineSample = "M"

// fitHeight resizes o to width and returns its height there: wrapped text
// knows its height only for a given width.
func fitHeight(o fyne.CanvasObject, width float32) float32 {
	if !o.Visible() {
		return 0
	}
	if o.Size().Width != width {
		o.Resize(fyne.NewSize(width, o.MinSize().Height))
	}
	return o.MinSize().Height
}

func (l *rowLayout) errWidth(controlX float32) float32 {
	w := l.width
	if w <= 0 {
		w = formMaxWidth
	}
	return max(w-controlX, l.r.cols.controlWidth())
}

func (l *rowLayout) Layout(_ []fyne.CanvasObject, size fyne.Size) {
	r := l.r
	l.width = size.Width
	pad := theme.Current().Size(theme.SizeNamePadding)
	tight := l.tight()
	labelX, controlX, mainH, firstH, labelY := l.geometry()

	r.dot.Resize(fyne.NewSquareSize(dotSize))
	r.dot.Move(fyne.NewPos(0, (firstH-dotSize)/2))
	lh := fitHeight(r.label, r.cols.label)
	r.label.Move(fyne.NewPos(labelX, labelY))
	r.label.Resize(fyne.NewSize(r.cols.label, lh))
	hh := fitHeight(r.help, r.cols.label)
	r.help.Move(fyne.NewPos(labelX, labelY+lh-tight))
	r.help.Resize(fyne.NewSize(r.cols.label, hh))

	mainW := r.main.MinSize().Width
	r.main.Move(fyne.NewPos(controlX, 0))
	r.main.Resize(fyne.NewSize(mainW, mainH))
	y := mainH - pad
	if r.caption.Visible() {
		ch := r.caption.MinSize().Height
		r.caption.Move(fyne.NewPos(controlX, y))
		r.caption.Resize(fyne.NewSize(size.Width-controlX, ch))
		y += ch - tight
	}
	ew := l.errWidth(controlX)
	eh := fitHeight(r.errText, ew)
	r.errText.Move(fyne.NewPos(controlX, y))
	r.errText.Resize(fyne.NewSize(ew, eh))

	ts := r.trail.MinSize()
	r.trail.Move(fyne.NewPos(controlX+max(r.cols.controlWidth(), mainW)+pad, (firstH-ts.Height)/2))
	r.trail.Resize(ts)
}

// tight is how far a line of text under another moves up into the
// other's padding: the inner padding of both, less a small gap.
func (l *rowLayout) tight() float32 {
	th := theme.Current()
	return 2*th.Size(theme.SizeNameInnerPadding) - th.Size(theme.SizeNamePadding)
}

func (l *rowLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	r := l.r
	pad := theme.Current().Size(theme.SizeNamePadding)
	tight := l.tight()
	_, controlX, mainH, _, labelY := l.geometry()
	left := labelY + fitHeight(r.label, r.cols.label)
	if r.help.Visible() {
		left += fitHeight(r.help, r.cols.label) - tight
	}
	// As in Layout: the caption and the error line under the control, each
	// moved up by tight, and the last one's bottom padding counted.
	right, y := mainH, mainH-pad
	if r.caption.Visible() {
		h := r.caption.MinSize().Height
		right, y = y+h, y+h-tight
	}
	if r.errText.Visible() {
		right = y + fitHeight(r.errText, l.errWidth(controlX))
	}
	w := controlX + max(r.cols.controlWidth(), r.main.MinSize().Width) + pad + r.trail.MinSize().Width
	return fyne.NewSize(w, max(left, right))
}

// fixedWidth lays its objects out at its width and their own height.
type fixedWidth float32

func (w fixedWidth) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(fyne.NewSize(float32(w), size.Height))
	}
}

func (w fixedWidth) MinSize(objs []fyne.CanvasObject) fyne.Size {
	s := fyne.NewSize(float32(w), 0)
	for _, o := range objs {
		s.Height = max(s.Height, o.MinSize().Height)
	}
	return s
}

// newFormCard returns a card with a title (or a header row) above rows.
func newFormCard(head fyne.CanvasObject, rows ...fyne.CanvasObject) fyne.CanvasObject {
	if head != nil {
		rows = append([]fyne.CanvasObject{head}, rows...)
	}
	body := container.NewVBox(rows...)
	return container.NewStack(newCardRect(fyne.Size{}),
		container.New(layout.NewCustomPaddedLayout(10, 14, 16, 16), body))
}

// cardTitle returns the title of a card without a header control.
func cardTitle(title string) fyne.CanvasObject {
	l := widget.NewLabel(title)
	l.TextStyle.Bold = true
	l.SizeName = theme.SizeNameSubHeadingText
	return l
}

// newForm returns cards in a column at most formMaxWidth wide, at the top
// left of a vertical scroll area.
func newForm(cards ...fyne.CanvasObject) fyne.CanvasObject {
	return container.New(maxWidth(formMaxWidth), container.New(layout.NewCustomPaddedVBoxLayout(cardGap), cards...))
}

// maxWidth lays its objects out at the left, at most its width wide unless
// they need more.
type maxWidth float32

func (m maxWidth) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(fyne.NewSize(min(size.Width, max(float32(m), o.MinSize().Width)), size.Height))
	}
}

func (m maxWidth) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var s fyne.Size
	for _, o := range objs {
		s = s.Max(o.MinSize())
	}
	return s
}

// Columns of a two-column card: the label column at most pairLabelWidth wide
// and pairControlWidth kept for the control, so that two fit side by side
// in a form.
const pairLabelWidth, pairControlWidth = 170, 150

// newTwoColumns returns left and right side by side when they fit the
// width, else right under left.
func newTwoColumns(left, right fyne.CanvasObject) fyne.CanvasObject {
	return container.New(&twoColumns{}, left, right)
}

// twoColumns lays out two columns side by side when both fit, else one
// under the other.
type twoColumns struct {
	// width is the width of the last layout, which decides in MinSize
	// whether the columns are side by side.
	width float32
}

func (l *twoColumns) gap() float32 { return 2 * theme.Current().Size(theme.SizeNamePadding) }

func (l *twoColumns) sideBySide(objs []fyne.CanvasObject, width float32) bool {
	return width >= objs[0].MinSize().Width+l.gap()+objs[1].MinSize().Width
}

func (l *twoColumns) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	left, right := objs[0], objs[1]
	if l.width != size.Width {
		l.width = size.Width
		defer func() {
			for _, o := range objs {
				o.Refresh() // the scroll area asks MinSize again
			}
		}()
	}
	if l.sideBySide(objs, size.Width) {
		w := (size.Width - l.gap()) / 2
		left.Move(fyne.NewPos(0, 0))
		left.Resize(fyne.NewSize(w, left.MinSize().Height))
		right.Move(fyne.NewPos(w+l.gap(), 0))
		right.Resize(fyne.NewSize(w, right.MinSize().Height))
		return
	}
	lh := left.MinSize().Height
	left.Move(fyne.NewPos(0, 0))
	left.Resize(fyne.NewSize(size.Width, lh))
	right.Move(fyne.NewPos(0, lh))
	right.Resize(fyne.NewSize(size.Width, right.MinSize().Height))
}

func (l *twoColumns) MinSize(objs []fyne.CanvasObject) fyne.Size {
	a, b := objs[0].MinSize(), objs[1].MinSize()
	if l.sideBySide(objs, l.width) {
		return fyne.NewSize(max(a.Width, b.Width), max(a.Height, b.Height))
	}
	return fyne.NewSize(max(a.Width, b.Width), a.Height+b.Height)
}

// withVisual returns content with visual (when not nil) to its right on a
// page at least sideBySideWidth wide, else above it.
func withVisual(content, visual fyne.CanvasObject) fyne.CanvasObject {
	if visual == nil {
		return content
	}
	return container.New(&visualLayout{}, content, visual)
}

// visualLayout lays out a page's content and its visual at their own
// heights; the page scrolls them together.
type visualLayout struct {
	// width is the width of the last layout, which decides in MinSize
	// whether the two are side by side.
	width float32
}

func (l *visualLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	content, visual := objs[0], objs[1]
	gap := 2 * theme.Current().Size(theme.SizeNamePadding)
	if l.width != size.Width {
		l.width = size.Width
		defer func() {
			for _, o := range objs {
				o.Refresh() // the page's scroll area asks MinSize again
			}
		}()
	}
	if !visual.Visible() {
		content.Move(fyne.NewPos(0, 0))
		content.Resize(fyne.NewSize(size.Width, content.MinSize().Height))
		return
	}
	if size.Width >= sideBySideWidth {
		vw := min(max(visualMinWidth, size.Width-formMaxWidth-gap), visualMaxWidth)
		content.Move(fyne.NewPos(0, 0))
		content.Resize(fyne.NewSize(size.Width-vw-gap, content.MinSize().Height))
		visual.Move(fyne.NewPos(size.Width-vw, 0))
		visual.Resize(fyne.NewSize(vw, visual.MinSize().Height))
		return
	}
	vh := visual.MinSize().Height
	visual.Move(fyne.NewPos(0, 0))
	visual.Resize(fyne.NewSize(min(size.Width, formMaxWidth), vh))
	content.Move(fyne.NewPos(0, vh+gap))
	content.Resize(fyne.NewSize(size.Width, content.MinSize().Height))
}

func (l *visualLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	c, v := objs[0].MinSize(), objs[1].MinSize()
	if !objs[1].Visible() {
		return c
	}
	w := max(c.Width, v.Width) // side by side only when there is room for both
	if l.width >= sideBySideWidth {
		return fyne.NewSize(w, max(c.Height, v.Height))
	}
	gap := 2 * theme.Current().Size(theme.SizeNamePadding)
	return fyne.NewSize(w, c.Height+gap+v.Height)
}

// numberEntry is a short entry for a number. It hands its text to commit
// on Enter and when it loses focus, and calls restore on Escape.
type numberEntry struct {
	widget.Entry
	commit  func(text string)
	restore func()
}

func newNumberEntry() *numberEntry {
	e := &numberEntry{}
	e.ExtendBaseWidget(e)
	e.OnSubmitted = func(text string) { e.commitText(text) }
	return e
}

func (e *numberEntry) commitText(text string) {
	if e.commit != nil && !e.Disabled() {
		e.commit(text)
	}
}

// FocusLost commits the text.
func (e *numberEntry) FocusLost() {
	e.Entry.FocusLost()
	e.commitText(e.Text)
}

// TypedKey restores the value on Escape.
func (e *numberEntry) TypedKey(k *fyne.KeyEvent) {
	if k.Name == fyne.KeyEscape && e.restore != nil {
		e.restore()
		return
	}
	e.Entry.TypedKey(k)
}

// bindNumber makes the entry of r edit a number: current returns the text
// of the value in the model, set applies a text and returns the message for
// one it refuses. A refused text stays in the entry with the message under
// it until it is corrected, Escape restores the value, or the value changes.
func (r *formRow) bindNumber(e *numberEntry, current func() string, set func(text string) error) (refresh func()) {
	var refusedAt string // the value shown when a text was refused
	show := func() {
		refusedAt = ""
		r.setError("")
		if t := current(); e.Text != t {
			e.SetText(t)
		}
	}
	e.commit = func(text string) {
		if text == current() {
			show()
			return
		}
		if err := set(text); err != nil {
			refusedAt = current()
			r.setError(err.Error())
		}
	}
	e.restore = show
	return func() {
		if !r.invalid || current() != refusedAt {
			show()
		}
	}
}

// themeRect is a rectangle in theme colours: a fill and, unless stroke is
// "", a 1 px outline. radius -1 is the card radius of the theme.
type themeRect struct {
	widget.BaseWidget
	fill, stroke fyne.ThemeColorName
	radius       float32
	min          fyne.Size
	shadow       bool // a soft shadow under it, as under a card
}

func newThemeRect(fill, stroke fyne.ThemeColorName, radius float32, min fyne.Size) *themeRect {
	r := &themeRect{fill: fill, stroke: stroke, radius: radius, min: min}
	r.ExtendBaseWidget(r)
	return r
}

// newCardRect returns the background of a card: the surface, a 1 px
// outline and a soft shadow.
func newCardRect(min fyne.Size) *themeRect {
	r := newThemeRect(colorNameSurface, theme.ColorNameSeparator, -1, min)
	r.shadow = true
	return r
}

func (r *themeRect) CreateRenderer() fyne.WidgetRenderer {
	rr := &themeRectRenderer{r: r, rect: canvas.NewRectangle(color.Transparent)}
	rr.Refresh()
	return rr
}

type themeRectRenderer struct {
	r    *themeRect
	rect *canvas.Rectangle
}

func (rr *themeRectRenderer) Layout(size fyne.Size) { rr.rect.Resize(size) }

func (rr *themeRectRenderer) MinSize() fyne.Size { return rr.r.min }

func (rr *themeRectRenderer) Refresh() {
	th := rr.r.Theme()
	v := fyne.CurrentApp().Settings().ThemeVariant()
	rr.rect.FillColor = th.Color(rr.r.fill, v)
	rr.rect.StrokeWidth = 0
	if rr.r.stroke != "" {
		rr.rect.StrokeColor = th.Color(rr.r.stroke, v)
		rr.rect.StrokeWidth = 1
	}
	rr.rect.CornerRadius = rr.r.radius
	if rr.r.radius < 0 {
		rr.rect.CornerRadius = th.Size(theme.SizeNameCardRadius)
	}
	rr.rect.Shadow = canvas.Shadow{}
	if rr.r.shadow {
		rr.rect.Shadow = canvas.Shadow{Color: th.Color(colorNameCardShadow, v), BlurRadius: 10,
			Offset: fyne.NewPos(0, 2), Variant: canvas.DropShadow}
	}
	rr.rect.Refresh()
}

func (rr *themeRectRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{rr.rect} }

func (rr *themeRectRenderer) Destroy() {}
