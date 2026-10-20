// Package bus runs the operations of a VE.Bus system configuration on a
// live bus through an mk2.Client: find the units and read their system
// records (Session.Scan), write a new configuration safely (Session.Write,
// Session.WriteQuick), reset one unit to standalone
// (Session.ResetStandalone), restart the system (Session.Reset) and flash a
// unit's LEDs (Session.FlashLEDs).
//
// It is a separate package so that package system stays pure logic without
// I/O (its TestNoIO guard).
//
// These operations write to live devices. The safety rules they follow are
// our design (#58), on top of facts about the original tools that were
// reverse-engineered (#53) and restated in our own words:
//
//   - Nothing is written without an explicit confirmation
//     (WriteOptions.Confirm); without it a writing operation is a dry run
//     that reads the current records and reports what it would send.
//     Writing a configuration that removes assistants needs a second,
//     separate confirmation (WriteOptions.RemoveAssistants).
//   - Before anything is written the system check (system.Layout.Check)
//     and the bus check (system.Layout.CheckBus against the last scan of
//     the session) must report no error. There is no override.
//   - Immediately before writing, every unit's record is read again (the
//     snapshot). Units are written in the order of Layout.Records, each
//     with up to WriteAttempts attempts of write, read back and compare all
//     nine bytes (reverse-engineered (#53)). Every unit is written, also
//     when its record does not change, as the original tools do
//     (reverse-engineered (#53)).
//   - When a unit still fails, or the context is cancelled, the snapshot is
//     written back, with the same verification, to every unit that was
//     written, and the system is not reset (our design: without a reset
//     the new records are inferred not to take effect, #53). Cancellation
//     lets the attempt in progress finish first, so a write is never cut
//     in half.
//   - Only after every unit verified are the units restarted with the
//     broadcast system reset, sent twice (the original Quick Configure
//     does; the broadcast has no acknowledgement); then the session waits
//     and finds the units again, for up to Options.PostResetWindow, and
//     compares each unit's record and short address with what was written.
//
// Discovery (Scan) follows the original tools (reverse-engineered (#53)):
// identification on for all units (twice), then repeatedly ask for the
// identity of the next unit with identification on, read its setting 85
// info to learn whether it has "external BF", and switch its identification
// off, until two requests in a row get no answer; then read each unit's
// record. A unit that answers twice (it does not switch identification
// off), or more than system.MaxUnits identities, ends the scan (our guard).
//
// Every operation starts with mk2.Client.PrepareSystemLink, which checks
// the link again. A Session sends the keep-alive frame while it is open
// unless Options.NoKeepAlive is set; whether it is needed is a hardware
// check (#3).
//
// Use the Client with Options.Retries 0: the try counts here are the
// documented ones and would multiply with the Client's own retries.
//
// Failures that need the user to act are *Error values with a Kind and a
// Guidance text in English (our words, for the front ends' translation
// tables).
package bus
