package main

// busRunner runs the operations of pkg/vebus/system/bus for a window, one
// at a time through the session's Do, with a dialog that shows their
// progress. The System Configurator and the Quick Configure wizard use it.

import (
	"context"
	"errors"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/vebus/system/bus"
)

// systemOptions are the options of the system sessions. Tests shorten the
// waits.
var systemOptions bus.Options

// busRunner belongs to one window and one session. Its fields are used on
// the UI goroutine only, except bs.
type busRunner struct {
	win    fyne.Window
	sess   *session        // nil: no operation can run
	ctx    context.Context // ends with close
	cancel context.CancelFunc
	// bs is the system session on sess's client, opened by the first
	// operation. It is used inside sess.Do only.
	bs *bus.Session
	// ops counts the operations of run that have not finished, done
	// included.
	ops sync.WaitGroup
}

func newBusRunner(w fyne.Window, s *session) *busRunner {
	r := &busRunner{win: w, sess: s}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	return r
}

// close stops what the runner started: the operation in progress, the
// keep-alive and the flashing LEDs.
func (r *busRunner) close() {
	r.cancel()
	if r.sess == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r.sess.Do(ctx, func(context.Context, *mk2.Client) error {
			if r.bs != nil {
				r.bs.Close()
			}
			return nil
		})
	}()
}

// openBus opens r.bs on cl if it is not open. Call it inside sess.Do.
func (r *busRunner) openBus(ctx context.Context, cl *mk2.Client) error {
	if r.bs != nil {
		return nil
	}
	bs, err := bus.Open(ctx, cl, systemOptions)
	if err != nil {
		return err
	}
	r.bs = bs
	return nil
}

// activeAddresses returns the short address of each unit of the last scan
// of bs. Call it inside sess.Do.
func activeAddresses(bs *bus.Session) map[uint32]byte {
	m := map[uint32]byte{}
	for _, u := range bs.Units() {
		if a, ok := bs.ActiveAddress(u.LongID); ok {
			m[u.LongID] = a
		}
	}
	return m
}

// run runs op on the system session in the background, with a dialog that
// shows its steps and can cancel it, and then calls done on the UI
// goroutine with op's error. Cancelling a write leads to its rollback.
func (r *busRunner) run(title string, op func(ctx context.Context, bs *bus.Session, p bus.Progress) error, done func(err error)) {
	ctx, cancel := context.WithCancel(r.ctx)
	step := widget.NewLabel(tr("Checking the interface…"))
	bar := widget.NewProgressBarInfinite()
	d := dialog.NewCustomWithoutButtons(title, container.NewVBox(step, bar), r.win)
	var stop *widget.Button
	stop = widget.NewButton(tr("Cancel"), func() {
		stop.Disable()
		cancel()
	})
	d.SetButtons([]fyne.CanvasObject{stop})
	d.Resize(fyne.NewSize(480, d.MinSize().Height))
	d.Show()
	progress := func(e bus.Event) {
		text := stepText(e)
		fyne.Do(func() { step.SetText(text) })
	}
	r.ops.Add(1)
	go func() {
		err := r.sess.Do(ctx, func(ctx context.Context, cl *mk2.Client) error {
			if err := r.openBus(ctx, cl); err != nil {
				return err
			}
			return op(ctx, r.bs, progress)
		})
		cancel()
		fyne.Do(func() {
			defer r.ops.Done()
			bar.Stop()
			d.Hide()
			done(err)
		})
	}()
}

// showFailure shows the error of a system operation, unless the user
// cancelled it before anything was written.
func (r *busRunner) showFailure(title string, err error, details string) {
	var be *bus.Error
	if !errors.As(err, &be) && errors.Is(err, context.Canceled) {
		return
	}
	msg := failureText(err)
	if details != "" {
		msg += "\n\n" + details
	}
	text := widget.NewLabel(msg)
	text.Wrapping = fyne.TextWrapWord
	d := dialog.NewCustom(title, tr("OK"), container.NewVScroll(text), r.win)
	d.Resize(fyne.NewSize(560, 360))
	d.Show()
}
