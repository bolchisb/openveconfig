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
// project's own tables, names and wording (docs/CLEAN-ROOM.md). Some are
// inferences, marked INFERRED. None has been checked against a device yet.
//
// Not known, and therefore not implemented:
//   - How the checker words (settings 128 and 191) are computed. A grid
//     parameter change cannot be sent with valid checker words, so CheckWrite
//     refuses it. Choosing the grid code (setting 81) is not affected.
//   - Passwords, unlocking and key codes. They are out of scope; only values
//     the user supplies may ever be sent (docs/CLEAN-ROOM.md, "Access
//     control").
//   - The encoding of parameters spread over several words, and which page
//     some grid codes use (PageFor reports those as unknown).
package gridcode
