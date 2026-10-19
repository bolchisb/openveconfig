package mk2

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// Timeouts recommended by §3.3.
const (
	// DefaultTimeout is the reply timeout for every command except 'F' 5.
	DefaultTimeout = 500 * time.Millisecond
	// DefaultSlowTimeout is the reply timeout for 'F' 5 (MasterMultiLED),
	// which can take up to 750 ms; §3.3 asks for a value greater than that.
	DefaultSlowTimeout = time.Second
)

// Client errors.
var (
	ErrTimeout      = errors.New("mk2: timeout waiting for reply")
	ErrClosed       = errors.New("mk2: client closed")
	ErrNotSupported = errors.New("mk2: not supported by device")
	ErrAccessLevel  = errors.New("mk2: access level required")
	ErrShortReply   = errors.New("mk2: reply too short")
)

// UnknownCommandError is the Winmon "Unknown command" reply 0x80 (§7.3).
type UnknownCommandError struct {
	// Reason bit 0: <Info0> not recognised; bit 1: <Info1> not recognised;
	// neither: <W command> not recognised.
	Reason byte
}

func (e *UnknownCommandError) Error() string {
	return fmt.Sprintf("mk2: device rejected command (reason 0x%02X)", e.Reason)
}

// Tunnel selects the MK2 command used to carry Winmon commands (§7.3).
type Tunnel int

const (
	// TunnelW uses 'W'. This is the default.
	TunnelW Tunnel = iota
	// TunnelXYZ rotates 'X', 'Y', 'Z' so a reply can be matched to its
	// request even if an earlier reply arrives late.
	TunnelXYZ
	// TunnelXYZAddressed rotates 'x', 'y', 'z' and appends the destination
	// address set with SetDestination to every frame.
	TunnelXYZAddressed
)

// WinmonMode is the Winmon frame mode of the MK2 (§7.3). It changes the
// reply format of CommandSendSoftwareVersion, CommandGetSettingInfo and
// CommandGetRAMVarInfo.
type WinmonMode int

const (
	// WinmonShort is mode 1 (short, legacy frames), the MK2 default.
	WinmonShort WinmonMode = iota
	// WinmonLong is mode 2 (long frames), selected with FlagsExt0 bit 0 = 0
	// in the 'S' command.
	WinmonLong
)

// WriteMethod selects how WriteSetting and WriteRAMVar write a value.
type WriteMethod int

const (
	// WriteMethodData sends CommandWriteSetting (0x33) or
	// CommandWriteRAMVar (0x32), then CommandWriteData (0x34) (§7.3.5-7).
	// This is the default.
	WriteMethodData WriteMethod = iota
	// WriteMethodViaID sends CommandWriteViaID (0x37, §7.3.11), which the
	// spec marks "Winmon mode 2 only" and which takes an 8-bit ID.
	// Community reports say 0x33/0x34 did not work for them while 0x37 did.
	WriteMethodViaID
)

// Options configures a Client. The zero value follows the spec.
type Options struct {
	// Timeout is the reply timeout (default DefaultTimeout). It restarts
	// whenever a frame of a multi-frame reply arrives.
	Timeout time.Duration
	// SlowTimeout is the reply timeout for 'F' 5 (default DefaultSlowTimeout).
	SlowTimeout time.Duration
	// Retries is how many times a request is repeated after a timeout. The
	// spec defines no retry policy, so the default is 0.
	Retries int
	// Tunnel selects 'W' or the tagged 'X/Y/Z' / 'x/y/z' commands.
	Tunnel Tunnel
	// WinmonMode is the MK2's initial Winmon mode. SetState updates it when
	// it sends FlagsExt0.
	WinmonMode WinmonMode
	// WriteMethod selects the setting/RAM write commands.
	WriteMethod WriteMethod
	// SettingInfoCommand is the Winmon command used for GetSettingInfo.
	// 0 means the spec's CommandGetSettingInfo (0x35). Community traces of
	// VEConfigure show 0x3C instead; its reply is parsed the same way.
	SettingInfoCommand byte
	// IgnoreMissingWriteAck makes WriteSetting, WriteRAMVar and WriteViaID
	// return nil instead of ErrTimeout when no acknowledgement arrives.
	// Community reports say acks for high setting IDs are often missing.
	// Read the value back to confirm the write.
	IgnoreMissingWriteAck bool
	// Unsolicited, if set, receives every frame that is not a reply to the
	// current request, such as the periodic 'V' frames (§3.4) or forwarded
	// panel frames (§5.1). It runs on the goroutine of the request in
	// progress and must not call Client methods.
	Unsolicited func(Frame)
}

// Client talks to an MK2/MK3 over an io.ReadWriter. Its methods are safe
// for concurrent use; requests are serialised, so a 'W' or 'A' command is
// never sent before the previous reply arrived (§7.1).
type Client struct {
	rw     io.ReadWriter
	opts   Options
	frames chan Frame // closed by readLoop when the transport fails

	closing   chan struct{}
	closeOnce sync.Once
	readErr   error // set before frames is closed

	mu   sync.Mutex // held for each request
	mode WinmonMode
	dest byte
	tag  int
}

// NewClient starts a Client on rw. A background goroutine reads rw until it
// returns an error or the Client is closed. opts may be nil.
func NewClient(rw io.ReadWriter, opts *Options) *Client {
	c := &Client{
		rw:      rw,
		frames:  make(chan Frame, 16),
		closing: make(chan struct{}),
	}
	if opts != nil {
		c.opts = *opts
	}
	if c.opts.Timeout <= 0 {
		c.opts.Timeout = DefaultTimeout
	}
	if c.opts.SlowTimeout <= 0 {
		c.opts.SlowTimeout = DefaultSlowTimeout
	}
	if c.opts.SettingInfoCommand == 0 {
		c.opts.SettingInfoCommand = WCmdGetSettingInfo
	}
	c.mode = c.opts.WinmonMode
	go c.readLoop()
	return c
}

// Close stops the Client. If the transport implements io.Closer it is
// closed too, which unblocks the reader goroutine.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.closing)
		if cl, ok := c.rw.(io.Closer); ok {
			err = cl.Close()
		}
	})
	return err
}

// SetDestination sets the address appended to 'x/y/z' frames when Tunnel is
// TunnelXYZAddressed (§7.3).
func (c *Client) SetDestination(addr byte) {
	c.mu.Lock()
	c.dest = addr
	c.mu.Unlock()
}

func (c *Client) readLoop() {
	sc := bufio.NewScanner(c.rw)
	sc.Split(ScanFrames)
	for sc.Scan() {
		f, err := Decode(sc.Bytes())
		if err != nil {
			continue
		}
		// Never block the reader: if nobody is waiting and the buffer is
		// full, drop the oldest frame.
		for sent := false; !sent; {
			select {
			case c.frames <- f:
				sent = true
			default:
				select {
				case <-c.frames:
				default:
				}
			}
		}
	}
	c.readErr = sc.Err()
	if c.readErr == nil {
		c.readErr = io.EOF
	}
	close(c.frames)
}

// handler inspects a received frame. mine reports whether the frame belongs
// to the current request; done reports whether the reply is complete.
type handler func(f Frame) (mine, done bool, err error)

// exchange sends req (if non-nil) and feeds received frames to h until h
// reports done. c.mu must be held. Frames that were already queued before
// req was sent are stale and go to the Unsolicited callback.
func (c *Client) exchange(ctx context.Context, req *Frame, timeout time.Duration, h handler) error {
	if err := c.drain(); err != nil {
		return err
	}
	if req != nil {
		b, err := req.Encode()
		if err != nil {
			return err
		}
		if _, err := c.rw.Write(b); err != nil {
			return fmt.Errorf("mk2: write: %w", err)
		}
	}
	if h == nil {
		return nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case f, ok := <-c.frames:
			if !ok {
				return c.closedErr()
			}
			mine, done, err := h(f)
			if !mine {
				c.unsolicited(f)
				continue
			}
			if err != nil || done {
				return err
			}
			timer.Reset(timeout)
		case <-timer.C:
			return ErrTimeout
		case <-ctx.Done():
			return ctx.Err()
		case <-c.closing:
			return ErrClosed
		}
	}
}

func (c *Client) drain() error {
	for {
		select {
		case f, ok := <-c.frames:
			if !ok {
				return c.closedErr()
			}
			c.unsolicited(f)
		case <-c.closing:
			return ErrClosed
		default:
			return nil
		}
	}
}

func (c *Client) closedErr() error {
	select {
	case <-c.closing:
		return ErrClosed
	default:
	}
	return fmt.Errorf("mk2: read: %w", c.readErr)
}

func (c *Client) unsolicited(f Frame) {
	if c.opts.Unsolicited != nil {
		c.opts.Unsolicited(f)
	}
}

// do runs op with c.mu held, repeating it up to Options.Retries times while
// it times out.
func (c *Client) do(op func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	err := op()
	for i := 0; i < c.opts.Retries && errors.Is(err, ErrTimeout); i++ {
		err = op()
	}
	return err
}
