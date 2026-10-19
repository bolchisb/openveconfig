// Package vebus models VE.Bus devices and their settings.
//
// A Catalogue lists the known settings, flags and RAM variables with a
// name, unit, UI tab and group, and relations between them (settings stored
// relative to another, flags that must hold inverse values). It is data:
// DefaultCatalogue is built from a Go table and can be extended without
// code changes elsewhere.
//
// A Device holds what one device reported (the options of each variable:
// scale, offset, default, minimum, maximum, access byte) apart from the
// values: the current, possibly edited value and the last value read. It
// converts between raw and display values with the scaling rules of
// "Interfacing with VE.Bus products - MK2 Protocol 3.14" §7.3.8-§7.3.9,
// handles flags as bits of their word, tracks which values changed, and
// validates values against the device's limits and cross-setting rules
// before anything is written.
//
// Section numbers (§) refer to that document. Facts marked
// "reverse-engineered (#6)" come from this project's behavioural spec,
// with OBSERVED or INFERRED confidence; "COMMUNITY" marks third-party
// findings, cited by URL.
package vebus
