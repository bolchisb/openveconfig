package simulator

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/bolchisb/openveconfig/pkg/mk2"
)

// VE.Bus system units.
//
// The system frames are not in the MK2 document. The simulator follows the
// project's behavioural spec (#53), which describes what the original PC
// tools send and expect (reverse-engineered); the device side of it is
// partly inferred from that. Where the spec leaves the device's behaviour
// open, the simulator makes a choice and says so; those choices are
// assumptions to be checked on hardware (#3).

// DefaultResetDuration is how long a system unit stays silent after the
// system reset frame, unless Config.ResetDuration says otherwise. A
// simulator choice: the original tools wait 2-2.5 s after a reset
// (reverse-engineered, #53), so the units are back by then.
const DefaultResetDuration = 2 * time.Second

// factoryRecord is the system record of a unit that has never been
// configured: one unit, L1, address 0 (reverse-engineered, #53).
var factoryRecord = [mk2.SystemRecordLen]byte{0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0xE0, 0x00}

// setting85 is the setting whose info tells the host whether a unit has
// "external BF" (reverse-engineered, #53: the host reads bit 0 of its Max;
// what the setting is for is not known, so its name here is made up).
const setting85 = 85

// SystemUnit makes a Device a unit of a VE.Bus system: it answers the raw
// system frames of mk2 (mk2.Client.Identify, ReadSystemConfig, ...). Zero
// optional fields take their defaults. All values are synthetic.
//
// How a unit handles the system frames (sub-commands, mk2.Sys…) addressed
// to its long ID or to mk2.LongIDAll; frames for another long ID are
// ignored, and only SysIdentify and SysReadConfig get a reply:
//
//   - SysIdentifyMode 01 / 00: identification on / off.
//   - SysIdentify: among the units with identification on, the one with the
//     lowest long ID answers with its identity and active short address
//     (a simulator choice; arbitration on a real bus is unknown).
//   - SysFlashLEDs 01 … / 00: flashing on / off.
//   - SysWriteConfig + 9 bytes: stores the record. The active record and
//     short address do not change until the unit restarts (the original
//     tools reset after writing, so a written record is inferred to take
//     effect only then, #53).
//   - SysReadConfig: replies with the stored record, also right after a
//     write (the original tools read back before resetting, #53).
//   - SysReset: the unit restarts: the stored record becomes active,
//     flashing and freeze end, identification returns to its power-up
//     state, and the unit answers nothing (raw or Winmon) for
//     Config.ResetDuration.
//   - SysFreeze: sets a flag that UnitState shows; its effect is unknown, so
//     nothing else changes.
//   - SysKeepAlive: counted (UnitState); its meaning is unknown.
//   - Any other sub-command, and long ID 0xFFFFFFFF, gets no reply.
//
// A unit that is switched off answers nothing. Switching it on with
// SetPowered starts it as a restart does, without the silent time. Raw
// frames that are not system frames, or have a bad checksum, are dropped,
// and with Config.NineBit all raw frames are (simulator choices).
type SystemUnit struct {
	// LongID identifies the unit. It must be unique, and neither 0
	// (mk2.LongIDAll) nor 0xFFFFFFFF.
	LongID uint32
	// VersionWord is the version word of the identify reply: the low 24
	// bits are the firmware number and must equal Profile.Version, the
	// high 8 bits a sub-version. 0 means Profile.Version.
	VersionWord uint32
	// Letter is the compatibility letter index, 0 = 'A'.
	Letter byte
	// ConfigProtocol is the configuration-protocol byte of the identify
	// reply.
	ConfigProtocol byte
	// IdentifyFlag is byte 5 of the identify reply; 0 means
	// mk2.IdentifyFlag.
	IdentifyFlag byte
	// ResponseChar is byte 5 of the configuration reply and gives the
	// kind of unit (mk2.ResponseChar…); 0 means 0x02, a single-input Multi.
	ResponseChar byte
	// Record is the stored system record; zero means the factory record
	// 01 01 01 00 00 00 00 E0 00. Its byte 8 must equal Device.Address.
	Record [mk2.SystemRecordLen]byte
	// Off makes the unit start switched off.
	Off bool
	// ExternalBF clears bit 0 of the Max of setting 85 (GetSettingInfo),
	// which the host reads as "external BF"; otherwise it is set. Setting
	// 85 is added to the profile if missing.
	ExternalBF bool
	// IdentifyOff makes identification start off after power-up and
	// restart. The default is on (a simulator choice; the real default is
	// unknown).
	IdentifyOff bool
}

// Behaviour makes one system unit misbehave, to test the host's error
// paths. The zero value is a unit that behaves. Set it with SetBehaviour.
type Behaviour struct {
	// NoReadReply: the unit never answers SysReadConfig.
	NoReadReply bool
	// IgnoreWrites: written records are dropped (a refused write), so the
	// read-back shows the previous record.
	IgnoreWrites bool
	// CorruptWrites: written records are stored with byte 0 inverted, so
	// the read-back differs from what was written.
	CorruptWrites bool
	// StaleReads: once a write has changed the record, the next StaleReads
	// reads (counted from SetBehaviour) return the record from before.
	StaleReads int
	// IgnoreMute: identification cannot be switched off, so the unit
	// keeps answering SysIdentify.
	IgnoreMute bool
	// UnsolicitedIdentify: the unit sends its identify reply as soon as
	// identification is switched on, before the host asks for it.
	UnsolicitedIdentify bool
	// PowerOffOnMute: the unit switches off when its identification is
	// switched off, so a scan loses it after identifying it.
	PowerOffOnMute bool
	// PowerOffOnWrite: the unit switches off when a record is written to
	// it, before storing it (a unit that drops off during a write).
	PowerOffOnWrite bool
	// IgnoreReset: the unit does not restart on SysReset.
	IgnoreReset bool
	// ResetDuration, if not 0, replaces Config.ResetDuration for this unit
	// (negative: no silent time).
	ResetDuration time.Duration
}

// UnitState is a snapshot of a system unit, for tests and demos.
type UnitState struct {
	LongID  uint32
	Powered bool
	// Restarting is true during the silent time after a reset.
	Restarting bool
	// Address is the active short address.
	Address        byte
	Identification bool
	Flashing       bool
	Frozen         bool
	// Record is the stored record, ActiveRecord the one in use since the
	// last restart.
	Record, ActiveRecord [mk2.SystemRecordLen]byte
	// Writes counts SysWriteConfig frames received, including ignored
	// ones; Resets counts restarts caused by SysReset.
	Writes, Resets int
	KeepAlives     int
	LastKeepAlive  time.Time
}

// unit is the live state of a system unit. The embedded SystemUnit holds
// the configuration; its Record is the stored record.
type unit struct {
	SystemUnit
	on                         bool
	active                     [mk2.SystemRecordLen]byte
	identify, flashing, frozen bool
	silentUntil                time.Time
	writes, resets, keepAlives int
	lastKeepAlive              time.Time

	b      Behaviour
	stale  int                       // reads left that return old
	old    [mk2.SystemRecordLen]byte // record before the last changing write
	hasOld bool
}

// newUnit checks and completes d.System and starts the unit (switched on
// unless Off). d.Address is the unit's address.
func newUnit(d Device) *unit {
	u := &unit{SystemUnit: *d.System}
	switch {
	case u.LongID == mk2.LongIDAll || u.LongID == 0xFFFFFFFF:
		panic(fmt.Sprintf("simulator: invalid long ID 0x%08X", u.LongID))
	case u.VersionWord == 0:
		u.VersionWord = d.Profile.Version
	case u.VersionWord&0xFFFFFF != d.Profile.Version:
		panic(fmt.Sprintf("simulator: unit 0x%08X: version word 0x%08X does not match firmware %d", u.LongID, u.VersionWord, d.Profile.Version))
	}
	if u.IdentifyFlag == 0 {
		u.IdentifyFlag = mk2.IdentifyFlag
	}
	if u.ResponseChar == 0 {
		u.ResponseChar = 0x02
	}
	if u.Record == ([mk2.SystemRecordLen]byte{}) {
		u.Record = factoryRecord
	}
	if u.Record[8] != d.Address {
		panic(fmt.Sprintf("simulator: unit 0x%08X: address 0x%02X differs from record address 0x%02X", u.LongID, d.Address, u.Record[8]))
	}
	u.on = !u.Off
	u.active = u.Record
	u.identify = !u.IdentifyOff
	return u
}

// withExternalBF returns a copy of settings in which setting 85 reports
// ext: bit 0 of its Max is 0 for external BF, 1 otherwise.
func withExternalBF(settings []Setting, ext bool) []Setting {
	settings = slices.Clone(settings)
	i := slices.IndexFunc(settings, func(st Setting) bool { return st.ID == setting85 })
	if i < 0 {
		settings = append(settings, Setting{ID: setting85, Name: "Setting85", Sc: 1})
		i = len(settings) - 1
	}
	settings[i].Max |= 1
	if ext {
		settings[i].Max &^= 1
	}
	return settings
}

func (d *device) longID() uint32 {
	if d.sys == nil {
		return 0
	}
	return d.sys.LongID
}

// answers reports whether d answers frames at time now: a system unit only
// while switched on and not restarting.
func (d *device) answers(now time.Time) bool {
	return d.sys == nil || d.sys.on && !now.Before(d.sys.silentUntil)
}

// start puts a system unit in its power-up state and keeps it silent for
// silent. s.mu is held.
func (s *Simulator) start(d *device, silent time.Duration) {
	u := d.sys
	u.active = u.Record
	d.addr = u.Record[8]
	u.identify = !u.IdentifyOff
	u.flashing, u.frozen = false, false
	u.silentUntil = time.Time{}
	if silent > 0 {
		u.silentUntil = time.Now().Add(silent)
	}
	s.sortDevices()
}

// units returns the system units in long ID order. s.mu is held.
func (s *Simulator) units() []*device {
	var ds []*device
	for _, d := range s.devices {
		if d.sys != nil {
			ds = append(ds, d)
		}
	}
	slices.SortFunc(ds, func(a, b *device) int { return cmp.Compare(a.sys.LongID, b.sys.LongID) })
	return ds
}

func (s *Simulator) unit(longID uint32) *device {
	for _, d := range s.devices {
		if d.sys != nil && d.sys.LongID == longID {
			return d
		}
	}
	return nil
}

// raw handles a raw frame from the host (see SystemUnit). s.mu is held.
func (s *Simulator) raw(tok []byte) {
	p, err := mk2.DecodeRaw(tok)
	if err != nil || s.nineBit {
		return
	}
	longID, sub, args, err := mk2.ParseSystemPayload(p)
	if err != nil {
		return
	}
	s.fault = s.takeRawFault(sub)
	defer func() { s.fault = 0 }()
	if s.fault == Drop {
		return
	}
	now := time.Now()
	for _, d := range s.units() {
		if !d.answers(now) || longID != mk2.LongIDAll && longID != d.sys.LongID {
			continue
		}
		if sub == mk2.SysIdentify {
			if d.sys.identify {
				s.send(d.identity().Frame())
				return // only one unit answers
			}
			continue
		}
		s.system(d, sub, args, now)
	}
}

func (d *device) identity() mk2.Identity {
	u := d.sys
	return mk2.Identity{
		LongID: u.LongID, Flag: u.IdentifyFlag, ShortAddress: d.addr,
		VersionWord: u.VersionWord, Letter: u.Letter, ConfigProtocol: u.ConfigProtocol,
	}
}

// system handles a system frame other than SysIdentify for the unit d.
// s.mu is held.
func (s *Simulator) system(d *device, sub byte, args []byte, now time.Time) {
	u := d.sys
	switch sub {
	case mk2.SysIdentifyMode:
		switch {
		case len(args) == 0:
		case args[0] == 1:
			u.identify = true
			if u.b.UnsolicitedIdentify {
				s.send(d.identity().Frame())
			}
		case args[0] == 0 && u.b.PowerOffOnMute:
			u.on = false
		case args[0] == 0 && !u.b.IgnoreMute:
			u.identify = false
		}
	case mk2.SysFlashLEDs:
		if len(args) > 0 {
			u.flashing = args[0] != 0
		}
	case mk2.SysReset:
		if u.b.IgnoreReset {
			return
		}
		u.resets++
		dur := s.resetDur
		if u.b.ResetDuration != 0 {
			dur = u.b.ResetDuration
		}
		s.start(d, dur)
	case mk2.SysWriteConfig:
		if len(args) != mk2.SystemRecordLen {
			return // a simulator choice
		}
		u.writes++
		switch {
		case u.b.PowerOffOnWrite:
			u.on = false
			return
		case u.b.IgnoreWrites:
			return
		}
		rec := [mk2.SystemRecordLen]byte(args)
		if u.b.CorruptWrites {
			rec[0] ^= 0xFF
		}
		if rec != u.Record {
			u.old, u.hasOld = u.Record, true
		}
		u.Record = rec
	case mk2.SysReadConfig:
		if u.b.NoReadReply {
			return
		}
		rec := u.Record
		if u.hasOld && u.stale > 0 {
			rec = u.old
			u.stale--
		}
		s.send(mk2.SystemConfig{LongID: u.LongID, ResponseChar: u.ResponseChar, Record: rec}.Frame())
	case mk2.SysFreeze:
		u.frozen = true
	case mk2.SysKeepAlive:
		u.keepAlives++
		u.lastKeepAlive = now
	}
}

// SetPowered switches the system unit longID on or off, as a user does
// with its switch. Switching it on starts it with its stored record (see
// SystemUnit). It reports whether the unit exists.
func (s *Simulator) SetPowered(longID uint32, on bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.unit(longID)
	if d == nil {
		return false
	}
	if on && !d.sys.on {
		s.start(d, 0)
	}
	d.sys.on = on
	return true
}

// SetBehaviour sets the misbehaviour of the system unit longID, replacing
// any earlier one. It reports whether the unit exists.
func (s *Simulator) SetBehaviour(longID uint32, b Behaviour) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.unit(longID)
	if d == nil {
		return false
	}
	d.sys.b = b
	d.sys.stale = b.StaleReads
	return true
}

// Unit returns the state of the system unit longID, and whether it exists.
func (s *Simulator) Unit(longID uint32) (UnitState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.unit(longID)
	if d == nil {
		return UnitState{}, false
	}
	return d.unitState(time.Now()), true
}

// Units returns the state of every system unit, in long ID order.
func (s *Simulator) Units() []UnitState {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var us []UnitState
	for _, d := range s.units() {
		us = append(us, d.unitState(now))
	}
	return us
}

func (d *device) unitState(now time.Time) UnitState {
	u := d.sys
	return UnitState{
		LongID: u.LongID, Powered: u.on, Restarting: u.on && now.Before(u.silentUntil),
		Address: d.addr, Identification: u.identify, Flashing: u.flashing, Frozen: u.frozen,
		Record: u.Record, ActiveRecord: u.active,
		Writes: u.writes, Resets: u.resets, KeepAlives: u.keepAlives, LastKeepAlive: u.lastKeepAlive,
	}
}

// Ready-made system buses. Long IDs are made up, starting at firstLongID;
// the units use Synthetic12V230 (firmware 2699481, made up). The configured
// records are the worked examples of the project's spec (#53), built with
// the rules that pkg/vebus/system implements.

const firstLongID = 0x10000001

// SystemDevice returns a system unit u on a Synthetic12V230 profile, at the
// short address of its record (the factory record when u.Record is zero).
// If u.VersionWord is set, the profile's firmware follows it.
func SystemDevice(u SystemUnit) Device {
	p := Synthetic12V230()
	if u.VersionWord != 0 {
		p.Version = u.VersionWord & 0xFFFFFF
	}
	addr := u.Record[8]
	if u.Record == ([mk2.SystemRecordLen]byte{}) {
		addr = factoryRecord[8]
	}
	return Device{Address: addr, Profile: p, System: &u}
}

// SystemFactory returns n units with the factory record, all at address 0,
// as new units arrive. Their long IDs are firstLongID, firstLongID+1, ...
func SystemFactory(n int) []Device {
	ds := make([]Device, n)
	for i := range ds {
		ds[i] = SystemDevice(SystemUnit{LongID: firstLongID + uint32(i)})
	}
	return ds
}

// systemBus returns units with the given records and consecutive long IDs.
func systemBus(recs ...[mk2.SystemRecordLen]byte) []Device {
	ds := make([]Device, len(recs))
	for i, r := range recs {
		ds[i] = SystemDevice(SystemUnit{LongID: firstLongID + uint32(i), Record: r})
	}
	return ds
}

// SystemParallel2 returns two units configured in parallel on one phase.
func SystemParallel2() []Device {
	return systemBus(
		[mk2.SystemRecordLen]byte{0x02, 0x02, 0x09, 0x00, 0x00, 0x00, 0x00, 0xF0, 0x00},
		[mk2.SystemRecordLen]byte{0xFF, 0xFF, 0x09, 0x00, 0x00, 0x00, 0x00, 0xF0, 0x03},
	)
}

// SystemThreePhase returns three units configured as a three-phase system,
// one per phase.
func SystemThreePhase() []Device {
	return systemBus(
		[mk2.SystemRecordLen]byte{0x01, 0x01, 0x07, 0x00, 0x00, 0x00, 0x00, 0xF8, 0x00},
		[mk2.SystemRecordLen]byte{0x01, 0x01, 0x07, 0x00, 0x00, 0x00, 0x04, 0xF9, 0x01},
		[mk2.SystemRecordLen]byte{0x01, 0x01, 0x07, 0x00, 0x00, 0x00, 0x08, 0xFA, 0x02},
	)
}

// SystemMixedFirmware returns SystemParallel2 with different firmware in
// the second unit (2699475, made up), which the system check refuses.
func SystemMixedFirmware() []Device {
	ds := SystemParallel2()
	ds[1].System.VersionWord = 2699475
	ds[1].Profile.Version = 2699475
	return ds
}
