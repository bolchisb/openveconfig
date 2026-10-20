package main

import (
	"context"
	"image/color"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/mk2"
)

// Remote panel timing. The protocol leaves the poll rate to the host: each
// poll sends 'L' and then 'F' 5 through session.Do, one request at a time,
// and the client waits mk2.DefaultSlowTimeout for the 'F' 5 reply, more
// than the 750 ms of §3.3. Tests shorten these.
var (
	// panelPollInterval is the pause between two reads of the panel state.
	panelPollInterval = time.Second
	// blinkInterval is half the period of a blinking LED on screen. The
	// protocol does not define it; this is our choice.
	blinkInterval = 500 * time.Millisecond
)

// chargerProductType is the product type (AA of the AA BB CCC firmware
// version, §2) of a charger product. Reverse-engineered: the original tool
// shows a charger panel for it. Its switch positions are not known; this
// panel offers on and off.
const chargerProductType = 14

// noLimit as the variant 2 'S' limit tells the device to ignore the input
// current set point (§5).
const noLimit = 0x8000

// MasterMultiLED <AC input configuration> bits (§6.2).
const (
	overrideByPanel = 1 << 2 // a remote panel may override the input current limit
	dmcDedicated    = 1 << 3 // bytes 4 to 10 of the frame are not valid
)

// MasterMultiLED <Switch register> bits with the current switch state (§6.2).
const (
	switchRegCharge = 1 << 4
	switchRegInvert = 1 << 5
)

// ledUnknown is <LED on> and <LED blink> of the 'L' reply when the MK2
// cannot determine the LED status (§4).
const ledUnknown = 0x1F

// remotePanels holds the open remote panel of each ui. A nil entry means
// that the panel is closed but its session listener is registered. Used on
// the UI goroutine only.
var remotePanels = map[*ui]*remotePanel{}

// showRemotePanel opens the remote panel window for the open session, or
// brings the open one to the front. The panel closes when the session
// changes.
func showRemotePanel(u *ui) {
	if p := remotePanels[u]; p != nil {
		p.win.RequestFocus()
		return
	}
	if u.sess == nil {
		showMessage(u, tr("Remote panel"), tr("Connect to a device first."))
		return
	}
	if _, ok := remotePanels[u]; !ok {
		u.onSession(func(*session) {
			if p := remotePanels[u]; p != nil {
				p.win.Close()
			}
		})
	}
	p := newRemotePanel(u, u.sess)
	remotePanels[u] = p
	p.win.Show()
	p.start()
}

// panelLED is one LED of the panel.
type panelLED struct {
	bit byte // mk2.LEDMains ... mk2.LEDTemperature
	ind *indicator
}

// switchButton is the button for one switch position.
type switchButton struct {
	sw mk2.Switch
	b  *widget.Button
}

// remotePanel is the remote panel window. Its fields are used on the UI
// goroutine only.
type remotePanel struct {
	u      *ui
	sess   *session
	win    fyne.Window
	ctx    context.Context // ends when the window closes
	cancel context.CancelFunc
	cmds   sync.WaitGroup // running switch and limit commands

	leds   []*panelLED
	led    mk2.LEDStatus
	hasLED bool // led holds a reply
	phase  bool // blink phase

	switches []switchButton
	sw       mk2.Switch // reported or last chosen position; 0 if unknown

	limit              *widget.Slider // in 0.1 A
	limitText          *widget.Label
	limitMin, limitMax *widget.Label // the slider's range
	limitNote          *widget.Label
	status             *widget.Label
	override           bool   // the device lets the panel set the limit
	ownLimit           uint16 // limit set from this panel in 0.1 A; 0 if none
	dragging           bool   // the user is moving the slider
	// pending counts commands not yet sent. While one is pending, a poll
	// result may predate it, so it does not move the switch or slider.
	pending int
}

// newRemotePanel builds the panel window for session s without starting
// to poll.
func newRemotePanel(u *ui, s *session) *remotePanel {
	p := &remotePanel{u: u, sess: s, win: u.app.NewWindow(tr("Remote panel"))}
	p.ctx, p.cancel = context.WithCancel(context.Background())

	chargeLEDs := []fyne.CanvasObject{
		mutedText(tr("Charger")),
		p.newLED(mk2.LEDMains, tr("Mains on"), colorNameLEDGreen),
		p.newLED(mk2.LEDBulk, tr("Bulk"), colorNameLEDAmber),
		p.newLED(mk2.LEDAbsorption, tr("Absorption"), colorNameLEDAmber),
		p.newLED(mk2.LEDFloat, tr("Float"), colorNameLEDAmber),
	}
	inverterLEDs := []fyne.CanvasObject{
		mutedText(tr("Inverter")),
		p.newLED(mk2.LEDInverter, tr("Inverter"), colorNameLEDGreen),
		p.newLED(mk2.LEDOverload, tr("Overload"), colorNameLEDRed),
		p.newLED(mk2.LEDLowBattery, tr("Low battery"), colorNameLEDRed),
		p.newLED(mk2.LEDTemperature, tr("Temperature"), colorNameLEDRed),
	}
	ledCard := widget.NewCard(tr("Status"), "", container.NewGridWithColumns(2,
		container.NewVBox(chargeLEDs...), container.NewVBox(inverterLEDs...)))
	p.renderLEDs()

	// The switch is a segmented control: one button per position, the
	// position in force highlighted.
	// The product type is the AA of the AA BB CCC firmware version (§2).
	positions := []switchButton{{sw: mk2.SwitchOn}, {sw: mk2.SwitchOff}}
	if s.firmware[s.target]/100000 != chargerProductType {
		positions = append(positions, switchButton{sw: mk2.SwitchChargerOnly}, switchButton{sw: mk2.SwitchInverterOnly})
	}
	buttons := container.NewGridWithColumns(len(positions))
	for _, pos := range positions {
		pos.b = widget.NewButton(switchLabel(pos.sw), func() { p.setSwitch(pos.sw) })
		pos.b.Importance = widget.LowImportance
		p.switches = append(p.switches, pos)
		buttons.Add(pos.b)
	}
	segmented := container.NewStack(newColourRect(theme.ColorNameInputBackground, fyne.Size{}, theme.SizeNameInputRadius),
		container.New(layout.NewCustomPaddedLayout(2, 2, 2, 2), buttons))

	p.limit = widget.NewSlider(0, 1)
	p.limit.Step = 1
	p.limit.Disable()
	p.limit.OnChanged = func(v float64) {
		p.dragging = true
		p.limitText.SetText(deciAmps(uint16(v)))
	}
	p.limit.OnChangeEnded = func(v float64) {
		p.dragging = false
		p.setLimit(uint16(v))
	}
	p.limitText = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	p.limitText.SizeName = theme.SizeNameHeadingText
	p.limitMin, p.limitMax = widget.NewLabel(""), widget.NewLabel("")
	p.limitMin.SizeName, p.limitMax.SizeName = theme.SizeNameCaptionText, theme.SizeNameCaptionText
	p.limitNote = widget.NewLabel("")
	p.limitNote.Wrapping = fyne.TextWrapWord
	p.status = widget.NewLabel("")
	p.status.Wrapping = fyne.TextWrapWord

	limitCard := widget.NewCard(tr("Input current limiter"), "", container.NewVBox(
		p.limitText, p.limit, container.NewBorder(nil, nil, p.limitMin, p.limitMax), p.limitNote))
	p.win.SetContent(container.NewStack(newColourRect(colorNamePage, fyne.Size{}, ""),
		container.New(layout.NewCustomPaddedLayout(8, 8, 12, 12), container.NewVBox(
			ledCard, widget.NewCard(tr("Switch"), "", segmented), limitCard, p.status))))
	p.win.Resize(fyne.NewSize(460, 600))
	p.win.SetOnClosed(func() {
		p.cancel()
		remotePanels[u] = nil
	})
	return p
}

// newLED adds an LED for bit, lit in theme colour colour, and returns it
// with its name.
func (p *remotePanel) newLED(bit byte, name string, colour fyne.ThemeColorName) fyne.CanvasObject {
	l := &panelLED{bit: bit, ind: newIndicator(colour, 14)}
	p.leds = append(p.leds, l)
	return container.NewHBox(container.NewCenter(l.ind), widget.NewLabel(name))
}

// switchLabel names a switch position.
func switchLabel(sw mk2.Switch) string {
	switch sw {
	case mk2.SwitchOn:
		return tr("On")
	case mk2.SwitchOff:
		return tr("Off")
	case mk2.SwitchChargerOnly:
		return tr("Charger only")
	default:
		return tr("Inverter only")
	}
}

// deciAmps formats a current given in 0.1 A.
func deciAmps(v uint16) string { return amps(float64(v)/10, 1) }

// start polls the device and blinks the LEDs until the window closes.
func (p *remotePanel) start() {
	pollEvery, blinkEvery := panelPollInterval, blinkInterval
	go func() {
		for {
			err := p.poll(p.ctx)
			if p.ctx.Err() != nil {
				return
			}
			if err != nil {
				fyne.Do(func() { p.showError(err) })
			}
			select {
			case <-p.ctx.Done():
				return
			case <-time.After(pollEvery):
			}
		}
	}()
	go func() {
		t := time.NewTicker(blinkEvery)
		defer t.Stop()
		for {
			select {
			case <-p.ctx.Done():
				return
			case <-t.C:
				fyne.Do(func() {
					p.phase = !p.phase
					p.renderLEDs()
				})
			}
		}
	}()
}

// poll reads the LED status and the MasterMultiLED frame once and shows
// them. The results are handed to the UI while the session is still held,
// so they reach it in the same order as the commands on the line.
func (p *remotePanel) poll(ctx context.Context) error {
	return p.sess.Do(ctx, func(ctx context.Context, c *mk2.Client) error {
		led, err := c.LED(ctx)
		if err != nil {
			return err
		}
		m, err := c.MasterMultiLED(ctx)
		if err != nil {
			return err
		}
		fyne.Do(func() { p.show(led, m) })
		return nil
	})
}

// showError shows a failed poll or command.
func (p *remotePanel) showError(err error) {
	p.status.SetText(tr("Communication with the device failed: {{.Err}}", args{"Err": err}))
}

// show displays a poll result.
func (p *remotePanel) show(led mk2.LEDStatus, m mk2.MasterMultiLED) {
	p.led, p.hasLED = led, true
	p.renderLEDs()
	p.status.SetText("")
	if led.On == ledUnknown && led.Blink == ledUnknown {
		p.status.SetText(tr("The device cannot report its LED status."))
	}

	valid := m.ACInputConfig&dmcDedicated == 0
	p.override = valid && m.ACInputConfig&overrideByPanel != 0
	if p.pending == 0 && m.HasSwitchRegister {
		p.showSwitch(switchFromRegister(m.SwitchRegister))
	}
	if !valid {
		p.limit.Disable()
		p.limitText.SetText("")
		p.limitMin.SetText("")
		p.limitMax.SetText("")
		p.limitNote.SetText(tr("The device does not report its AC input current limit."))
		return
	}
	// A limit of 0 has its own meaning (§5 note 3), so the slider never
	// goes down to it.
	lo, hi := max(m.MinInputLimit, 1), m.MaxInputLimit
	p.limit.Min, p.limit.Max = float64(lo), float64(hi)
	p.limitMin.SetText(deciAmps(lo))
	p.limitMax.SetText(deciAmps(hi))
	if p.override && hi > lo {
		p.limit.Enable()
		p.limitNote.SetText(tr("The device accepts a limit from {{.Min}} to {{.Max}}.", args{"Min": deciAmps(lo), "Max": deciAmps(hi)}))
	} else {
		p.limit.Disable()
		p.limitNote.SetText(tr("This device does not let a remote panel override its AC input current limit. It uses its own setting."))
	}
	if p.pending == 0 && !p.dragging {
		p.limit.Value = float64(m.ActualInputLimit)
		p.limitText.SetText(deciAmps(m.ActualInputLimit))
	}
	p.limit.Refresh()
}

// ledLit reports whether LED bit of st is lit in blink phase phase. A
// blinking LED whose <LED on> bit is 0 blinks in anti-phase (§4).
func ledLit(st mk2.LEDStatus, bit byte, phase bool) bool {
	on := st.On&bit != 0
	if st.Blink&bit == 0 {
		return on
	}
	return phase == on
}

// renderLEDs draws the LEDs for the current status and blink phase.
func (p *remotePanel) renderLEDs() {
	unknown := !p.hasLED || p.led.On == ledUnknown && p.led.Blink == ledUnknown
	for _, l := range p.leds {
		st := indicatorOff
		switch {
		case unknown:
			st = indicatorUnknown
		case ledLit(p.led, l.bit, p.phase):
			st = indicatorOn
		}
		l.ind.set(st)
	}
}

// indicatorState is what an indicator shows.
type indicatorState int

const (
	indicatorUnknown indicatorState = iota // not known: a hollow ring
	indicatorOff                           // a dim dot
	indicatorOn                            // a dot in its colour, with a glow
)

// indicator is a round lamp in a theme colour: an LED of the remote panel
// or the dot of a status chip.
type indicator struct {
	widget.BaseWidget
	colour fyne.ThemeColorName // when on
	state  indicatorState
	size   float32 // diameter of the dot
}

func newIndicator(colour fyne.ThemeColorName, size float32) *indicator {
	i := &indicator{colour: colour, size: size}
	i.ExtendBaseWidget(i)
	return i
}

// set shows st.
func (i *indicator) set(st indicatorState) {
	if i.state != st {
		i.state = st
		i.Refresh()
	}
}

func (i *indicator) CreateRenderer() fyne.WidgetRenderer {
	r := &indicatorRenderer{i: i, glow: canvas.NewCircle(color.Transparent), dot: canvas.NewCircle(color.Transparent)}
	r.Refresh()
	return r
}

type indicatorRenderer struct {
	i         *indicator
	glow, dot *canvas.Circle
}

// indicatorGlow is how far the glow of a lit indicator reaches.
const indicatorGlow = 3

func (r *indicatorRenderer) Layout(size fyne.Size) {
	c := fyne.NewPos(size.Width/2, size.Height/2)
	d := r.i.size
	r.dot.Move(fyne.NewPos(c.X-d/2, c.Y-d/2))
	r.dot.Resize(fyne.NewSquareSize(d))
	g := d + 2*indicatorGlow
	r.glow.Move(fyne.NewPos(c.X-g/2, c.Y-g/2))
	r.glow.Resize(fyne.NewSquareSize(g))
}

func (r *indicatorRenderer) MinSize() fyne.Size {
	return fyne.NewSquareSize(r.i.size + 2*indicatorGlow)
}

func (r *indicatorRenderer) Refresh() {
	th := r.i.Theme()
	v := fyne.CurrentApp().Settings().ThemeVariant()
	nrgba := func(n fyne.ThemeColorName) color.NRGBA { return color.NRGBAModel.Convert(th.Color(n, v)).(color.NRGBA) }
	r.glow.Hidden = r.i.state != indicatorOn
	r.dot.StrokeWidth = 0
	switch r.i.state {
	case indicatorOn:
		c := nrgba(r.i.colour)
		r.dot.FillColor = c
		r.glow.FillColor = withAlpha(c, 0x4d)
	case indicatorOff:
		fg := nrgba(theme.ColorNameForeground)
		r.dot.FillColor = withAlpha(fg, 0x1f)
		r.dot.StrokeColor, r.dot.StrokeWidth = withAlpha(fg, 0x40), 1
	default:
		r.dot.FillColor = color.Transparent
		r.dot.StrokeColor, r.dot.StrokeWidth = nrgba(theme.ColorNameDisabled), 1.5
	}
	r.Layout(r.i.Size())
	r.glow.Refresh()
	r.dot.Refresh()
}

func (r *indicatorRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.glow, r.dot} }

func (r *indicatorRenderer) Destroy() {}

// switchFromRegister returns the switch position of a MasterMultiLED
// <Switch register>.
func switchFromRegister(r byte) mk2.Switch {
	switch r & (switchRegCharge | switchRegInvert) {
	case switchRegCharge | switchRegInvert:
		return mk2.SwitchOn
	case switchRegCharge:
		return mk2.SwitchChargerOnly
	case switchRegInvert:
		return mk2.SwitchInverterOnly
	default:
		return mk2.SwitchOff
	}
}

// showSwitch marks sw as the switch position.
func (p *remotePanel) showSwitch(sw mk2.Switch) {
	p.sw = sw
	for _, s := range p.switches {
		imp := widget.LowImportance
		if s.sw == sw {
			imp = widget.HighImportance
		}
		if s.b.Importance != imp {
			s.b.Importance = imp
			s.b.Refresh()
		}
	}
}

// setSwitch sends switch position sw. It repeats a limit set from this
// panel, as a panel sends both together; otherwise it tells the device to
// ignore the limit.
func (p *remotePanel) setSwitch(sw mk2.Switch) {
	limit := uint16(noLimit)
	if p.override && p.ownLimit != 0 {
		limit = p.ownLimit
	}
	p.showSwitch(sw)
	p.send(mk2.State{Switch: sw, Limit: limit, Flags: mk2.StateFlagLimitDeciAmps})
}

// setLimit sends the AC input current limit, in 0.1 A, with the current
// switch position.
func (p *remotePanel) setLimit(limit uint16) {
	if !p.override {
		return
	}
	if p.sw == 0 {
		p.status.SetText(tr("Choose a switch position first."))
		return
	}
	p.ownLimit = limit
	p.send(mk2.State{Switch: p.sw, Limit: limit, Flags: mk2.StateFlagLimitDeciAmps})
}

// send sends the panel state st ('S', §5, variant 2) in the background.
func (p *remotePanel) send(st mk2.State) {
	p.pending++
	p.cmds.Go(func() {
		p.sess.Do(p.ctx, func(ctx context.Context, c *mk2.Client) error {
			err := c.SetState(ctx, st)
			fyne.Do(func() {
				p.pending--
				if err != nil && ctx.Err() == nil {
					p.showError(err)
				}
			})
			return err
		})
	})
}
