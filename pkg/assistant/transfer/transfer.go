// Package transfer reads a unit's whole assistant area over an MK2/MK3
// and writes one back with verification and rollback (#80). The blocks
// go through an mk2.BlockCodec (#79). For real devices that is
// mk2.DeviceBlockCodec, which sends nothing until the block requests are
// specified (#77), and writing to real devices is also off
// (WriteOnHardware); the simulator's codec works fully.
//
// The behaviour follows the project's spec of the transfer (#77, written
// from public documents and community sources; section labels such as T3.2
// are cited where used). Most of it is that spec's design for the port
// (INFERRED), not knowledge of the original tools:
//
//   - The area is read whole, never stopping at the first erased block:
//     the tail and the checksum sit at its end, after the free space
//     (T1.4, T3.5). Its size is assistant.ImageSize for the firmware
//     families it was seen with (AreaSize, T1.1, T3.2); for other units
//     the end is found by reading until the unit answers that a block is
//     outside the area.
//   - A write is refused before anything is sent unless the image's
//     checksum is right, it was read from the same unit (long ID) with the
//     same firmware, it has the size of the unit's area, and the unit's
//     system record says that it is configured for assistants (byte 7 bit
//     4 clear; observed on our unit and community-reported, T5.2, T5.3).
//     Changing that flag, or any setting, to enable or clear assistants is
//     not done: which item enables assistants is UNKNOWN (T5.5, T5.8).
//     TODO(#77): add the enable step once the hardware checks H1 and H3
//     have settled it.
//   - Write (see Write) takes a snapshot, skips a unit that already holds
//     the image, writes the blocks in ascending order so that the block
//     with the checksum comes last, retries a failed block once, reads the
//     area back and compares, restarts the units and checks the running
//     assistants (RAM records from ID 128); on any failure it writes the
//     snapshot back the same way (T4.6, T4.7, T7.6, T8.3).
package transfer

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/bolchisb/openveconfig/pkg/assistant"
	"github.com/bolchisb/openveconfig/pkg/mk2"
)

// knownFamilies are the firmware families (the first four digits of the
// firmware number) whose area was seen at assistant.ImageSize bytes: our
// MultiPlus-II, firmware 2627560 (observed), and community MultiPlus
// files, firmware 2729560 (community-reported, kylehart/mk2vsc FORMAT.md
// §5). Spec #77 T1.1.
var knownFamilies = []uint32{2627, 2729}

// maxArea bounds a read that looks for the end of an area of unknown size
// (our guard).
const maxArea = 0x10000

// ramAssistants is the RAM ID of the first assistant RAM record (public
// document: Victron, "ESS mode 2 and 3" §3.6.2; spec #77 T7.4).
const ramAssistants = 128

// AreaSize returns the size of the assistant area of a unit with this
// firmware number, if its family was seen with one (spec #77 T3.2, design).
// Whether other products and firmware have the same size is UNKNOWN (T1.7).
func AreaSize(firmware uint32) (int, bool) {
	if slices.Contains(knownFamilies, firmware/1000) {
		return assistant.ImageSize, true
	}
	return 0, false
}

// Unit is the unit whose area is read or written.
type Unit struct {
	// Address is the unit's VE.Bus address, for the Winmon requests.
	Address byte
	// LongID identifies the unit. Write needs it, to read the unit's
	// system record and to check that the area is the unit's own.
	LongID uint32
}

// Area is a unit's assistant area as read from it (Read), or as a front
// end loads it from a backup.
type Area struct {
	// LongID and Firmware identify the unit and firmware the area was
	// read from.
	LongID   uint32
	Firmware uint32
	Image    []byte
	// ChecksumOK reports whether the image's last 4 bytes are its checksum
	// (assistant.ChecksumOK). A unit whose earlier write was interrupted
	// shows a wrong checksum (spec #77 T8.3).
	ChecksumOK bool
	// Running are the assistant IDs of the unit's assistant RAM records
	// when the area was read (ReadRunning), if RunningKnown.
	Running      []uint16
	RunningKnown bool
}

// Read reads the whole assistant area of the unit u with codec, and the
// assistant RAM records. It reads the firmware version first: for a
// family of AreaSize it reads exactly that many bytes, also when blocks
// are erased; otherwise it reads until the unit answers mk2.ErrBlockRange.
// It fails with mk2.ErrBlockTransferUnspecified when the codec does not
// know the block size, before anything is sent.
func Read(ctx context.Context, c *mk2.Client, codec mk2.BlockCodec, u Unit) (*Area, error) {
	bs := codec.BlockSize()
	if bs <= 0 {
		return nil, fmt.Errorf("transfer: %w", mk2.ErrBlockTransferUnspecified)
	}
	if err := c.SetAddress(ctx, u.Address); err != nil {
		return nil, fmt.Errorf("transfer: select address %d: %w", u.Address, err)
	}
	fw, err := c.SoftwareVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("transfer: firmware version: %w", err)
	}
	size, known := AreaSize(fw)
	if known && size%bs != 0 {
		return nil, fmt.Errorf("transfer: area of %d bytes is not a whole number of %d-byte blocks", size, bs)
	}
	var img []byte
	for n := 0; !known || len(img) < size; n++ {
		if len(img) >= maxArea {
			return nil, fmt.Errorf("transfer: no end of the area found after %d bytes", len(img))
		}
		var b []byte
		err := try(func() (err error) {
			b, err = c.ReadBlock(ctx, codec, n)
			return err
		})
		if !known && n > 0 && errors.Is(err, mk2.ErrBlockRange) {
			break
		}
		if err == nil && len(b) != bs {
			err = fmt.Errorf("%d bytes, want %d", len(b), bs)
		}
		if err != nil {
			return nil, fmt.Errorf("transfer: read block %d: %w", n, err)
		}
		img = append(img, b...)
	}
	running, err := ReadRunning(ctx, c)
	if err != nil {
		return nil, err
	}
	return &Area{
		LongID: u.LongID, Firmware: fw, Image: img, ChecksumOK: assistant.ChecksumOK(img),
		Running: running, RunningKnown: true,
	}, nil
}

// ReadRunning returns the assistant IDs of the assistant RAM records of
// the selected device: from RAM ID 128, each record starts with a word
// 0xZZZY, ZZZ the assistant ID (5 is ESS) and Y the number of words that
// follow (public document: "ESS mode 2 and 3" §3.6.2). The walk stops at a
// 0x0000 word (community-reported: yvesf/ve-ctrl-tool mk2ess.go), when RAM
// ID 128 is not supported (no assistants, spec #77 H2), or after RAM ID
// 255, the last that CommandReadRAMVar can address (§7.3.3).
func ReadRunning(ctx context.Context, c *mk2.Client) ([]uint16, error) {
	ids := []uint16{}
	for id := ramAssistants; id <= 0xFF; {
		v, err := c.ReadRAMVar(ctx, byte(id))
		if errors.Is(err, mk2.ErrNotSupported) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("transfer: read assistant RAM record %d: %w", id, err)
		}
		if v[0] == 0 {
			break
		}
		ids = append(ids, v[0]>>4)
		id += 1 + int(v[0]&0xF)
	}
	return ids, nil
}

// try runs op, and once more when it fails with an error of the block
// itself (no reply, a refusal, a short reply), not of the link or the
// context: the single retry of spec #77 T4.6.
func try(op func() error) error {
	err := op()
	if errors.Is(err, mk2.ErrTimeout) || errors.Is(err, mk2.ErrBlockRefused) || errors.Is(err, mk2.ErrShortReply) {
		err = op()
	}
	return err
}
