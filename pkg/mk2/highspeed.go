package mk2

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Line speeds.
const (
	// BaudRate is the line speed of the MK2 protocol (§3.1, 8N1).
	BaudRate = 2400
	// HighSpeedBaudRate is the MK3 high-speed rate. COMMUNITY: not in the
	// MK2 protocol document; see NegotiateBaud.
	HighSpeedBaudRate = 115200
)

// CmdBaudRate is the MK3 baud-rate change request 'N'. COMMUNITY: not in the
// MK2 protocol document; see NegotiateBaud.
const CmdBaudRate = 'N'

// autobaud is the sync string sent after a baud-rate change. COMMUNITY.
var autobaud = []byte("UUUUU")

// ErrNoLink means the interface did not answer 'V' at 2400 baud, even after
// the recovery steps of NegotiateBaud.
var ErrNoLink = errors.New("mk2: no reply at 2400 baud")

// BaudRateSetter changes the line speed of the serial port under a Client.
// serialport.Port implements it.
type BaudRateSetter interface {
	SetBaudRate(baud int) error
}

// HighSpeed selects whether NegotiateBaud tries the MK3 high-speed rate.
type HighSpeed int

const (
	// HighSpeedAuto tries 115200 baud and falls back to 2400.
	HighSpeedAuto HighSpeed = iota
	// HighSpeedOff never sends 'N' and leaves the link at 2400 baud.
	HighSpeedOff
)

// highSpeedTiming holds the delays of the switch. Tests shorten them.
var highSpeedTiming = struct {
	beforeN, afterN, afterSync, afterReset time.Duration
}{
	beforeN:    100 * time.Millisecond,
	afterN:     50 * time.Millisecond,
	afterSync:  100 * time.Millisecond,
	afterReset: time.Second,
}

// verifyAttempts is how many 'V' requests verifyLink sends.
const verifyAttempts = 3

// NegotiateBaud sets up the line speed of a newly opened link and returns the
// rate in use. port must be the serial port under c. Call it before any other
// request; it holds the Client for its whole run (several seconds when it has
// to fall back).
//
// It first makes sure the interface answers 'V' at 2400 baud (restoreLowSpeed
// below). With HighSpeedOff it stops there, so that option both disables the
// switch and forces 2400. With HighSpeedAuto it then tries the switch; if the
// link does not answer 'V' at 115200 it restores 2400 and returns 2400 with a
// nil error. The error is non-nil only when no working 2400 link could be
// restored, or ctx ended; the port is then left at 2400.
//
// The switch is not in Victron's MK2 protocol document, which gives only
// 2400 baud. Two independent public sources describe it:
//
//   - yvesf/ve-ctrl-tool, pkg/mk2/mk2io.go, UpgradeHighSpeed (MIT,
//     https://github.com/yvesf/ve-ctrl-tool): wait 100 ms, write
//     02 FF 4E B1 ('N'), wait 50 ms, set the port to 115200, write "UUUUU",
//     wait 100 ms. It does not wait for a reply. Its command-line tools
//     first send 'R' at 115200, wait 1 s and go back to 2400 ("reset both in
//     high and low speed"), and offer a -low flag to skip the switch. The
//     sequence and delays here are adapted from that code.
//   - xcellsior/ve-bus-programming, FINDINGS.md §12.9 (MIT, a static read of
//     VictronConnect): "When the interface is an MK3, VictronConnect sends
//     02 FF 4E", switches to 115200, sends a frame starting with 0x55 bytes,
//     and logs success "when an N reply comes back"; otherwise it reverts to
//     2400. The rest of the handshake was not decoded.
//
// UNKNOWN: the format and baud rate of the 'N' reply, whether VEConfigure
// uses 'N' at all, and how VictronConnect tells an MK2 from an MK3. So this
// code does not wait for an 'N' reply (it goes to the Unsolicited callback if
// it comes); the link counts as switched only when 'V' is answered at
// 115200.
//
// MK2/MK3 detection: no public source gives a rule, so NegotiateBaud does not
// detect. It tries the switch on any interface that answers 'V' at 2400 and
// relies on the fallback. What an MK2 does with 'N' is undocumented; it is
// expected to ignore it. A caller that knows the interface is an MK2 (for
// example an RS232 MK2.2) should pass HighSpeedOff.
func (c *Client) NegotiateBaud(ctx context.Context, port BaudRateSetter, mode HighSpeed) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.restoreLowSpeed(ctx, port); err != nil {
		return BaudRate, err
	}
	if mode == HighSpeedOff {
		return BaudRate, nil
	}
	if err := c.switchHighSpeed(ctx, port); err == nil {
		return HighSpeedBaudRate, nil
	}
	return BaudRate, c.restoreLowSpeed(ctx, port)
}

// switchHighSpeed sends 'N', moves the port to 115200 and checks the link.
// The port is left at 115200 even on failure. c.mu must be held.
func (c *Client) switchHighSpeed(ctx context.Context, port BaudRateSetter) error {
	if err := c.sleep(ctx, highSpeedTiming.beforeN); err != nil {
		return err
	}
	if err := c.exchange(ctx, &Frame{Command: CmdBaudRate}, 0, nil); err != nil {
		return err
	}
	if err := c.sleep(ctx, highSpeedTiming.afterN); err != nil {
		return err
	}
	if err := c.resync(ctx, port, HighSpeedBaudRate); err != nil {
		return err
	}
	return c.verifyLink(ctx)
}

// restoreLowSpeed brings the link to a working 2400 baud. It sets the port to
// 2400, sends the autobaud string (COMMUNITY, xcellsior FINDINGS §3
// "Optional: Sync Sequence": VEConfigure sends 5x 0x55, which "brings a
// silent MK3 back after an interrupted session") and checks 'V'. If nothing
// answers, the MK3 may still be at 115200, so, like ve-ctrl-tool, it sends
// 'R' (§3.6) at 115200, waits, and checks 'V' at 2400 again. c.mu must be
// held.
func (c *Client) restoreLowSpeed(ctx context.Context, port BaudRateSetter) error {
	if err := c.resync(ctx, port, BaudRate); err != nil {
		return err
	}
	if err := c.verifyLink(ctx); !errors.Is(err, ErrTimeout) {
		return err
	}
	if err := port.SetBaudRate(HighSpeedBaudRate); err != nil {
		return c.lowSpeedAfter(port, fmt.Errorf("mk2: %w", err))
	}
	err := c.exchange(ctx, &Frame{Command: CmdReset}, 0, nil)
	if err == nil {
		// Let 'R' leave the UART at 115200 and the MK3 restart.
		err = c.sleep(ctx, highSpeedTiming.afterReset)
	}
	if err != nil {
		return c.lowSpeedAfter(port, err)
	}
	if err := port.SetBaudRate(BaudRate); err != nil {
		return fmt.Errorf("mk2: %w", err)
	}
	c.discardReceived()
	if err := c.verifyLink(ctx); err != nil {
		if errors.Is(err, ErrTimeout) {
			return ErrNoLink
		}
		return err
	}
	return nil
}

// lowSpeedAfter puts the port back to 2400 and returns err.
func (c *Client) lowSpeedAfter(port BaudRateSetter, err error) error {
	if e := port.SetBaudRate(BaudRate); e != nil {
		return fmt.Errorf("%w (and mk2: %w)", err, e)
	}
	return err
}

// resync sets the port to baud, sends the autobaud string, waits, and drops
// everything received so far, which may be garbage from the old rate.
func (c *Client) resync(ctx context.Context, port BaudRateSetter, baud int) error {
	if err := port.SetBaudRate(baud); err != nil {
		return fmt.Errorf("mk2: %w", err)
	}
	if _, err := c.rw.Write(autobaud); err != nil {
		return fmt.Errorf("mk2: write: %w", err)
	}
	if err := c.sleep(ctx, highSpeedTiming.afterSync); err != nil {
		return err
	}
	c.discardReceived()
	return nil
}

// verifyLink sends 'V' until a version frame arrives, up to verifyAttempts
// times. c.mu must be held.
func (c *Client) verifyLink(ctx context.Context) error {
	var err error
	for range verifyAttempts {
		err = c.exchange(ctx, &Frame{Command: CmdVersion}, c.opts.Timeout, func(f Frame) (bool, bool, error) {
			_, perr := ParseVersion(f)
			return perr == nil, perr == nil, nil
		})
		if !errors.Is(err, ErrTimeout) {
			return err
		}
	}
	return err
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closing:
		return ErrClosed
	}
}
