// Package gridcode describes VE.Bus grid codes and the grid parameters
// packed into the grid setting words, without any user interface.
//
// It holds the list of grid codes (the values of setting 81), the rule for
// which codes can be chosen without a password, and the parameter pages:
// for each page, the parameters with the word (setting 129-190) and bit
// field they live in, their scaling and limits. Param.Unpack and Param.Pack
// convert between a word and a parameter's display value, changing only
// the parameter's bits.
//
// Source: every fact here is reverse-engineered (#9, #27), restated in this
// project's own tables, names and wording (CONTRIBUTING.md). Some are
// inferences, marked INFERRED. None has been checked against a device yet.
//
// The checker words (settings 128 and 191) are computed by Stamp and
// checked by Pair.Consistent and Pair.CheckShown (checker.go). They carry
// the grid code and must be written in a fixed order, so CheckDeviceWrite
// keeps grid settings out of the ordinary bulk write; they go through the
// dedicated send instead, and settings files can be stamped and saved.
//
// SendPlan builds the ordered writes of that fixed order (setting 81, check
// word 128, the parameter words, check word 191 last) and Send runs them
// against the device (#52). A stopped send reports which writes had already
// reached the device (SendError).
//
// Not known, and therefore not implemented:
//   - Passwords, unlocking and key codes. They are out of scope; only values
//     the user supplies may ever be sent (CONTRIBUTING.md, "Access
//     control").
//   - The encoding of parameters spread over several words, and which page
//     some grid codes use (PageFor reports those as unknown).
package gridcode
