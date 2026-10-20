package simulator

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/bolchisb/openveconfig/pkg/assistant"
	"github.com/bolchisb/openveconfig/pkg/mk2"
)

// Assistant areas (#79).
//
// How a real unit's assistant area is read and written over the MK is not
// known (spec #77 T2.4, T2.5: UNKNOWN). The simulator therefore speaks its
// own block codec, BlockCodec, with Winmon command and reply codes and a
// block size that were made up for the simulator. They are not the
// device's protocol: never send them to a real device.

// The requests and replies of BlockCodec. Made up for the simulator. Every
// one carries the block number, little-endian, after its code.
const (
	WCmdBlockRead      = 0xE0 // + block number
	WCmdBlockWrite     = 0xE1 // + block number + BlockSize bytes
	WReplyBlock        = 0xE2 // + block number + BlockSize bytes
	WReplyBlockWritten = 0xE3 // + block number
	WReplyBlockRange   = 0xE4 // + block number: outside the area
	WReplyBlockRefused = 0xE5 // + block number: write refused
)

// BlockSize is the block size of BlockCodec, a simulator choice.
const BlockSize = 64

// ramAssistants is the first RAM ID of the assistant RAM records (public
// document: Victron, "ESS mode 2 and 3" §3.6.2; spec #77 T7.4).
const ramAssistants = 128

// AssistantMemory gives a simulated device an assistant area, which
// BlockCodec reads and writes. All contents are synthetic.
type AssistantMemory struct {
	// Image is the area at start; nil means ErasedArea(). Its length must
	// be a non-zero multiple of BlockSize; blocks after it are answered
	// WReplyBlockRange, which is how a reader finds the end.
	Image []byte
	// RAM are the words of the assistant RAM records from RAM ID 128: a
	// record starts with a word 0xZZZY, ZZZ the assistant ID and Y the
	// number of words that follow (public document: "ESS mode 2 and 3"
	// §3.6.2). nil: RAM ID 128 is not supported (0x90). The simulator does
	// not run programs, so writing the area does not change them (a
	// simulator choice).
	RAM []uint16
	// RefuseWrites: every block write is answered WReplyBlockRefused and
	// changes nothing.
	RefuseWrites bool
	// CorruptWrites: the next CorruptWrites block writes are stored with
	// their first byte inverted, so the read-back differs.
	CorruptWrites int
}

// AssistantState is a snapshot of a device's assistant area.
type AssistantState struct {
	Image []byte
	// Writes counts the block write requests received, refused ones
	// included.
	Writes int
}

// area is the live assistant area of a device.
type area struct {
	mem    AssistantMemory
	img    []byte
	writes int
}

func newArea(m AssistantMemory) *area {
	a := &area{mem: m, img: bytes.Clone(m.Image)}
	if a.img == nil {
		a.img = ErasedArea()
	}
	if len(a.img) == 0 || len(a.img)%BlockSize != 0 {
		panic(fmt.Sprintf("simulator: assistant area of %d bytes is not a multiple of %d", len(a.img), BlockSize))
	}
	if len(m.RAM) > 0x100-ramAssistants {
		panic("simulator: too many assistant RAM words")
	}
	return a
}

// addAssistantRAM adds the assistant RAM records of a to the RAM
// variables of d.
func (d *device) addAssistantRAM(a *area) {
	for i, w := range a.mem.RAM {
		id := uint16(ramAssistants + i)
		d.ramInfo[id] = RAMVar{ID: id, Name: fmt.Sprintf("Assistant%d", i), Sc: 1}
		d.ram[id] = w
	}
}

// block answers a BlockCodec request (info without padding) for the area
// a. nil means "unknown command": the device has no assistant area.
func (a *area) block(cmd byte, info []byte) []byte {
	if a == nil || len(info) < 2 {
		return nil
	}
	off := int(binary.LittleEndian.Uint16(info)) * BlockSize
	reply := func(code byte, data ...byte) []byte { return append([]byte{code, info[0], info[1]}, data...) }
	if cmd == WCmdBlockWrite {
		a.writes++
	}
	if off+BlockSize > len(a.img) {
		return reply(WReplyBlockRange)
	}
	if cmd == WCmdBlockRead {
		return reply(WReplyBlock, a.img[off:off+BlockSize]...)
	}
	data := info[2:]
	if a.mem.RefuseWrites || len(data) != BlockSize {
		return reply(WReplyBlockRefused)
	}
	copy(a.img[off:], data)
	if a.mem.CorruptWrites > 0 {
		a.mem.CorruptWrites--
		a.img[off] ^= 0xFF
	}
	return reply(WReplyBlockWritten)
}

// Assistants returns the assistant area of the first device at addr that
// has one, and whether there is one.
func (s *Simulator) Assistants(addr byte) (AssistantState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.devices {
		if d.addr == addr && d.area != nil {
			return AssistantState{Image: bytes.Clone(d.area.img), Writes: d.area.writes}, true
		}
	}
	return AssistantState{}, false
}

// BlockCodec is the simulator's own mk2.BlockCodec (see WCmdBlockRead). It
// reports Hardware false. Use it only with a Simulator.
type BlockCodec struct{}

func (BlockCodec) Hardware() bool { return false }
func (BlockCodec) BlockSize() int { return BlockSize }

func (BlockCodec) ReadRequest(n int) (byte, []byte, error) {
	if n < 0 || n > 0xFFFF {
		return 0, nil, mk2.ErrBlockRange
	}
	return WCmdBlockRead, binary.LittleEndian.AppendUint16(nil, uint16(n)), nil
}

func (BlockCodec) WriteRequest(n int, data []byte) (byte, []byte, error) {
	if n < 0 || n > 0xFFFF {
		return 0, nil, mk2.ErrBlockRange
	}
	if len(data) != BlockSize {
		return 0, nil, fmt.Errorf("simulator: block of %d bytes, want %d", len(data), BlockSize)
	}
	return WCmdBlockWrite, append(binary.LittleEndian.AppendUint16(nil, uint16(n)), data...), nil
}

// about reports whether the reply p is about block n.
func about(n int, p []byte) bool { return len(p) >= 3 && int(binary.LittleEndian.Uint16(p[1:])) == n }

func (BlockCodec) ParseRead(n int, p []byte) ([]byte, bool, error) {
	switch {
	case !about(n, p):
		return nil, false, nil
	case p[0] == WReplyBlockRange:
		return nil, true, mk2.ErrBlockRange
	case p[0] != WReplyBlock:
		return nil, false, nil
	case len(p) < 3+BlockSize:
		return nil, true, mk2.ErrShortReply
	}
	return bytes.Clone(p[3 : 3+BlockSize]), true, nil
}

func (BlockCodec) ParseWrite(n int, p []byte) (bool, error) {
	if !about(n, p) {
		return false, nil
	}
	switch p[0] {
	case WReplyBlockWritten:
		return true, nil
	case WReplyBlockRefused:
		return true, mk2.ErrBlockRefused
	case WReplyBlockRange:
		return true, mk2.ErrBlockRange
	}
	return false, nil
}

// Synthetic area contents, in the shapes the spec describes (#77 T1.2,
// T3.3, T3.4). Their bytes are made up; only the layout and the checksum
// (assistant.Checksum) follow the spec.

// ErasedArea returns assistant.ImageSize bytes of 0xFF: an erased area,
// the "bare" shape of a unit without assistants. Its checksum is
// 0xFFFFFFFF, which is what its last 4 bytes hold.
func ErasedArea() []byte { return bytes.Repeat([]byte{0xFF}, assistant.ImageSize) }

// HeaderArea returns the other shapes of a unit without assistants: n
// header bytes (the spec names a 6-byte container and a 64-byte stub),
// then 0xFF, with the checksum in the last 4 bytes. The header bytes are
// made up.
func HeaderArea(n int) []byte {
	img := ErasedArea()
	for i := range n {
		img[i] = byte(0x30 + i)
	}
	return withChecksum(img)
}

// ProgramArea returns the shape of an area with assistants: n program
// bytes from offset 0, 0xFF free space, a 6-byte tail that ends where the
// checksum starts, and the checksum. All bytes but the checksum are made
// up.
func ProgramArea(n int) []byte {
	img := ErasedArea()
	for i := range n {
		img[i] = byte(i*7 + 3)
	}
	copy(img[len(img)-10:], []byte{0x21, 0x22, 0x23, 0x24, 0x25, 0x26})
	return withChecksum(img)
}

func withChecksum(img []byte) []byte {
	binary.LittleEndian.PutUint32(img[len(img)-4:], assistant.Checksum(img))
	return img
}
