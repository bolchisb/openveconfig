# Changelog

What was implemented, newest first. Each entry names its issues and commits. Hardware checks that are still open are listed in [docs/HARDWARE-TESTING.md](../HARDWARE-TESTING.md) and on #3.

## 2026-10-20

### M4: Assistants, phase 1 (preserve)

- **Plan.** Phase 1 keeps a unit's assistants through a backup and restore: #77 (transfer spec), #78 (area image), #79 (block read and write), #80 (whole-area read and write with verify and rollback), #81 (CLI and GUI). Phase 2, editing assistants (#32, #33), stays deferred until the package format and its licensing are settled (#8).
- **`pkg/assistant`** (#78): a unit's assistant area as an image. `Expand` turns the compact form of `.rvsc`/`.rvms` files (runs of literal bytes and fill bytes) into the image, and `Compact` turns an image back into that form (6 or more equal bytes become a fill run). The expanded size seen, 2816 bytes, is a constant but is not assumed.
- **`pkg/rvsc`** (#78): `Unit.AssistantImage` and `Unit.SetAssistantImage` read and replace a unit's area; the rest of the file is unchanged. `TestRealAssistantAreas` (`RVSC_TESTDATA`) checks that real device-form areas expand and compact back to the same bytes.

### Virtual switch pages (GUI)

- **Second virtual switch laid out as in the original** (`51549ec`): the Ignore AC input and Generator control pages have the Load conditions and Battery conditions groups with "when … for …" lines, the stop condition (Udc higher than / Bulk finished / Absorption finished) on one line, and the generator-only check boxes.
- **VS options in every usage that has it** (`3d87d1a`): shown for usages 1, 2, 5 and 6, and for the dedicated usages 3 and 4 when the unit can change the period time on Udc; in 3 and 4 without "Do not switch off within" and with the "when virtual switch is on" check box disabled. "Invert driving of auxiliary relay" only for a generator.
- **Checked against VEConfigure** on a real `.rvsc` file: the General, Grid, Inverter, Charger and Virtual switch values match the original tool.

### M6: Firmware files

- **`pkg/vff`** (#37, `51d6876`): reads and validates `.vff` firmware containers (formats 1 to 7, parts, records, check bytes, checksums, metadata); code is not decoded. `openveconfig-cli firmware info <file.vff>`. All 112 firmware files of a local VictronConnect cache validate (`VFF_TESTDATA`).

### Project

- **CONTRIBUTING.md** and a fuller pull request template (#19, `ce49d81`).
- **Release packaging** (#20, `defff18`): the macOS GUI as `OpenVeConfig.app` (ad-hoc signed, not notarized); the README says what is signed.
- **docs/HARDWARE-TESTING.md** (#73, `a082fd7`): the phase-3 procedure for VE.Bus systems, with backups, abort criteria and the open questions.

### M5: System Configurator and Quick Configure

- **Backup files with a system layout** (#70, #71; `76b265e`, `6f54d1b`): `rvsc.File.SetLayout` puts a layout into an `.rvms` downloaded from the same units; `openveconfig-cli rvsc-system` and System Configurator > "Update a backup file…" save the result as a copy.
- **`.vms` real-file findings** (#72, `325dc65`): `TestRealFileFindings` logs what real System Configurator files show about the AC-inputs field and the second-input record. Waiting for sample files.
- **Quick Configure wizard** (#61, `26d9f94`): set up 2 or 3 in parallel, split phase, two phases or three phase by switching the units on one at a time; look at an existing system; reset a unit to standalone; read a VE.Bus error code from the LEDs.
- **System Configurator window** (#60; `e00477e`, `503c762`, `1dc6d28`): phase × AC-input grid with drag and drop and a keyboard alternative, live system check, send with verify and rollback, identify, rename, open settings, `.vms` open and save, a demo of three new units.
- **`openveconfig-cli system show | write | reset | standalone`** (#59, `0db5e6e`), with `vms.File.Layout` and `vms.FromLayout` to convert System Configurator files.
