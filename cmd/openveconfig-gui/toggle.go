package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Size of a toggle switch: the track, and the gap between track and knob.
const (
	toggleWidth  = 36
	toggleHeight = 20
	toggleInset  = 3
)

// toggleSwitch is an on/off switch: a rounded track with a knob that sits
// on the right when on. Tapping it or pressing Space while it has the focus
// flips it, like a check box.
type toggleSwitch struct {
	widget.DisableableWidget
	On        bool
	OnChanged func(bool)
	focused   bool
}

func newToggleSwitch(changed func(bool)) *toggleSwitch {
	t := &toggleSwitch{OnChanged: changed}
	t.ExtendBaseWidget(t)
	return t
}

// SetOn sets the state, calls OnChanged when it changes and redraws.
func (t *toggleSwitch) SetOn(on bool) {
	if on == t.On {
		return
	}
	t.On = on
	if t.OnChanged != nil {
		t.OnChanged(on)
	}
	t.Refresh()
}

func (t *toggleSwitch) Tapped(*fyne.PointEvent) {
	if t.Disabled() {
		return
	}
	if c := fyne.CurrentApp().Driver().CanvasForObject(t); c != nil && !t.focused {
		c.Focus(t)
	}
	t.SetOn(!t.On)
}

func (t *toggleSwitch) FocusGained() {
	t.focused = true
	t.Refresh()
}

func (t *toggleSwitch) FocusLost() {
	t.focused = false
	t.Refresh()
}

func (t *toggleSwitch) TypedRune(r rune) {
	if r == ' ' && !t.Disabled() {
		t.SetOn(!t.On)
	}
}

func (t *toggleSwitch) TypedKey(*fyne.KeyEvent) {}

func (t *toggleSwitch) CreateRenderer() fyne.WidgetRenderer {
	r := &toggleRenderer{t: t, track: canvas.NewRectangle(color.Transparent), knob: canvas.NewCircle(color.Transparent),
		ring: canvas.NewRectangle(color.Transparent)}
	r.Refresh()
	return r
}

type toggleRenderer struct {
	t           *toggleSwitch
	ring, track *canvas.Rectangle
	knob        *canvas.Circle
}

func (r *toggleRenderer) MinSize() fyne.Size {
	pad := r.t.Theme().Size(theme.SizeNamePadding)
	return fyne.NewSize(toggleWidth+2*pad, toggleHeight+2*pad)
}

func (r *toggleRenderer) Layout(size fyne.Size) {
	pos := fyne.NewPos((size.Width-toggleWidth)/2, (size.Height-toggleHeight)/2)
	r.track.Move(pos)
	r.track.Resize(fyne.NewSize(toggleWidth, toggleHeight))
	r.ring.Move(pos.SubtractXY(2, 2))
	r.ring.Resize(fyne.NewSize(toggleWidth+4, toggleHeight+4))
	d := float32(toggleHeight - 2*toggleInset)
	x := pos.X + toggleInset
	if r.t.On {
		x = pos.X + toggleWidth - toggleInset - d
	}
	r.knob.Move(fyne.NewPos(x, pos.Y+toggleInset))
	r.knob.Resize(fyne.NewSquareSize(d))
}

func (r *toggleRenderer) Refresh() {
	th := r.t.Theme()
	v := fyne.CurrentApp().Settings().ThemeVariant()
	t := r.t
	track, knob := th.Color(theme.ColorNameInputBorder, v), th.Color(theme.ColorNameBackground, v)
	if t.On {
		track, knob = th.Color(theme.ColorNamePrimary, v), th.Color(theme.ColorNameForegroundOnPrimary, v)
	}
	if t.Disabled() {
		track = th.Color(theme.ColorNameDisabledButton, v)
		knob = th.Color(theme.ColorNameDisabled, v)
	}
	r.track.FillColor = track
	r.track.CornerRadius = toggleHeight / 2
	r.knob.FillColor = knob
	r.ring.FillColor = color.Transparent
	r.ring.CornerRadius = toggleHeight/2 + 2
	r.ring.StrokeWidth = 0
	if t.focused {
		r.ring.StrokeColor = th.Color(theme.ColorNameFocus, v)
		r.ring.StrokeWidth = 2
	}
	r.Layout(t.Size())
	canvas.Refresh(t)
}

func (r *toggleRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.ring, r.track, r.knob}
}

func (r *toggleRenderer) Destroy() {}
