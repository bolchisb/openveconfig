package main

// Charts shown with the settings pages: the charge profile (Charger), the
// AC input voltage window (Grid) and the battery voltage levels of the
// inverter (Inverter). They are our own schematic drawings of the loaded
// model's values, made of Fyne canvas primitives in theme colours.
//
// A chart is described by a chartSpec, computed from the model without any
// pixels (the functions the tests check), and drawn by a plot widget.

import (
	"image/color"
	"math"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/vebus"
)

// chartViews holds the chart of each page of each main window, so that a
// page built again gets the same chart. Used on the UI goroutine only.
var chartViews = map[*ui]map[vebus.Tab]*chartView{}

// pageVisual returns the chart shown with the settings page of tab, or nil
// for a page without one. The chart follows the model in u.settings and
// hides itself while the model lacks the settings it needs.
func pageVisual(u *ui, tab vebus.Tab) fyne.CanvasObject {
	var spec func(d *vebus.Device) *chartSpec
	switch tab {
	case vebus.TabCharger:
		spec = chargeChart
	case vebus.TabGrid:
		spec = acInputChart
	case vebus.TabInverter:
		spec = batteryChart
	default:
		return nil
	}
	if chartViews[u] == nil {
		chartViews[u] = map[vebus.Tab]*chartView{}
	}
	if c := chartViews[u][tab]; c != nil {
		return c.root
	}
	c := newChartView(u, spec)
	chartViews[u][tab] = c
	return c.root
}

// chartSpec describes a chart.
type chartSpec struct {
	title, subtitle string
	plot            plotScene
	legend          []legendItem
	notes           []string // short facts shown under the chart
	caption         string   // what the chart shows, in muted text
}

// legendItem is a colour swatch and what it stands for.
type legendItem struct {
	colour fyne.ThemeColorName
	text   string
}

// plotScene is what a plot draws. Coordinates are fractions of the plot
// area: x from its left (0) to its right (1) edge, y from its bottom (0) to
// its top (1) edge. Labels may lie outside it, in the margins.
type plotScene struct {
	minWidth, minHeight      float32 // of the whole plot, in px
	left, top, right, bottom float32 // margins around the plot area, in px
	areas                    []plotArea
	lines                    []plotLine
	labels                   []plotLabel
}

// plotArea is a filled rectangle.
type plotArea struct {
	x1, y1, x2, y2 float32
	colour         fyne.ThemeColorName
	alpha          uint8
}

// plotLine is a polyline.
type plotLine struct {
	pts    []fyne.Position
	colour fyne.ThemeColorName
	alpha  uint8
	width  float32
}

// labelPlace is where a label stands vertically from its anchor.
type labelPlace int

const (
	placeAbove  labelPlace = iota // the value just above the anchor, the name above it
	placeBelow                    // the value just below the anchor, the name below it
	placeMiddle                   // the value centred on the anchor; no name
)

// plotLabel is a value, optionally with a name in smaller muted text,
// anchored at (x, y).
type plotLabel struct {
	x, y        float32
	value, name string
	place       labelPlace
	// align says which edge of the label is at x: leading puts its left
	// edge just right of x, trailing its right edge just left of x.
	align fyne.TextAlign
	muted bool // the value in muted, regular text instead of bold
	// row groups labels that are moved apart sideways so that they do not
	// overlap; 0 for a label that stays where it is.
	row int
}

// Opacity of the tints of the charts.
const (
	tintPhase = 0x18 // a phase band of the charge profile
	tintZone  = 0x80 // a zone of a voltage scale; half opacity keeps the zones apart for colour-blind readers
)

// scaleX returns the position of v on a scale from lo to hi.
func scaleX(v, lo, hi float64) float32 {
	if hi <= lo {
		return 0.5
	}
	return float32((v - lo) / (hi - lo))
}

// setting returns the display value of setting id of d, or false when d
// does not have it or it holds no value.
func setting(d *vebus.Device, id uint16) (float64, bool) {
	v, err := d.Display(vebus.SettingID(id))
	return v, err == nil
}

// absolute returns the level setting id stands for (vebus.Device.Absolute),
// or false when d does not have it.
func absolute(d *vebus.Device, id uint16) (float64, bool) {
	if _, ok := setting(d, id); !ok {
		return 0, false
	}
	v, err := d.Absolute(id)
	return v, err == nil
}

// hours formats a time in minutes as hours.
func hours(minutes float64) string {
	h := minutes / 60
	decimals := 0
	if h != math.Trunc(h) {
		decimals = 1
	}
	return tr("{{.Value}} h", args{"Value": formatNumber(h, decimals)})
}

// days formats a time in minutes as days.
func days(minutes float64) string {
	d := minutes / 1440
	decimals := 0
	if d != math.Trunc(d) {
		decimals = 1
	}
	return tr("{{.Value}} d", args{"Value": formatNumber(d, decimals)})
}

// Phases of the charge profile, as fractions of its width.
const (
	bulkEnd       = 0.3
	absorptionEnd = 0.56
	repeatStart   = 0.76
	repeatEnd     = 0.9
	// The voltage panel is the top of the plot, the current panel the
	// bottom; the gap between them holds the current panel's name.
	voltageBottom = 0.42
	currentTop    = 0.28
)

// chargeChart returns the charge profile of d: bulk at the charge current
// up to the absorption voltage, absorption, then float, and repeated
// absorption when it is on. It needs the absorption and float voltages
// (settings 2 and 3).
func chargeChart(d *vebus.Device) *chartSpec {
	vAbs, ok1 := setting(d, 2)
	vFloat, ok2 := setting(d, 3)
	if !ok1 || !ok2 {
		return nil
	}
	s := &chartSpec{
		title:   tr("Charge profile"),
		caption: tr("Schematic, not to scale: the shape of a charge cycle with these settings, not a simulation."),
	}
	adaptive := false
	if e, ok := d.Catalogue().Entry(vebus.SettingID(10)); ok {
		if raw, err := d.Raw(e.ID()); err == nil {
			name := choiceLabels()[10][raw]
			if name == "" {
				name = e.Choices[raw]
			}
			if name != "" {
				s.subtitle = tr("Charge algorithm: {{.Algorithm}}", args{"Algorithm": name})
			}
			adaptive = raw >= 2
		}
	}
	// Repeated absorption is shown when it has a length and an interval.
	repTime, ok1 := setting(d, 7)
	repEvery, ok2 := setting(d, 8)
	repeat := ok1 && ok2 && repTime > 0 && repEvery > 0
	if repeat {
		s.notes = append(s.notes, tr("Repeated absorption: {{.Time}} every {{.Interval}}.",
			args{"Time": hours(repTime), "Interval": days(repEvery)}))
	}
	iBulk, hasCurrent := setting(d, 4)
	hasCurrent = hasCurrent && iBulk > 0

	p := &s.plot
	p.minWidth, p.minHeight = 360, 200
	p.left, p.top, p.right, p.bottom = 58, 20, 8, 36

	// Phase bands over both panels.
	p.areas = append(p.areas, plotArea{x1: bulkEnd, y1: 0, x2: absorptionEnd, y2: 1, colour: theme.ColorNamePrimary, alpha: tintPhase})
	if repeat {
		p.areas = append(p.areas, plotArea{x1: repeatStart, y1: 0, x2: repeatEnd, y2: 1, colour: theme.ColorNamePrimary, alpha: tintPhase})
	}
	p.labels = append(p.labels,
		plotLabel{x: bulkEnd / 2, y: 0, value: tr("Bulk"), place: placeBelow, row: 1},
		plotLabel{x: (bulkEnd + absorptionEnd) / 2, y: 0, value: tr("Absorption"), place: placeBelow, row: 1},
	)
	if t, ok := setting(d, 9); ok {
		text := hours(t)
		if adaptive {
			text = tr("up to {{.Time}}", args{"Time": text})
		}
		p.labels[len(p.labels)-1].name = text
	}
	if repeat {
		p.labels = append(p.labels,
			plotLabel{x: (absorptionEnd + repeatStart) / 2, y: 0, value: tr("Float"), place: placeBelow, row: 1},
			plotLabel{x: (repeatStart + repeatEnd) / 2, y: 0, value: tr("Repeat"), name: hours(repTime), place: placeBelow, row: 1})
	} else {
		p.labels = append(p.labels, plotLabel{x: (absorptionEnd + 1) / 2, y: 0, value: tr("Float"), place: placeBelow, row: 1})
	}

	// The voltage panel: from a start voltage below float up to absorption.
	vTop, vBottom := float32(1), float32(voltageBottom)
	if !hasCurrent {
		vBottom = 0
	}
	// The domain is a multiple of the step from float to absorption, so
	// that the two levels stay apart however close they are.
	span := vAbs - vFloat
	if span <= 0 {
		span = vAbs * 0.03
	}
	vStart := min(vFloat, vAbs) - 1.2*span
	lo, hi := vStart-0.2*span, max(vAbs, vFloat)+0.35*span
	vy := func(v float64) float32 { return vBottom + (vTop-vBottom)*scaleX(v, lo, hi) }
	var volts []fyne.Position
	for i := range 9 {
		t := float64(i) / 8
		// Rising slowly, then faster towards the absorption voltage.
		v := vStart + (vAbs-vStart)*(0.55*t+0.45*t*t*t)
		volts = append(volts, fyne.NewPos(float32(bulkEnd*t), vy(v)))
	}
	const drop = 0.025 // width of a step between two levels
	volts = append(volts, fyne.NewPos(absorptionEnd, vy(vAbs)), fyne.NewPos(absorptionEnd+drop, vy(vFloat)))
	if repeat {
		volts = append(volts, fyne.NewPos(repeatStart, vy(vFloat)), fyne.NewPos(repeatStart+drop, vy(vAbs)),
			fyne.NewPos(repeatEnd, vy(vAbs)), fyne.NewPos(repeatEnd+drop, vy(vFloat)))
	}
	volts = append(volts, fyne.NewPos(1, vy(vFloat)))
	p.lines = append(p.lines,
		plotLine{pts: []fyne.Position{{X: 0, Y: vy(vAbs)}, {X: 1, Y: vy(vAbs)}}, colour: theme.ColorNameSeparator, alpha: 0xff, width: 1},
		plotLine{pts: []fyne.Position{{X: 0, Y: vy(vFloat)}, {X: 1, Y: vy(vFloat)}}, colour: theme.ColorNameSeparator, alpha: 0xff, width: 1},
		plotLine{pts: []fyne.Position{{X: 0, Y: vBottom}, {X: 1, Y: vBottom}}, colour: theme.ColorNameSeparator, alpha: 0xff, width: 1},
		plotLine{pts: volts, colour: theme.ColorNamePrimary, alpha: 0xff, width: 2.5},
	)
	vLabel := func(v float64) string { return tr("{{.Value}} V", args{"Value": formatNumber(v, 2)}) }
	p.labels = append(p.labels,
		plotLabel{x: 0, y: vTop, value: tr("Battery voltage"), place: placeAbove, align: fyne.TextAlignLeading, muted: true},
		plotLabel{x: 0, y: vy(vAbs), value: vLabel(vAbs), place: placeMiddle, align: fyne.TextAlignTrailing},
		plotLabel{x: 0, y: vy(vFloat), value: vLabel(vFloat), place: placeMiddle, align: fyne.TextAlignTrailing},
	)

	// The current panel: the charge current in bulk, falling during
	// absorption, low in float.
	if hasCurrent {
		iy := func(i float64) float32 { return currentTop * float32(i/iBulk) }
		tail, floatI := 0.12*iBulk, 0.04*iBulk
		cur := []fyne.Position{{X: 0, Y: iy(iBulk)}, {X: bulkEnd, Y: iy(iBulk)}}
		decay := func(from, to, i0, i1 float64) {
			for i := 1; i <= 8; i++ {
				t := float64(i) / 8
				cur = append(cur, fyne.NewPos(float32(from+(to-from)*t), iy(i1+(i0-i1)*math.Exp(-4*t))))
			}
		}
		decay(bulkEnd, absorptionEnd, iBulk, tail)
		cur = append(cur, fyne.NewPos(absorptionEnd+drop, iy(floatI)))
		if repeat {
			cur = append(cur, fyne.NewPos(repeatStart, iy(floatI)))
			decay(repeatStart, repeatEnd, 0.3*iBulk, floatI)
		}
		cur = append(cur, fyne.NewPos(1, iy(floatI)))
		p.lines = append(p.lines,
			plotLine{pts: []fyne.Position{{X: 0, Y: 0}, {X: 1, Y: 0}}, colour: theme.ColorNameSeparator, alpha: 0xff, width: 1},
			plotLine{pts: cur, colour: theme.ColorNamePrimary, alpha: 0xff, width: 2.5},
		)
		p.labels = append(p.labels,
			plotLabel{x: 0, y: currentTop, value: tr("Charge current"), place: placeAbove, align: fyne.TextAlignLeading, muted: true},
			plotLabel{x: 0, y: iy(iBulk), value: tr("{{.Value}} A", args{"Value": formatNumber(iBulk, 0)}), place: placeMiddle, align: fyne.TextAlignTrailing},
			plotLabel{x: 0, y: 0, value: tr("{{.Value}} A", args{"Value": formatNumber(0, 0)}), place: placeMiddle, align: fyne.TextAlignTrailing, muted: true},
		)
	}
	return s
}

// scaleMark is a labelled level on a voltage scale.
type scaleMark struct {
	v        float64
	name     string
	above    bool
	context  bool // shown for comparison: muted, thin
	decimals int
}

// scaleZone is a coloured stretch of a voltage scale.
type scaleZone struct {
	from, to float64
	colour   fyne.ThemeColorName
}

// Vertical extent of the bar of a voltage scale and of its ticks.
const (
	barBottom, barTop   = 0.42, 0.58
	tickBottom, tickTop = 0.3, 0.7
)

// scaleScene draws marks and zones on a horizontal scale from lo to hi.
// Marks above the bar and marks below it are kept from overlapping.
func scaleScene(lo, hi float64, zones []scaleZone, marks []scaleMark) plotScene {
	p := plotScene{minWidth: 360, minHeight: 130, left: 8, top: 4, right: 8, bottom: 4}
	for _, z := range zones {
		if z.to > z.from {
			p.areas = append(p.areas, plotArea{x1: scaleX(z.from, lo, hi), y1: barBottom, x2: scaleX(z.to, lo, hi), y2: barTop,
				colour: z.colour, alpha: tintZone})
		}
	}
	for _, m := range marks {
		x := scaleX(m.v, lo, hi)
		l := plotLabel{x: x, value: tr("{{.Value}} V", args{"Value": formatNumber(m.v, m.decimals)}), name: m.name, muted: m.context}
		y1, y2 := float32(tickBottom), float32(barTop)
		if m.above {
			y1, y2 = barBottom, tickTop
			l.y, l.place, l.row = tickTop, placeAbove, 1
		} else {
			l.y, l.place, l.row = tickBottom, placeBelow, 2
		}
		line := plotLine{pts: []fyne.Position{{X: x, Y: y1}, {X: x, Y: y2}}, colour: theme.ColorNameForeground, alpha: 0xff, width: 2}
		if m.context {
			line.colour, line.width = theme.ColorNamePlaceHolder, 1
		}
		p.lines = append(p.lines, line)
		p.labels = append(p.labels, l)
	}
	return p
}

// acInputChart returns the AC input voltage window of d: where the input
// disconnects (settings 44 and 46) and where it connects again (45 and 47,
// stored relative to them), around the nominal output voltage (setting 5).
func acInputChart(d *vebus.Device) *chartSpec {
	lowOff, ok1 := setting(d, 44)
	highOff, ok2 := setting(d, 46)
	if !ok1 || !ok2 || highOff <= lowOff {
		return nil
	}
	lowOn, ok := absolute(d, 45)
	if !ok {
		lowOn = lowOff
	}
	highOn, ok := absolute(d, 47)
	if !ok {
		highOn = highOff
	}
	marks := []scaleMark{
		{v: lowOff, name: tr("Disconnect")},
		{v: highOff, name: tr("Disconnect")},
	}
	if lowOn != lowOff {
		marks = append(marks, scaleMark{v: lowOn, name: tr("Reconnect"), above: true})
	}
	if highOn != highOff {
		marks = append(marks, scaleMark{v: highOn, name: tr("Reconnect"), above: true})
	}
	lo, hi := min(lowOff, lowOn), max(highOff, highOn)
	if nominal, ok := setting(d, 5); ok {
		marks = append(marks, scaleMark{v: nominal, name: tr("Nominal"), context: true})
		lo, hi = min(lo, nominal), max(hi, nominal)
	}
	pad := 0.12 * (hi - lo)
	lo, hi = lo-pad, hi+pad
	zones := []scaleZone{
		{lo, lowOff, colorNameZoneOff},
		{lowOff, lowOn, colorNameZoneWarn},
		{lowOn, highOn, colorNameZoneOK},
		{highOn, highOff, colorNameZoneWarn},
		{highOff, hi, colorNameZoneOff},
	}
	s := &chartSpec{
		title: tr("AC input voltage window"),
		plot:  scaleScene(lo, hi, zones, marks),
		legend: []legendItem{
			{colorNameZoneOK, tr("Accepted")},
			{colorNameZoneWarn, tr("Accepted until a disconnect")},
			{colorNameZoneOff, tr("Disconnected")},
		},
		caption: tr("The AC input is used while its voltage stays between the disconnect levels. After a disconnect it is used again once the voltage is back between the reconnect levels."),
	}
	if on, err := d.Flag(27); err == nil && on {
		s.notes = append(s.notes, tr("A wide AC input frequency range is accepted."))
	}
	return s
}

// batteryChart returns the battery voltage levels of the inverter on d:
// where it shuts down (setting 11), restarts (12, relative to 11) and gives
// the pre-alarm (63, relative to the restart level), with the float and
// absorption voltages for comparison.
func batteryChart(d *vebus.Device) *chartSpec {
	shut, ok := setting(d, 11)
	if !ok {
		return nil
	}
	marks := []scaleMark{{v: shut, name: tr("Shut down"), decimals: 2}}
	low := shut // the top of the low battery zone
	if v, ok := absolute(d, 12); ok {
		marks = append(marks, scaleMark{v: v, name: tr("Restart"), decimals: 2})
		low = v
	}
	if v, ok := absolute(d, 63); ok {
		marks = append(marks, scaleMark{v: v, name: tr("Pre-alarm"), decimals: 2})
		low = v
	}
	if v, ok := setting(d, 3); ok {
		marks = append(marks, scaleMark{v: v, name: tr("Float"), context: true, decimals: 2})
	}
	if v, ok := setting(d, 2); ok {
		marks = append(marks, scaleMark{v: v, name: tr("Absorption"), context: true, decimals: 2})
	}
	// Labels alternate below and above the bar in the order of the levels,
	// so that neighbours are on different sides.
	slices.SortStableFunc(marks, func(a, b scaleMark) int {
		switch {
		case a.v < b.v:
			return -1
		case a.v > b.v:
			return 1
		}
		return 0
	})
	lo, hi := marks[0].v, marks[len(marks)-1].v
	for i := range marks {
		marks[i].above = i%2 == 1
	}
	pad := max(0.1*(hi-lo), 0.05*hi)
	lo, hi = lo-pad, hi+pad
	low = max(low, shut)
	zones := []scaleZone{
		{lo, shut, colorNameZoneOff},
		{shut, low, colorNameZoneWarn},
		{low, hi, colorNameZoneOK},
	}
	return &chartSpec{
		title: tr("Battery voltage levels"),
		plot:  scaleScene(lo, hi, zones, marks),
		legend: []legendItem{
			{colorNameZoneOff, tr("Inverter off")},
			{colorNameZoneWarn, tr("Low battery")},
			{colorNameZoneOK, tr("Normal")},
		},
		caption: tr("The inverter shuts down when the battery voltage falls to the shut-down level and starts again once it rises to the restart level. Float and absorption are shown for comparison."),
	}
}

// chartView is the card that shows a chart of the loaded model.
type chartView struct {
	u       *ui
	spec    func(d *vebus.Device) *chartSpec
	root    *widget.Card
	plot    *plot
	legend  *fyne.Container
	notes   *fyne.Container
	caption *widget.RichText
	current *chartSpec // shown; nil while hidden
}

func newChartView(u *ui, spec func(d *vebus.Device) *chartSpec) *chartView {
	c := &chartView{u: u, spec: spec, plot: newPlot(), legend: container.New(layout.NewRowWrapLayout()), notes: container.NewVBox(),
		caption: mutedText("")}
	c.root = widget.NewCard("", "", container.NewVBox(c.plot, c.legend, c.notes, c.caption))
	u.settings.onChange(func(bool) { c.update() })
	// A page that is not shown is not in the window, so it does not take
	// part in the theme change; the chart follows the theme itself.
	u.app.Settings().AddListener(func(fyne.Settings) { fyne.Do(c.plot.Refresh) })
	c.update()
	return c
}

// update shows the chart of the loaded model, or hides the card when the
// model lacks what the chart needs.
func (c *chartView) update() {
	var s *chartSpec
	if d := c.u.settings.dev; d != nil {
		s = c.spec(d)
	}
	c.current = s
	if s == nil {
		c.root.Hide()
		return
	}
	c.root.SetTitle(s.title)
	c.root.SetSubTitle(s.subtitle)
	c.plot.setScene(s.plot)
	c.legend.Objects = nil
	for _, it := range s.legend {
		sw := newColourRect(it.colour, fyne.NewSquareSize(10), theme.SizeNameSelectionRadius)
		c.legend.Add(container.NewHBox(container.NewCenter(sw), widget.NewLabel(it.text)))
	}
	c.legend.Hidden = len(s.legend) == 0
	c.legend.Refresh()
	c.notes.Objects = nil
	for _, n := range s.notes {
		l := widget.NewLabel(n)
		l.Wrapping = fyne.TextWrapWord
		c.notes.Add(l)
	}
	c.notes.Hidden = len(s.notes) == 0
	c.notes.Refresh()
	c.caption.Segments[0].(*widget.TextSegment).Text = s.caption
	c.caption.Refresh()
	c.root.Show()
}

// plot draws a plotScene.
type plot struct {
	widget.BaseWidget
	scene plotScene
}

func newPlot() *plot {
	p := &plot{}
	p.ExtendBaseWidget(p)
	return p
}

func (p *plot) setScene(s plotScene) {
	p.scene = s
	p.Refresh()
}

func (p *plot) CreateRenderer() fyne.WidgetRenderer {
	r := &plotRenderer{p: p}
	r.Refresh()
	return r
}

type plotRenderer struct {
	p       *plot
	objects []fyne.CanvasObject
}

func (r *plotRenderer) MinSize() fyne.Size {
	return fyne.NewSize(r.p.scene.minWidth, r.p.scene.minHeight)
}

func (r *plotRenderer) Layout(size fyne.Size) { r.build(size) }

func (r *plotRenderer) Refresh() {
	r.build(r.p.Size())
}

func (r *plotRenderer) Objects() []fyne.CanvasObject { return r.objects }

func (r *plotRenderer) Destroy() {}

// placedLabel is a label with its texts and its box in pixels.
type placedLabel struct {
	l           plotLabel
	value, name *canvas.Text
	x, w        float32 // left edge and width
}

// build draws the scene at size.
func (r *plotRenderer) build(size fyne.Size) {
	s := r.p.scene
	th := r.p.Theme()
	variant := fyne.CurrentApp().Settings().ThemeVariant()
	colour := func(n fyne.ThemeColorName, alpha uint8) color.Color {
		c := color.NRGBAModel.Convert(th.Color(n, variant)).(color.NRGBA)
		if alpha != 0xff {
			c.A = uint8(uint16(c.A) * uint16(alpha) / 0xff)
		}
		return c
	}
	w, h := size.Width-s.left-s.right, size.Height-s.top-s.bottom
	px := func(x float32) float32 { return s.left + x*w }
	py := func(y float32) float32 { return s.top + (1-y)*h }

	r.objects = r.objects[:0]
	for _, a := range s.areas {
		rect := canvas.NewRectangle(colour(a.colour, a.alpha))
		rect.Move(fyne.NewPos(px(a.x1), py(a.y2)))
		rect.Resize(fyne.NewSize(px(a.x2)-px(a.x1), py(a.y1)-py(a.y2)))
		r.objects = append(r.objects, rect)
	}
	for _, l := range s.lines {
		c := colour(l.colour, l.alpha)
		for i := 1; i < len(l.pts); i++ {
			seg := canvas.NewLine(c)
			seg.StrokeWidth = l.width
			seg.Position1 = fyne.NewPos(px(l.pts[i-1].X), py(l.pts[i-1].Y))
			seg.Position2 = fyne.NewPos(px(l.pts[i].X), py(l.pts[i].Y))
			r.objects = append(r.objects, seg)
			if i < len(l.pts)-1 && l.width > 1 {
				// A round joint, so that thick segments meet without gaps.
				dot := canvas.NewCircle(c)
				dot.Move(seg.Position2.SubtractXY(l.width/2, l.width/2))
				dot.Resize(fyne.NewSquareSize(l.width))
				r.objects = append(r.objects, dot)
			}
		}
	}

	textSize, nameSize := th.Size(theme.SizeNameText), th.Size(theme.SizeNameCaptionText)
	const gap = 3
	var placed []*placedLabel
	for _, l := range s.labels {
		pl := &placedLabel{l: l, value: canvas.NewText(l.value, colour(theme.ColorNameForeground, 0xff))}
		pl.value.TextSize = textSize
		if l.muted {
			pl.value.Color = colour(theme.ColorNamePlaceHolder, 0xff)
			pl.value.TextSize = nameSize
		} else {
			pl.value.TextStyle.Bold = true
		}
		pl.w = pl.value.MinSize().Width
		if l.name != "" && l.place != placeMiddle {
			pl.name = canvas.NewText(l.name, colour(theme.ColorNamePlaceHolder, 0xff))
			pl.name.TextSize = nameSize
			pl.w = max(pl.w, pl.name.MinSize().Width)
		}
		switch l.align {
		case fyne.TextAlignLeading:
			pl.x = px(l.x) + gap
		case fyne.TextAlignTrailing:
			pl.x = px(l.x) - gap - pl.w
		default:
			pl.x = px(l.x) - pl.w/2
		}
		placed = append(placed, pl)
	}
	spreadLabels(placed, size.Width)
	for _, pl := range placed {
		vs := pl.value.MinSize()
		y := py(pl.l.y)
		var vy, ny float32
		switch pl.l.place {
		case placeAbove:
			vy = y - gap - vs.Height
			if pl.name != nil {
				ny = vy - pl.name.MinSize().Height + 2
			}
		case placeBelow:
			vy = y + gap
			ny = vy + vs.Height - 2
		default:
			vy = y - vs.Height/2
		}
		pl.value.Move(fyne.NewPos(pl.x+(pl.w-vs.Width)/2, vy))
		pl.value.Resize(vs)
		r.objects = append(r.objects, pl.value)
		if pl.name != nil {
			ns := pl.name.MinSize()
			pl.name.Move(fyne.NewPos(pl.x+(pl.w-ns.Width)/2, ny))
			pl.name.Resize(ns)
			r.objects = append(r.objects, pl.name)
		}
	}
}

// spreadLabels moves the labels of each row apart so that they keep a gap
// between them, and keeps them within width where they fit.
func spreadLabels(labels []*placedLabel, width float32) {
	const gap = 8
	rows := map[int][]*placedLabel{}
	for _, l := range labels {
		if l.l.row != 0 {
			rows[l.l.row] = append(rows[l.l.row], l)
		}
	}
	for _, row := range rows {
		slices.SortStableFunc(row, func(a, b *placedLabel) int {
			switch {
			case a.x+a.w/2 < b.x+b.w/2:
				return -1
			case a.x+a.w/2 > b.x+b.w/2:
				return 1
			}
			return 0
		})
		// Push right past the left neighbour, then back left from the
		// right edge, then right again from the left edge.
		for i, l := range row {
			if i > 0 {
				l.x = max(l.x, row[i-1].x+row[i-1].w+gap)
			}
		}
		for i := len(row) - 1; i >= 0; i-- {
			limit := width
			if i < len(row)-1 {
				limit = row[i+1].x - gap
			}
			row[i].x = min(row[i].x, limit-row[i].w)
		}
		for i, l := range row {
			limit := float32(0)
			if i > 0 {
				limit = row[i-1].x + row[i-1].w + gap
			}
			l.x = max(l.x, limit)
		}
	}
}
