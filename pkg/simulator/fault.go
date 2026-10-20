package simulator

import (
	"slices"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
)

// Fault is a fault injected into the handling of one request.
type Fault int

const (
	// Drop loses the request on the line: the simulator never sees it, so
	// nothing changes and nothing replies.
	Drop Fault = iota + 1
	// NoReply handles the request (a write takes effect) but loses every
	// reply frame, so the host times out.
	NoReply
	// Corrupt handles the request and sends the reply frames with a wrong
	// checksum, so the host discards them.
	Corrupt
	// Unsupported answers a Winmon request as if the device did not support
	// it: 0x90 for CommandReadRAMVar, 0x91 for CommandReadSetting, Sc = 0
	// (or 0x86 without data in mode 2) for the info commands, and "unknown
	// command" (0x80) for the rest (§7.3).
	Unsupported
	// AccessLevel answers a Winmon request with "access level required"
	// (0x9B, §7.3.11).
	AccessLevel
)

// FaultRule injects Fault into the requests it matches. Rules are checked
// in the order they were added; the first match applies.
type FaultRule struct {
	// Cmd is the MK2 command to match; 0 matches any. Every Winmon tag
	// ('W', 'X', 'x', ...) counts as mk2.CmdWinmon.
	Cmd byte
	// WCmd is the Winmon command to match; 0 matches any. A non-zero WCmd
	// matches Winmon requests only.
	WCmd byte
	// Raw makes the rule match raw system frames (see SystemUnit) whose
	// sub-command is SysCmd, such as mk2.SysReadConfig, instead of MK2
	// frames; Cmd and WCmd are then ignored. Rules without Raw never match
	// raw frames.
	Raw    bool
	SysCmd byte
	// Fault is the fault to inject. Unsupported and AccessLevel only match
	// Winmon requests. On a raw frame, Drop loses the frame, NoReply loses
	// the units' replies and Corrupt garbles them.
	Fault Fault
	// Count is how many matching requests get the fault; 0 means all of
	// them until ClearFaults.
	Count int
}

// InjectFault adds a fault rule.
func (s *Simulator) InjectFault(r FaultRule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = append(s.faults, r)
}

// ClearFaults removes all fault rules.
func (s *Simulator) ClearFaults() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = nil
}

// takeFault returns the fault for request f, or 0, and uses up one count of
// the matching rule. s.mu is held.
func (s *Simulator) takeFault(f mk2.Frame) Fault {
	cmd := f.Command
	var wcmd byte
	if mk2.ReplyTag(cmd) != 0 {
		cmd = mk2.CmdWinmon
		if r, err := mk2.ParseWinmonRequest(f); err == nil {
			wcmd = r.Cmd
		}
	}
	return s.take(func(r FaultRule) bool {
		winmonOnly := r.WCmd != 0 || r.Fault == Unsupported || r.Fault == AccessLevel
		return !r.Raw && (r.Cmd == 0 || r.Cmd == cmd) && (!winmonOnly || cmd == mk2.CmdWinmon) && (r.WCmd == 0 || r.WCmd == wcmd)
	})
}

// takeRawFault is takeFault for a raw system frame with sub-command sub.
func (s *Simulator) takeRawFault(sub byte) Fault {
	return s.take(func(r FaultRule) bool {
		return r.Raw && r.SysCmd == sub && r.Fault != Unsupported && r.Fault != AccessLevel
	})
}

// take returns the fault of the first rule that match accepts, or 0, and
// uses up one count of that rule. s.mu is held.
func (s *Simulator) take(match func(FaultRule) bool) Fault {
	for i, r := range s.faults {
		if !match(r) {
			continue
		}
		if r.Count > 0 {
			if s.faults[i].Count--; s.faults[i].Count == 0 {
				s.faults = slices.Delete(s.faults, i, i+1)
			}
		}
		return r.Fault
	}
	return 0
}
