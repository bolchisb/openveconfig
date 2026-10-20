package mk2

import (
	"context"
	"errors"
)

// Assistant area blocks (#79).
//
// A unit stores its assistants in an assistant area that a tool reads and
// writes block by block. The MK2 protocol document does not describe this
// (spec #77 T2.1, public document). The requests that read and write a
// block, the block size and the reply layouts are not known (spec #77
// T2.4, T2.5: UNKNOWN), so the transfer goes through a BlockCodec. The only
// codec for real devices, DeviceBlockCodec, refuses every transfer until a
// source specifies it; the simulator has its own made-up codec
// (simulator.BlockCodec).

// WCmdVLLBlockRead is the Winmon command community reports name "VLL block
// read", with a 2-byte argument, sent by VictronConnect (community-reported:
// xcellsior/ve-bus-programming FINDINGS.md §12.1; spec #77 T2.2). That it
// reads one block of the assistant area, and what the argument means, is
// inferred and not confirmed (spec #77 T2.3). Its reply is not decoded, so
// it is only sent by ProbeVLLBlockRead.
const WCmdVLLBlockRead = 0x5F

// Block transfer errors.
var (
	// ErrBlockTransferUnspecified means the requests that read or write
	// assistant blocks on a real device are not specified yet (#77), so
	// nothing was sent.
	ErrBlockTransferUnspecified = errors.New("mk2: assistant block transfer is not specified yet (#77); nothing was sent")
	// ErrBlockRange means the device answered that the block is outside
	// its assistant area.
	ErrBlockRange = errors.New("mk2: block outside the assistant area")
	// ErrBlockRefused means the device refused to write the block.
	ErrBlockRefused = errors.New("mk2: device refused the block write")
)

// BlockCodec turns reads and writes of one block of a unit's assistant
// area into Winmon requests (§7.3), and their replies back. Blocks are
// numbered from 0; block n holds the bytes from n × BlockSize.
type BlockCodec interface {
	// Hardware reports whether the codec is meant for real devices. Front
	// ends gate writes through such a codec (#80).
	Hardware() bool
	// BlockSize is the number of bytes in a block; 0 when not known.
	BlockSize() int
	// ReadRequest returns the Winmon command and info bytes that read
	// block n.
	ReadRequest(n int) (cmd byte, info []byte, err error)
	// ParseRead inspects a reply payload, from the reply code on. mine
	// reports whether it is the reply to the read of block n; then data
	// is the block, or err why there is none (ErrBlockRange, …).
	ParseRead(n int, p []byte) (data []byte, mine bool, err error)
	// WriteRequest returns the Winmon command and info bytes that write
	// data, BlockSize bytes, to block n.
	WriteRequest(n int, data []byte) (cmd byte, info []byte, err error)
	// ParseWrite inspects a reply payload as ParseRead does; err is nil
	// when the device acknowledged the write (ErrBlockRefused,
	// ErrBlockRange otherwise).
	ParseWrite(n int, p []byte) (mine bool, err error)
}

// DeviceBlockCodec is the codec for real devices. No allowed source names
// the block requests yet (spec #77 T2.4, T2.5), so it has no block size
// and every request returns ErrBlockTransferUnspecified: nothing is ever
// sent. The read half can be filled in once the hardware checks of the
// read probe (ProbeVLLBlockRead, #3) have decoded the reply; the write half
// waits for a source that names the write request, and then needs its own
// reply timeout, longer than the Winmon one (spec #77 T4.8, UNKNOWN).
type DeviceBlockCodec struct{}

func (DeviceBlockCodec) Hardware() bool { return true }
func (DeviceBlockCodec) BlockSize() int { return 0 }

func (DeviceBlockCodec) ReadRequest(int) (byte, []byte, error) {
	return 0, nil, ErrBlockTransferUnspecified
}

func (DeviceBlockCodec) ParseRead(int, []byte) ([]byte, bool, error) {
	return nil, false, ErrBlockTransferUnspecified
}

func (DeviceBlockCodec) WriteRequest(int, []byte) (byte, []byte, error) {
	return 0, nil, ErrBlockTransferUnspecified
}

func (DeviceBlockCodec) ParseWrite(int, []byte) (bool, error) {
	return false, ErrBlockTransferUnspecified
}

// ReadBlock reads block n of the target device's assistant area with
// codec. An "Unknown command" reply returns *UnknownCommandError: the
// device does not know the request (or has no assistant area).
// Options.Retries applies.
func (c *Client) ReadBlock(ctx context.Context, codec BlockCodec, n int) ([]byte, error) {
	cmd, info, err := codec.ReadRequest(n)
	if err != nil {
		return nil, err
	}
	var data []byte
	err = c.do(func() error {
		return c.winmon(ctx, cmd, info, func(p []byte) (bool, bool, error) {
			d, mine, err := codec.ParseRead(n, p)
			if !mine {
				return false, false, nil
			}
			data = d
			return true, true, err
		})
	})
	return data, err
}

// WriteBlock writes data to block n of the target device's assistant area
// with codec. Errors are as for ReadBlock, plus ErrBlockRefused. A timeout
// does not say whether the block was written: read it back.
func (c *Client) WriteBlock(ctx context.Context, codec BlockCodec, n int, data []byte) error {
	cmd, info, err := codec.WriteRequest(n, data)
	if err != nil {
		return err
	}
	return c.do(func() error {
		return c.winmon(ctx, cmd, info, func(p []byte) (bool, bool, error) {
			mine, err := codec.ParseWrite(n, p)
			return mine, mine, err
		})
	})
}

// ProbeVLLBlockRead sends WCmdVLLBlockRead with the 2-byte argument arg
// (little-endian, inferred) and returns the payload of the first reply,
// from the reply code on. It is read-only and only for the hardware check
// that decodes the reply (#3): the reply's layout, the block size and the
// meaning of arg are UNKNOWN (spec #77 T2.5). An "Unknown command" reply
// returns *UnknownCommandError: the device or firmware does not support
// the command.
func (c *Client) ProbeVLLBlockRead(ctx context.Context, arg uint16) ([]byte, error) {
	return c.Winmon(ctx, WCmdVLLBlockRead, le16(arg)...)
}
