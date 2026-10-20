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
// The checker words (settings 128 and 191) are computed by Stamp and
// checked by Pair.Consistent and Pair.CheckShown (checker.go). Writing them
// straight to a device needs a fixed write order that has not been checked
// on hardware, so CheckDeviceWrite refuses grid settings for a device;
// settings files can be stamped and saved.
//
// Not known, and therefore not implemented:
//   - Passwords, unlocking and key codes. They are out of scope; only values
//     the user supplies may ever be sent (docs/CLEAN-ROOM.md, "Access
//     control").
//   - The encoding of parameters spread over several words, and which page
//     some grid codes use (PageFor reports those as unknown).
package gridcode
