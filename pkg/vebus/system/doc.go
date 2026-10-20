// Package system models a VE.Bus system configuration: which phase and AC
// input each unit is on, how many phases the system has, which short
// addresses exist and which unit leads its group. It is pure logic with no
// I/O; reading and writing the records on a bus is done elsewhere.
//
// Every unit stores one 9-byte system record (Record). A Layout places units
// (Unit) in a grid of phases × AC inputs; Layout.Records builds each unit's
// record from it, and FromRecords rebuilds a Layout from the records read
// back from all units after checking that they agree. Check runs the
// compatibility rules and returns blocking errors and warnings (Issue).
// QuickLayout builds the fixed Quick Configure layouts from the same rules
// and recognises them in a set of records.
//
// Sources. Unless marked otherwise, the facts here are reverse-engineered
// (#10) from the original PC tools and restated in our own words in this
// project's behavioural spec (#53):
//
//   - the record layout (Record) and the factory record; cross-checked with
//     the community description in kylehart/mk2vsc docs/FORMAT.md §3.1.1
//     (MIT), which shows the same phase bytes, E/F high nibbles and
//     addresses;
//   - the unit kinds by response character, the firmware digits and the
//     compatibility ID (Unit). The digit structure of the firmware number
//     (family group, model, version) is public: Victron, "VE.Bus firmware
//     versions explained";
//   - the layout choices, grid, cell codes, ordering inside a cell and
//     second-input placement (Layout);
//   - record building, short-address allocation including the borrowing of
//     addresses when a phase has more than 11 units, and master marking
//     (Layout.Records);
//   - the assistant-reset flag rule (Unit.AssistantFlagApplies). Its meaning
//     (bit clear = assistants installed) is inferred from community files
//     (kylehart FORMAT.md §3.1.1) and is not confirmed on hardware;
//   - the compatibility rules E1–E13 and W1–W4 (Check). E7 and the device
//     side of mismatches (VE.Bus errors 25 and 2) are also in Victron's
//     public "VE.Bus Error Codes" page; W3 relates to error 16 there. E14,
//     the bus comparison, is our own design;
//   - the consistency check and records → layout mapping (FromRecords). The
//     placement of units after an inconsistent read and the order inside a
//     cell after a consistent read are our own design;
//   - the Quick Configure layouts and its per-unit checks (QuickLayout,
//     CheckQuickUnit). Matching records by short address in RecognizeQuick
//     is our own design.
//
// Rules the spec leaves open are not implemented: the grid-code
// requirement of some layouts, and exceptions to the same-family rule (the
// original has a provision for them but none is filled in). Rule E9 can
// only fire for a unit that is both of the MultiPlus-II family and
// dual-input, which no response character reports today (spec open
// question Q6); it is kept as specified.
//
// Messages are complete English sentences in our own words, meant to go
// through the GUI's translation table. Rule identifiers (Rule) are stable.
package system
