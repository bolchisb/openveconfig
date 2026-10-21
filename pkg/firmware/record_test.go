package firmware

import (
	"testing"

	"github.com/bolchisb/openveconfig/internal/vfftest"
	"github.com/bolchisb/openveconfig/pkg/vff"
)

func TestRecords(t *testing.T) {
	f, err := vff.Parse(vfftest.File7())
	if err != nil {
		t.Fatal(err)
	}
	recs := Records(f)
	// Main code: 3 records * 73 bytes = 219 bytes -> 4 blocks of 64.
	// Bootblock code: 1 record * 73 bytes = 73 bytes -> 2 blocks.
	if len(recs) != 6 {
		t.Fatalf("got %d records, want 6", len(recs))
	}
	for i, r := range recs {
		if int(r.Index) != i {
			t.Errorf("record %d has index %d", i, r.Index)
		}
	}
	// First four are main code, last two bootblock; addresses run per region.
	wantType := []byte{typeMainCode, typeMainCode, typeMainCode, typeMainCode, typeBootblock, typeBootblock}
	wantAddr := []uint32{0, 64, 128, 192, 0, 64}
	for i, r := range recs {
		if r.Type != wantType[i] {
			t.Errorf("record %d type %d, want %d", i, r.Type, wantType[i])
		}
		if r.Address != wantAddr[i] {
			t.Errorf("record %d address %d, want %d", i, r.Address, wantAddr[i])
		}
	}

	// A record frame round-trips its 70-byte payload (2 index + 4 address +
	// 64 data).
	frame, err := recs[0].Frame()
	if err != nil {
		t.Fatal(err)
	}
	body, err := unescape(frame[2:])
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 2+4+dataBlockLen+1 { // payload + checksum
		t.Errorf("record frame body length %d", len(body))
	}
}

func TestRecordsNoMainCode(t *testing.T) {
	// A file whose Main part has no code yields no records.
	data := vfftest.Part3(vfftest.Spec3{Format: 7, Meta: "Part=Main,Version=2003456,CL=0,EL=0",
		Desc: []byte("C")})
	f, err := vff.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if recs := Records(f); len(recs) != 0 {
		t.Errorf("got %d records, want 0", len(recs))
	}
}
