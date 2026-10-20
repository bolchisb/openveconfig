package simulator

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/bolchisb/openveconfig/pkg/assistant"
	"github.com/bolchisb/openveconfig/pkg/mk2"
)

func assistantBus(mem *AssistantMemory) []Device {
	return []Device{
		{Address: 0, Profile: Synthetic12V230(), Assistants: mem},
		{Address: 1, Profile: Synthetic48V120()},
	}
}

func TestAssistantAreaReadToTheEnd(t *testing.T) {
	ctx := context.Background()
	_, c := start(t, Config{Devices: assistantBus(&AssistantMemory{})}, nil)
	check(t, "SetAddress", c.SetAddress(ctx, 0))
	var img []byte
	for n := 0; ; n++ {
		b, err := c.ReadBlock(ctx, BlockCodec{}, n)
		if errors.Is(err, mk2.ErrBlockRange) {
			break
		}
		check(t, "ReadBlock", err)
		img = append(img, b...)
	}
	if !bytes.Equal(img, ErasedArea()) || !assistant.ChecksumOK(img) {
		t.Fatalf("read %d bytes, want the erased area", len(img))
	}

	// A device without an assistant area does not know the request.
	check(t, "SetAddress", c.SetAddress(ctx, 1))
	var uc *mk2.UnknownCommandError
	if _, err := c.ReadBlock(ctx, BlockCodec{}, 0); !errors.As(err, &uc) {
		t.Fatalf("no area: %v", err)
	}
}

func TestAssistantAreaWrite(t *testing.T) {
	ctx := context.Background()
	sim, c := start(t, Config{Devices: assistantBus(&AssistantMemory{CorruptWrites: 1})}, nil)
	check(t, "SetAddress", c.SetAddress(ctx, 0))
	data := bytes.Repeat([]byte{0x5A}, BlockSize)
	check(t, "WriteBlock 1", c.WriteBlock(ctx, BlockCodec{}, 1, data))
	check(t, "WriteBlock 2", c.WriteBlock(ctx, BlockCodec{}, 2, data))
	got, err := c.ReadBlock(ctx, BlockCodec{}, 1)
	if err != nil || got[0] != 0x5A^0xFF || !bytes.Equal(got[1:], data[1:]) {
		t.Fatalf("corrupted block = % X…, %v", got[:4], err)
	}
	if got, _ := c.ReadBlock(ctx, BlockCodec{}, 2); !bytes.Equal(got, data) {
		t.Fatalf("block 2 = % X…", got[:4])
	}
	if err := c.WriteBlock(ctx, BlockCodec{}, assistant.ImageSize/BlockSize, data); !errors.Is(err, mk2.ErrBlockRange) {
		t.Fatalf("write after the end: %v", err)
	}
	st, _ := sim.Assistants(0)
	if st.Writes != 3 || !bytes.Equal(st.Image[2*BlockSize:3*BlockSize], data) {
		t.Fatalf("state: %d writes", st.Writes)
	}
	if _, ok := sim.Assistants(1); ok {
		t.Fatal("device 1 has an area")
	}
}

func TestAssistantAreaRefusesWrites(t *testing.T) {
	ctx := context.Background()
	sim, c := start(t, Config{Devices: assistantBus(&AssistantMemory{RefuseWrites: true})}, nil)
	check(t, "SetAddress", c.SetAddress(ctx, 0))
	if err := c.WriteBlock(ctx, BlockCodec{}, 0, make([]byte, BlockSize)); !errors.Is(err, mk2.ErrBlockRefused) {
		t.Fatalf("WriteBlock: %v", err)
	}
	if st, _ := sim.Assistants(0); !bytes.Equal(st.Image, ErasedArea()) {
		t.Fatal("refused write changed the area")
	}
}

func TestAssistantFaultSkip(t *testing.T) {
	ctx := context.Background()
	sim, c := start(t, Config{Devices: assistantBus(&AssistantMemory{})}, nil)
	check(t, "SetAddress", c.SetAddress(ctx, 0))
	sim.InjectFault(FaultRule{WCmd: WCmdBlockWrite, Fault: Drop, Skip: 2, Count: 1})
	data := make([]byte, BlockSize)
	for n := range 4 {
		err := c.WriteBlock(ctx, BlockCodec{}, n, data)
		if n == 2 && !errors.Is(err, mk2.ErrTimeout) || n != 2 && err != nil {
			t.Fatalf("block %d: %v", n, err)
		}
	}
	st, _ := sim.Assistants(0)
	if st.Writes != 3 || st.Image[2*BlockSize] != 0xFF || st.Image[3*BlockSize] != 0 {
		t.Fatalf("%d writes, block 2 % X, block 3 % X", st.Writes, st.Image[2*BlockSize], st.Image[3*BlockSize])
	}
}

func TestAssistantRAMRecords(t *testing.T) {
	ctx := context.Background()
	_, c := start(t, Config{Devices: assistantBus(&AssistantMemory{RAM: []uint16{0x0052, 1, 2, 0}})}, nil)
	check(t, "SetAddress", c.SetAddress(ctx, 0))
	v, err := c.ReadRAMVar(ctx, 128)
	if err != nil || v[0] != 0x0052 {
		t.Fatalf("RAM 128 = %v, %v", v, err)
	}
	check(t, "SetAddress", c.SetAddress(ctx, 1))
	if _, err := c.ReadRAMVar(ctx, 128); !errors.Is(err, mk2.ErrNotSupported) {
		t.Fatalf("RAM 128 without assistants: %v", err)
	}
}

func TestAreaShapes(t *testing.T) {
	ff := func(b []byte) bool { return bytes.Count(b, []byte{0xFF}) == len(b) }
	for name, tt := range map[string]struct {
		img        []byte
		head, tail int // bytes before the 0xFF free space, after it
	}{
		"erased":    {ErasedArea(), 0, 0},
		"container": {HeaderArea(6), 6, 4},
		"stub":      {HeaderArea(64), 64, 4},
		"program":   {ProgramArea(1000), 1000, 10},
	} {
		img := tt.img
		if len(img) != assistant.ImageSize || !assistant.ChecksumOK(img) {
			t.Errorf("%s: %d bytes, checksum %08X", name, len(img), binary.LittleEndian.Uint32(img[len(img)-4:]))
		}
		if ff(img[:tt.head]) && tt.head > 0 || !ff(img[tt.head:len(img)-tt.tail]) {
			t.Errorf("%s: not %d bytes, 0xFF, %d bytes", name, tt.head, tt.tail)
		}
	}
}
