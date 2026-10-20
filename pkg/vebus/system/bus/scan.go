package bus

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/vebus/system"
)

// ScanReport is what a scan found.
type ScanReport struct {
	// Units are the units that answered, in the order found. Units the
	// session already knew keep their *system.Unit (and Name).
	Units []*system.Unit
	// Lost are units that were identified, or known from an earlier scan,
	// but did not answer now. They are no longer in the session's list.
	Lost []*system.Unit
	// Layout is rebuilt from the records (system.FromRecords). When they
	// are inconsistent every unit is unplaced and Inconsistent says why.
	Layout       *system.Layout
	Inconsistent *system.Issue
	// Issues are the system check of Layout.
	Issues []system.Issue
	// Differing are the units whose record differs from what Layout would
	// give them (not an error).
	Differing []*system.Unit
}

// Scan finds every unit on the bus and reads its record ("Get
// configuration"). The user should first switch every unit on, connect
// only supported remote panels and disconnect GX devices from the VE.Bus.
// It writes nothing: it only switches each unit's identification (volatile
// state) on and then off again, which is left off as the original tools do
// (reverse-engineered (#53)).
//
// When no unit answers it returns the empty report and a KindNoUnits
// *Error.
func (s *Session) Scan(ctx context.Context, p Progress) (*ScanReport, error) {
	if err := s.lock(); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	if err := s.prepare(ctx, p); err != nil {
		return nil, err
	}
	rep, err := s.scan(ctx, p)
	if err == nil && len(rep.Units) == 0 {
		err = &Error{Kind: KindNoUnits}
	}
	return rep, err
}

// scan runs the discovery of Scan on a prepared link and makes its result
// the session's list of units. s.mu is held.
func (s *Session) scan(ctx context.Context, p Progress) (*ScanReport, error) {
	c := s.c
	known := map[uint32]*system.Unit{}
	for _, u := range s.units {
		known[u.LongID] = u
	}
	for range 2 {
		if err := c.SetIdentification(ctx, mk2.LongIDAll, true); err != nil {
			return nil, stopErr("switch identification on", err)
		}
	}
	var found []*system.Unit
	seen := map[uint32]bool{}
	active := map[uint32]byte{}
	for misses := 0; misses < 2; {
		p.send(Event{Step: StepIdentify, Done: len(found)})
		id, err := c.Identify(ctx)
		if errors.Is(err, mk2.ErrTimeout) {
			misses++
			continue
		}
		if err != nil {
			return nil, stopErr("identify", err)
		}
		misses = 0
		u := known[id.LongID]
		if u == nil {
			u = &system.Unit{LongID: id.LongID}
		}
		if id.ConfigProtocol > maxConfigProtocol {
			return nil, &Error{Kind: KindNewerTool, Units: []*system.Unit{u}}
		}
		if seen[id.LongID] || len(found) == system.MaxUnits {
			return nil, &Error{Kind: KindNoMute, Units: []*system.Unit{u}}
		}
		seen[id.LongID] = true
		ext, err := s.externalBF(ctx, id.ShortAddress, u.ExternalBF)
		if err != nil {
			return nil, stopErr("read setting info", err)
		}
		u.VersionWord, u.Letter, u.ConfigProtocol, u.IdentifyFlag = id.VersionWord, id.Letter, id.ConfigProtocol, id.Flag
		u.ExternalBF = ext
		active[id.LongID] = id.ShortAddress
		found = append(found, u)
		if err := c.SetIdentification(ctx, id.LongID, false); err != nil {
			return nil, stopErr("switch identification off", err)
		}
	}

	rep := &ScanReport{}
	for i, u := range found {
		p.send(Event{Step: StepRead, Unit: u, Done: i + 1, Total: len(found)})
		sc, err := s.read(ctx, u.LongID)
		if err != nil {
			if !perUnit(err) {
				return nil, stopErr("read record", err)
			}
			rep.Lost = append(rep.Lost, u)
			delete(active, u.LongID)
			continue
		}
		u.ResponseChar = sc.ResponseChar
		u.Record = record(u, sc)
		rep.Units = append(rep.Units, u)
	}
	for _, u := range s.units {
		if !seen[u.LongID] {
			rep.Lost = append(rep.Lost, u)
		}
	}
	s.units, s.active, s.scanned = rep.Units, active, true
	rep.Layout, rep.Inconsistent = system.FromRecords(rep.Units)
	rep.Issues = rep.Layout.Check()
	if rep.Inconsistent == nil {
		rep.Differing = rep.Layout.Differing()
	}
	return rep, nil
}

// maxConfigProtocol is the newest configuration protocol supported (rule
// E13, reverse-engineered (#53)).
const maxConfigProtocol = 1

// record returns the record a read reply gives u: the record itself, or,
// when the unit reports that it has no system configuration, the factory
// record with the assistant-reset flag u had before (reverse-engineered
// (#53)). u.ResponseChar must be the reply's.
func record(u *system.Unit, sc mk2.SystemConfig) system.Record {
	if sc.ResponseChar == system.ResponseNoConfig {
		return system.FactoryDefault(u.Record)
	}
	return system.Record(sc.Record)
}

// read reads the record of the unit longID, with up to ReadTries tries
// while the unit does not answer.
func (s *Session) read(ctx context.Context, longID uint32) (mk2.SystemConfig, error) {
	var sc mk2.SystemConfig
	var err error
	for range ReadTries {
		sc, err = s.c.ReadSystemConfig(ctx, longID)
		if err == nil || !perUnit(err) {
			break
		}
	}
	return sc, err
}

// settingExternalBF is the setting whose info tells whether a unit has
// "external BF": bit 0 of its maximum is clear when it has, and a unit
// without the setting has it. Reverse-engineered (#53); what the setting is
// for, and what BF stands for, is not known.
const settingExternalBF = 85

// externalBF reads whether the unit at addr has external BF. New units
// usually share an address, so the read can collide; then, after one retry,
// and on "access level required" or any other refusal, the flag stays prev
// (reverse-engineered (#53); the tolerance is the original tools'). Only a
// cancelled context is an error; a broken link shows up in the next step.
//
// It sends GetSettingInfo (0x35, §7.3.8) itself instead of using
// Client.SettingInfo, whose command can be changed in the Client's options:
// the original tools use 0x35 here (reverse-engineered (#53)). The link is
// in Winmon mode 2 after PrepareSystemLink, so the reply has the long form.
func (s *Session) externalBF(ctx context.Context, addr byte, prev bool) (bool, error) {
	for range 2 {
		err := s.c.SetAddress(ctx, addr)
		var p []byte
		if err == nil {
			p, err = s.c.Winmon(ctx, mk2.WCmdGetSettingInfo, settingExternalBF, 0)
		}
		switch {
		case ctx.Err() != nil:
			return prev, ctx.Err()
		case errors.Is(err, mk2.ErrTimeout):
			continue
		case err != nil:
			return prev, nil
		}
		switch {
		case p[0] == mk2.WReplySettingInfoScale && len(p) >= 11:
			if binary.LittleEndian.Uint16(p[1:]) == 0 { // Sc = 0: not supported
				return true, nil
			}
			return binary.LittleEndian.Uint16(p[9:])&1 == 0, nil
		case p[0] == mk2.WReplyReadSettingOK || p[0] == mk2.WReplySettingNotSupported:
			return true, nil
		}
		return prev, nil
	}
	return prev, nil
}

// stopErr wraps an error that ends an operation.
func stopErr(what string, err error) error {
	if errors.Is(err, mk2.ErrLinkMode) {
		return &Error{Kind: KindLink, Err: err}
	}
	return fmt.Errorf("system: %s: %w", what, err)
}
