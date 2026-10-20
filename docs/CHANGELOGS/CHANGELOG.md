# Changelog

What was implemented, newest first. Each entry names its issues and commits. Hardware checks that are still open are listed in [docs/HARDWARE-TESTING.md](../HARDWARE-TESTING.md) and on #3.

## 2026-10-20

### M4: Assistants, phase 1 (preserve)

- **Plan.** Phase 1 keeps a unit's assistants through a backup and restore: #77 (transfer spec), #78 (area image), #79 (block read and write), #80 (whole-area read and write with verify and rollback), #81 (CLI and GUI). Phase 2, editing assistants (#32, #33), stays deferred until the package format and its licensing are settled (#8).
- **`pkg/assistant`** (#78): a unit's assistant area as an image. `Expand` turns the compact form of `.rvsc`/`.rvms` files (runs of literal bytes and fill bytes) into the image, and `Compact` turns an image back into that form (6 or more equal bytes become a fill run). The expanded size seen, 2816 bytes, is a constant but is not assumed.
- **`pkg/rvsc`** (#78): `Unit.AssistantImage` and `Unit.SetAssistantImage` read and replace a unit's area; the rest of the file is unchanged. `TestRealAssistantAreas` (`RVSC_TESTDATA`) checks that real areas expand and compact back to the same bytes.
- **Checked on a backup with assistants** (#78): the area of a file saved by VEConfigure (upload form) expands to 2816 bytes and compacts back byte for byte; its last run has the fill byte 0x00, which `Compact` now writes too.
- **Image checksum** (#77, #80): `assistant.Checksum` and `ChecksumOK`. The last 4 bytes of an image are the sum of the other bytes, inverted and shifted by 8 × (offset mod 4), minus 1; checked on the same unit with an ESS assistant and without assistants (the empty image's first bytes make the checksum 0xFFFFFFFF).

### Grid parameters as in the original (GUI)

- **All 17 parameter pages checked against the original's form**: every control bound to a grid word was matched by word and bits with `pkg/gridcode`. Nothing extra remains (the Microgrid page's hidden diagnostic curve points are gone: the original never shows them). What is missing are only values spread over 3–8 words whose conversion is not decoded (CEI network-service frequencies and droops, AU/NZ 2015 Volt-Watt curve and Fstop, Microgrid droops, Europe U> and two stage-1 delays); they stay listed as not supported.
- **Same order as the original** on every page: groups and parameters follow the original's screen (for example Europe: own settings, Normal connect, Reconnect after trip, IP protection stage 1 and 2 with each level before its delay, P(f), P(U); Microgrid: Enable microgrid operation first). A Volt-Watt point reads voltage, invert/charge, maximum power.
- **Page layout**: the tab is named after the page ("Romania grid code settings (TOR-D4 derivative)"); the page's own settings come first, without a title; the technical "setting N, bits …" lines and the check-word note are gone (a note stays only when the check words do not fit); the Undo button shows only while there are grid changes.
- **Over voltage U> (10 min. running mean)** is shown and edited on every page that has it except Europe: a 32-bit value in settings 129 (low) and 130 (high), scale 5e-05 V, one decimal (`gridcode.Param.High`, `FieldWords`, `PackWords`). The word order is inferred from a real Romania file, where it gives the original's 255.3 V. Europe's U> uses other bits and stays unsupported.
- **Greyed as in the original**: the P(U) voltages of Romania and Chile while the P(U) curve is "Not used" (the same handler in the original), and Europe's P(U) points and filter time while the response is "Not used" (the original disables their panel). Austria has no such rule in the original.
- **Reactive power group** (#84; `pkg/gridcode/reactive.go`, `Page.WithReactivePower`): on the pages the original shows it on (UK, AS/NZS 4777 2020, France, Spain, Belgium, Austria, Chile, Romania, Europe), with the layout of the grid words the original chooses by grid code and parameter-set version: layout A (one point per word) or the compact layout B (France from version 2, Belgium and Austria from version 1, Europe always), which adds the Limitation box (P Lock-in, P Lock-out). Reactive power regulation and the filter time are always shown; the other rows follow the regulation, as in the original (`Param.Shown`): Cos phi curve points with "Cos Phi as function of P", Q with a fixed Q, the Q(U) points with "Q follows grid voltage". One decimal, as the original. On a real Romania file the filter time reads 3.3 s, as in the original.
- **Cos phi values** (#84): "Cos phi is" and the cos phi of each curve point, with the original's conversion: the word is 32768 × (2 − cos phi) for a positive cos phi and 32768 × −cos phi for a negative one (32768 = 1.00), in steps of 0.01; a cos phi nearer to 0 than the code's lowest (0.8 for UK, Europe, codes 18–20 and 30–32, Belgium and Austria from version 1, France from version 2; 0.9 otherwise) is written as that lowest (`Param.CosPhiMin`). The Romania file shows 1.00, as the original.
- **Trip delay U< and U>> on the Romania and Austria pages** (TOR-D4) use the original's own conversion: the word counts ticks of 26.2144 ms minus 15 (at least 4), shown in steps of 0.5 s, and 3 means 0 s (`Param.TripDelay`). The Romania file holds 3 and now shows 0.0 s, as the original (it showed 1.5 s).
- **Reactive power, not yet:** Min. Cos phi, whose conversion is not decoded; the voltage lock-in box, which the original shows on a flag that is not known. They are listed as not supported. For France, Belgium and Austria read from a device the parameter-set version is not known, so the group is listed as not supported there. Germany (codes 4 and 5) still has no page, because which of the two pages the original picks is not known.

### Access passwords (GUI, demo only)

- **Password dialog and `0x3A`** (#9, #52; `47722c0`, `2a97d90`, PR #82): "Enter access password(s) (comma separated)." appears when a grid code that needs a password is chosen (cancel puts the old code back), before a send whose changes need one, and from the send result after the device refused settings. The passwords go to the device with Winmon `0x3A` before the writes; a reply starting with `0x9C` counts as accepted, anything else stops the send before anything is written.
- **No local check.** The program never checks, derives, keeps or saves a password; they live in memory for the connection. Unlike VEConfigure it does not check the password itself (docs/CLEAN-ROOM.md, Access control). That the device refuses a wrong password is inferred.
- **Off on real devices** (`accessPasswordsOnHardware`) until the checks listed on #3 are done; the demo devices accept the made-up password `DEMO`.
- `pkg/mk2`: `SendAccessPassword`, `ErrPasswordRefused`; `0x9B` after `CommandWriteData` is `ErrAccessLevel`. `pkg/simulator`: `Config.AccessPasswords` (test values only).

### Layout fixes (GUI)

- **From the label review** (#76; `451f084`, PR #82): General in the original's group order; chart titles wrap; the grid code button has the normal control width; the reason in the bar of unsent changes has its own line; Quick Configure says "Com port…".

### Labels and missing functions (GUI)

- **Labels in the original's words** (`e00477e`, `666f3a3`, PR #75; docs/CLEAN-ROOM.md, second exception): settings pages, grid pages, remote panel, monitors, menus and dialogs, translated in Romanian. Labels that follow values, as in the original: the usage options follow the inversion flag (26), the second switch's page follows its usage, the LED alarm delays say "alarm" or "pre-alarm". "UPS function" is flag 3 inverted. Setting 71 left the Advanced page.
- **Grid code pages**: `gridlabels.go` maps the `pkg/gridcode` names to the original's words through `tr`; page titles keep the standard in parentheses; the grid code list uses the original's "Country: standard" texts.
- **Functions the original has** (`666f3a3`): Parallel systems, Multi phase type and External current sensor on General (settings 13, 14, 89, flags 0 and 1; Set all settings to default leaves the system configuration alone); "Do not restart after short-circuit (VDE 2510-2 safety)" on Inverter (flag 64); the stop condition of the second switch (setting 58) as a choice plus a voltage; File > Export settings (copy, save as text) and Print settings (HTML in the browser); Help menu with About.
- **Grid code picker** (`666f3a3`): a button with the chosen code that opens a dialog with search, instead of a drop-down list that covered the window.

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
