package main

// Assistants (#81): whether the open settings file and the connected unit
// have assistants, reading a unit's assistant area into the file (the
// backup), and writing a file's area to its unit when the file is sent
// (the restore). The area is copied as it is (pkg/assistant/transfer);
// nothing here edits assistants. The card is on the Assistants page
// (assistantspage.go).

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/assistant"
	"github.com/bolchisb/openveconfig/pkg/assistant/transfer"
	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/rvsc"
	"github.com/bolchisb/openveconfig/pkg/simulator"
	"github.com/bolchisb/openveconfig/pkg/vebus"
	"github.com/bolchisb/openveconfig/pkg/vebus/system/bus"
)

// assistantWriteOptions are the options of the assistant writes, without
// Confirm and Progress. Tests shorten the waits.
var assistantWriteOptions transfer.Options

// assistantCodec returns the block codec for the devices of s: the
// simulator's in the demo, otherwise the one for real devices.
func assistantCodec(s *session) mk2.BlockCodec {
	if s.demo {
		return simulator.BlockCodec{}
	}
	return mk2.DeviceBlockCodec{}
}

// assistantsUnavailable returns why assistants cannot be read (write
// false) or written on the devices of s yet, or "".
func assistantsUnavailable(s *session, write bool) string {
	c := assistantCodec(s)
	if write && c.Hardware() && !transfer.WriteOnHardware {
		return tr("Writing assistants to real devices is off until it has been checked on hardware. Nothing is sent to the unit.")
	}
	if c.BlockSize() <= 0 {
		return tr("Reading assistants from real devices is not available yet: how a unit sends its assistant area has not been worked out. Nothing is sent to the unit.")
	}
	return ""
}

// areaSummary describes an assistant image in one line.
func areaSummary(img []byte) string {
	size := formatNumber(float64(len(img)), 0)
	if !assistant.ChecksumOK(img) {
		return tr("An assistant area of {{.Size}} bytes with a wrong checksum, as an interrupted write leaves it", args{"Size": size})
	}
	l, err := assistant.ParseLayout(img)
	if err != nil {
		return tr("An assistant area of {{.Size}} bytes whose layout is not known", args{"Size": size})
	}
	a := args{"Used": formatNumber(float64(l.Used), 0), "Free": formatNumber(float64(l.Free), 0)}
	if len(l.IDs) == 0 {
		return tr("No assistants · {{.Free}} bytes free", a)
	}
	ids := make([]string, len(l.IDs))
	for i, id := range l.IDs {
		ids[i] = strconv.Itoa(int(id))
	}
	a["IDs"] = strings.Join(ids, ", ")
	return trn("Assistant ID {{.IDs}} · {{.Used}} bytes used, {{.Free}} free",
		"Assistant IDs {{.IDs}} · {{.Used}} bytes used, {{.Free}} free", len(ids), a)
}

// fileAreaSummary describes the assistant area of a file's unit.
func fileAreaSummary(u *rvsc.Unit) string {
	img, err := u.AssistantImage()
	if err != nil {
		return tr("The file's assistant area cannot be read: {{.Err}}", args{"Err": err})
	}
	return areaSummary(img)
}

// runningText lists the IDs of running assistants. ID 5 is ESS (public
// document: Victron, "ESS mode 2 and 3" §3.6.2).
func runningText(ids []uint16) string {
	if len(ids) == 0 {
		return tr("none")
	}
	l := make([]string, len(ids))
	for i, id := range ids {
		l[i] = strconv.Itoa(int(id))
		if id == 5 {
			l[i] += " (ESS)"
		}
	}
	return strings.Join(l, ", ")
}

// locateUnit finds the unit longID on the bus, as the System Configurator
// does. Call it inside session.Do.
func locateUnit(ctx context.Context, c *mk2.Client, longID uint32) (transfer.Unit, error) {
	opts := systemOptions
	opts.NoKeepAlive = true
	bs, err := bus.Open(ctx, c, opts)
	if err != nil {
		return transfer.Unit{}, err
	}
	defer bs.Close()
	if _, err := bs.Scan(ctx, nil); err != nil {
		return transfer.Unit{}, err
	}
	a, ok := bs.ActiveAddress(longID)
	if !ok {
		return transfer.Unit{}, &uiError{msg: tr("No unit with long ID {{.LongID}} answered. Nothing was written.",
			args{"LongID": fmt.Sprintf("%08X", longID)})}
	}
	return transfer.Unit{Address: a, LongID: longID}, nil
}

// assistantsCard shows the assistants of the open settings file and of the
// connected unit, titled from the packages of ap, and reads a unit's
// assistants, on their own or into the file.
func assistantsCard(u *ui, ap *assistantsPage) fyne.CanvasObject {
	fileArea, unitArea := container.NewStack(), container.NewStack()
	hint := mutedText("")
	var unitImg []byte // what the last read found, for the session it was read on
	var unitRead string
	var readOn *session
	var update func()
	read := widget.NewButtonWithIcon(tr("Read from unit"), theme.DownloadIcon(), func() {
		s, addr := u.sess, u.unit
		runWithProgress(u, tr("Reading the assistants"), func(ctx context.Context, _ func(done, total int)) error {
			var a *transfer.Area
			err := s.Do(ctx, func(ctx context.Context, c *mk2.Client) error {
				var err error
				a, err = transfer.Read(ctx, c, assistantCodec(s), transfer.Unit{Address: addr})
				return err
			})
			if err != nil {
				return &uiError{err: err, msg: tr("The assistants cannot be read: {{.Err}}", args{"Err": err})}
			}
			fyne.Do(func() {
				unitImg, readOn = a.Image, s
				unitRead = tr("Unit at address {{.Address}}. Running: {{.Running}}.", args{"Address": addr, "Running": runningText(a.Running)})
				update()
			})
			return nil
		})
	})
	copyIn := widget.NewButtonWithIcon(tr("Copy into the file"), theme.DocumentSaveIcon(), func() {
		copyAreaIntoFile(u, func() { update() })
	})
	text := func(t string) fyne.CanvasObject {
		l := widget.NewLabel(t)
		l.Wrapping = fyne.TextWrapWord
		return l
	}
	update = func() {
		s, sf := u.sess, u.settings.file
		fileArea.Objects = []fyne.CanvasObject{text(tr("No settings file is open."))}
		if sf != nil {
			if img, err := sf.unit.AssistantImage(); err != nil {
				fileArea.Objects[0] = text(fileAreaSummary(sf.unit))
			} else {
				fileArea.Objects[0] = installedView(img, ap.title)
			}
		}
		why := ""
		switch {
		case s == nil:
			unitArea.Objects = []fyne.CanvasObject{text(tr("Not connected."))}
		case assistantsUnavailable(s, false) != "":
			why = assistantsUnavailable(s, false)
			unitArea.Objects = []fyne.CanvasObject{text(tr("Not available on real devices yet."))}
		case readOn == s:
			unitArea.Objects = []fyne.CanvasObject{container.NewVBox(text(unitRead), installedView(unitImg, ap.title))}
		default:
			unitArea.Objects = []fyne.CanvasObject{text(tr("Not read yet."))}
		}
		fileArea.Refresh()
		unitArea.Refresh()
		read.Disable()
		copyIn.Disable()
		if s != nil && why == "" {
			read.Enable()
			if sf != nil {
				copyIn.Enable()
			}
		}
		if why == "" {
			why = tr("Copy into the file reads the assistants of the file's unit and puts them into the file; save the file to keep them. " +
				"To write a file's assistants to its unit, send the file's settings and choose to write its assistants.")
		}
		hint.Segments[0].(*widget.TextSegment).Text = why
		hint.Refresh()
	}
	u.onSession(func(*session) { update() })
	u.settings.onChange(func(bool) { update() })
	ap.onScan = append(ap.onScan, func() { update() })
	update()
	rows := container.New(layout.NewFormLayout(),
		widget.NewLabelWithStyle(tr("In the file"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), fileArea,
		widget.NewLabelWithStyle(tr("In the unit"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), unitArea)
	kept := callout(theme.InfoIcon(), tr("A unit keeps its assistants when only its settings are sent: "+
		"they are written only when you send a file and choose to write the file's assistants."))
	return newFormCard(cardTitle(tr("Installed assistants")), rows, container.NewHBox(read, copyIn), hint, kept)
}

// copyAreaIntoFile reads the assistant area of the open file's unit, found
// on the bus by its long ID, into the file, and offers to save the file.
// done is called after the area changed.
func copyAreaIntoFile(u *ui, done func()) {
	s, sf := u.sess, u.settings.file
	if s == nil || sf == nil {
		return
	}
	fu := sf.unit
	runWithProgress(u, tr("Reading the assistants into the file"), func(ctx context.Context, _ func(done, total int)) error {
		var a *transfer.Area
		err := s.Do(ctx, func(ctx context.Context, c *mk2.Client) error {
			unit, err := locateUnit(ctx, c, fu.LongID)
			if err == nil {
				a, err = transfer.Read(ctx, c, assistantCodec(s), unit)
			}
			return err
		})
		var ue *uiError
		switch {
		case errors.As(err, &ue):
			return err
		case err != nil:
			return &uiError{err: err, msg: tr("The assistants cannot be read: {{.Err}}", args{"Err": err})}
		case a.Firmware != fu.Firmware():
			return &uiError{msg: tr("The unit has firmware {{.Unit}}, the file's unit {{.File}}: its assistants would not be accepted back. The file was not changed.",
				args{"Unit": strconv.FormatUint(uint64(a.Firmware), 10), "File": strconv.FormatUint(uint64(fu.Firmware()), 10)})}
		case !a.ChecksumOK:
			return &uiError{msg: tr("The checksum of the unit's assistant area is wrong, as an interrupted write leaves it. The file was not changed.")}
		}
		fyne.Do(func() {
			if u.settings.file != sf {
				return
			}
			fu.SetAssistantImage(a.Image)
			done()
			c := newConfirm(u, tr("Copy into the file"), tr("The assistants of unit {{.Unit}} are in {{.File}} now: {{.Area}}. Save the file to keep them.",
				args{"Unit": vebus.UnitLabel(fu), "File": sf.name, "Area": areaSummary(a.Image)}), tr("Save settings"), func() { saveSettingsFile(u) })
			showWithKeys(u, c, c.Confirm)
		})
		return nil
	})
}

// assistPlan is the outcome of the checks of writing a file's assistant
// area to a unit, before anything is written.
type assistPlan struct {
	unit transfer.Unit
	want *transfer.Area
	dry  *transfer.Report // the dry run, when the checks ran
	// blocked says why the area cannot be written; "" when it can.
	blocked string
}

// planAssistants checks whether the assistant area of file's unit can be
// written to the unit at addr: the unit with the file unit's long ID must
// answer there and the dry run of the write must pass. Nothing is written.
func planAssistants(ctx context.Context, s *session, file *settingsFile, addr byte) *assistPlan {
	p := &assistPlan{}
	if why := assistantsUnavailable(s, true); why != "" {
		p.blocked = why
		return p
	}
	img, err := file.unit.AssistantImage()
	if err != nil {
		p.blocked = tr("The file's assistant area cannot be read: {{.Err}}", args{"Err": err})
		return p
	}
	p.want = &transfer.Area{LongID: file.unit.LongID, Firmware: file.unit.Firmware(), Image: img}
	err = s.Do(ctx, func(ctx context.Context, c *mk2.Client) error {
		unit, err := locateUnit(ctx, c, p.want.LongID)
		if err != nil {
			return err
		}
		if unit.Address != addr {
			return &uiError{msg: tr("The file's unit (long ID {{.LongID}}) answers at address {{.Found}}, not at address {{.Address}}. Nothing was written.",
				args{"LongID": fmt.Sprintf("%08X", unit.LongID), "Found": unit.Address, "Address": addr})}
		}
		p.unit = unit
		p.dry, err = transfer.Write(ctx, c, assistantCodec(s), unit, p.want, transfer.Options{})
		return err
	})
	if err != nil {
		p.blocked = assistantError(err)
	}
	return p
}

// writes reports whether sending writes the area.
func (p *assistPlan) writes() bool { return p.blocked == "" && !p.dry.Unchanged }

// writeAssistants writes the planned area, with the progress in blocks.
func writeAssistants(ctx context.Context, s *session, p *assistPlan, progress func(done, total int)) (*transfer.Report, error) {
	opts := assistantWriteOptions
	opts.Confirm = true
	opts.Progress = func(e transfer.Event) {
		if e.Total > 0 {
			progress(e.Done, e.Total)
		}
	}
	var rep *transfer.Report
	var werr error
	err := s.Do(ctx, func(ctx context.Context, c *mk2.Client) error {
		rep, werr = transfer.Write(ctx, c, assistantCodec(s), p.unit, p.want, opts)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rep, werr
}

// stepLine is one step of an assistant report: an icon that says how it
// went, and the text.
func stepLine(icon fyne.Resource, text string) fyne.CanvasObject {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	return container.NewBorder(nil, nil, widget.NewIcon(icon), nil, l)
}

func okIcon() fyne.Resource    { return theme.NewSuccessThemedResource(theme.ConfirmIcon()) }
func errorIcon() fyne.Resource { return theme.NewErrorThemedResource(theme.ErrorIcon()) }
func infoIcon() fyne.Resource  { return theme.InfoIcon() }

// assistantsHeading is the heading of the assistant steps.
func assistantsHeading() fyne.CanvasObject {
	return widget.NewLabelWithStyle(tr("Assistants"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
}

// view shows the plan in the dialog that asks to send a file.
func (p *assistPlan) view() fyne.CanvasObject {
	box := container.NewVBox(assistantsHeading())
	switch {
	case p.blocked != "":
		box.Add(stepLine(errorIcon(), p.blocked))
	case p.dry.Unchanged:
		box.Add(stepLine(okIcon(), tr("The unit already holds the file's assistants; they are not written again.")))
	default:
		box.Add(stepLine(infoIcon(), tr("In the unit: {{.Area}}", args{"Area": areaSummary(p.dry.Snapshot.Image)})))
		box.Add(stepLine(infoIcon(), tr("In the file: {{.Area}}", args{"Area": areaSummary(p.want.Image)})))
		box.Add(callout(theme.NewWarningThemedResource(theme.WarningIcon()), tr("After the settings, the file's assistants replace the unit's and are read back. "+
			"Then every unit restarts, so loads lose power for a moment. If anything fails, the unit's assistants are written back.")))
	}
	return box
}

// assistantSteps reports what a send of a file did with its assistants:
// kept (p nil), not written because the settings failed, or the steps of
// the write and its error.
func assistantSteps(p *assistPlan, settingsOK bool, rep *transfer.Report, err error) fyne.CanvasObject {
	box := container.NewVBox(assistantsHeading())
	switch {
	case p == nil:
		box.Add(stepLine(infoIcon(), tr("The unit's assistants were kept; the file's assistant area was not written.")))
	case !p.writes():
		box.Add(stepLine(okIcon(), tr("The unit already holds the file's assistants; they are not written again.")))
	case !settingsOK:
		box.Add(stepLine(theme.NewWarningThemedResource(theme.WarningIcon()),
			tr("The file's assistants were not written, because not every setting was sent.")))
	default:
		if rep != nil && rep.Writes > 0 {
			icon := okIcon()
			if err != nil {
				icon = theme.NewWarningThemedResource(theme.WarningIcon())
			}
			box.Add(stepLine(icon, trn("{{.Count}} block written", "{{.Count}} blocks written", rep.Writes)))
		}
		if err != nil {
			box.Add(stepLine(errorIcon(), assistantError(err)))
			break
		}
		box.Add(stepLine(okIcon(), tr("Read back and verified")))
		if rep.Reset {
			box.Add(stepLine(okIcon(), tr("All units restarted, and the unit answered again")))
		}
		if rep.RunningKnown {
			box.Add(stepLine(okIcon(), tr("Running assistants: {{.IDs}}", args{"IDs": runningText(rep.Running)})))
		}
	}
	return box
}

// assistantError says what went wrong in a check or write of assistants,
// and what to do.
func assistantError(err error) string {
	var ue *uiError
	if errors.As(err, &ue) {
		return ue.msg
	}
	var te *transfer.Error
	if !errors.As(err, &te) {
		return tr("The assistants could not be checked or written: {{.Err}}", args{"Err": err})
	}
	cause := tr("The assistants could not be checked or written: {{.Err}}", args{"Err": te.Err})
	for _, c := range []struct {
		err  error
		text string
	}{
		{transfer.ErrHardwareGate, tr("Writing assistants to real devices is off until it has been checked on hardware.")},
		{transfer.ErrNotConfigured, tr("The unit is not configured for assistants, and how to switch them on is not known yet.")},
		{transfer.ErrOtherUnit, tr("The file's assistants were read from another unit.")},
		{transfer.ErrFirmware, tr("The file's assistants were read from a unit with other firmware.")},
		{transfer.ErrSize, tr("The file's assistant area does not have the size of the unit's.")},
		{transfer.ErrChecksum, tr("The checksum of the file's assistant area is wrong.")},
		{transfer.ErrMismatch, tr("The assistants read back differ from the ones written.")},
		{transfer.ErrRunning, tr("Other assistants run after the restart.")},
	} {
		if errors.Is(te.Err, c.err) {
			cause = c.text
			break
		}
	}
	return cause + " " + assistantGuidance(te.Kind)
}

// assistantGuidance is transfer.Error.Guidance in the UI language.
func assistantGuidance(k transfer.Kind) string {
	switch k {
	case transfer.KindGated:
		return tr("Writing assistants to devices is not available yet. Nothing was sent.")
	case transfer.KindRefused, transfer.KindSnapshot:
		return tr("Nothing was written.")
	case transfer.KindRolledBack:
		return tr("Writing the assistants failed. The previous assistants were written back and verified, and the unit restarted with them.")
	case transfer.KindRollbackFailed:
		return tr("Writing the assistants failed and the previous assistants could not be restored. The unit's state is unknown. " +
			"Restore the assistants with VEConfigure, or switch the unit off and on.")
	case transfer.KindNotBack:
		return tr("The unit did not answer after the restart. Wait a few minutes and read it again. " +
			"If its LEDs show an error, restore the assistants with VEConfigure.")
	}
	return ""
}
