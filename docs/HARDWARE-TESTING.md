# Hardware testing

How OpenVeConfig is tested against real MK2/MK3 interfaces and Multi/Quattro units (#3). Simulator tests are not enough: the system frames, the timings and parts of the file formats are reverse-engineered or inferred, and only real units can confirm them.

**No write or flash feature is released before its phase has passed on hardware.**

## Rules for every session

- Use a unit that is off-grid or has no critical loads. Writes to the system configuration switch every unit off for a moment.
- Record the setup before you start: interface (MK2-USB or MK3-USB, version from `openveconfig-cli version`), each unit's model, firmware and compatibility ID, the OS and the OpenVeConfig commit.
- Take a backup before anything is written, and know how to restore it (see each phase).
- EEPROM write cycles are limited (community report). Do not write in loops. A system write sends each unit at most 2 × 3 record writes (three attempts, and three for the rollback).
- Capture the traffic: `--verbose` on the CLI logs every frame with direction and timestamp. For interface timing questions also use a software port monitor **and** a logic analyser on TX/RX, because the tools change the baud rate on the fly and a port monitor alone hides that.
- Keep captures and files made by the original tools out of the repository (#2); record their names in the results table.
- Write the outcome into the results table at the end of this document, and on the issue the check belongs to. A result that contradicts a spec gets its own issue.

## Phases

Each phase starts only after the previous one has passed.

| Phase | What | Writes |
|---|---|---|
| 1 | Read-only: port detection, interface version, discovery, settings, RAM variables, LED/panel state | nothing |
| 2 | Settings write: low-risk settings only, then restore | settings |
| 3 | Multi-unit VE.Bus system configuration, Quick Configure | system records |
| 4 | Firmware (M6), on a spare unit only, with a written recovery plan | firmware |

The checks of phases 1 and 2 are listed on #3. This document describes phase 3 in detail.

## Hardware/firmware matrix

The interface/unit/firmware combinations this project has been tested against. Keep it up to date: add a row
the first time a combination is used, record which phase it has reached, and link the result in the results
table at the end of this document. Captures and files made by the original tools stay out of the repository
(#2); record only their names.

| Interface (version) | Unit model | Firmware | Compatibility ID | OS(es) | Phase reached | Result |
|---|---|---|---|---|---|---|
| _MK3-USB (…)_ | _e.g. MultiPlus-II 48/5000/70_ | _e.g. 5xx_ | _…_ | _Windows / macOS / Linux_ | _1 / 2 / 3 / 4_ | _—_ |

(The template row is kept as the format example; real rows are added as hardware is used.)

## Phase 3: VE.Bus system configuration

Needs at least two units of the same model and firmware (three for a three-phase test), straight RJ45 cables between them, and the interface on one of them. Disconnect GX devices from the VE.Bus during the test, and connect only supported remote panels.

### Preconditions

1. Phase 1 has passed on these units.
2. Backups:
   - with the GX device's VE.Bus backup, download an `.rvms` of the system, and keep a copy;
   - with the original tools, save each unit's settings and, in VE.Bus System Configurator, the system as a `.vms` file.
3. The current state, saved for comparison:

   ```sh
   openveconfig-cli system show --port COM3 --json > before.json
   openveconfig-cli dump --port COM3 --address 0 > unit0-before.json   # and every other address
   ```

### Abort criteria

Stop the session and do not go on with the next step when:

- a command reports **rollback failed**: do not switch the system on for use. Switch every unit off and on again, run `system show`, and send a known configuration again. If units do not answer, connect them one at a time;
- a command reports **not back after reset** and `system show` a minute later still does not find every unit with the record written;
- a unit shows a VE.Bus error code on its LEDs that does not clear (Tools > Quick Configure > Read a VE.Bus error code);
- anything happens that this document does not describe.

### Steps

Run each step with `--verbose 2> stepN.log`, so the frames are kept.

1. **Find the units.** `system show --port COM3` lists every unit with its address, long ID, firmware, kind, phase, AC input, role and record, and says whether the records agree. Compare it with the original tool.
2. **Dry run of a write.** Save the layout from the original tool as `site.vms`, or make one in the GUI's System Configurator, then:

   ```sh
   openveconfig-cli system write --port COM3 site.vms
   ```

   Nothing is written. Check that the checks, the records and the assistant warning are what you expect.
3. **Write.** The same command with `--yes` (and `--remove-assistants` when the dry run says assistants are removed). Each unit is written and read back, the system restarts and the units are found again. Then run `system show` and compare with the original tool.
4. **Restart only.** `system reset --port COM3 --yes`: every unit restarts and comes back with its record.
5. **Rollback (optional, needs a way to make a write fail).** For example, switch one unit off during step 3. The units that were written must hold their previous records again, and the system must not restart.
6. **Standalone.** Switch every unit off, switch on only one, then run `system standalone --port COM3 --yes`. Confirm with `dump` that the unit's other settings did not change.
7. **GUI, System Configurator.** Tools > System Configurator: get the configuration, arrange a parallel system and, if you have three units, a three-phase one, send it, and check that a new Get configuration shows the same layout. Try Identify (the LEDs flash) and Open settings.
8. **GUI, Quick Configure.** Tools > Quick Configure:
   - set up a system: switch the units on one at a time when asked;
   - switch on a unit that does not match (other firmware), if you have one: the flow must stop before anything is written;
   - look at an existing system: the layout is recognised, and Open settings opens the right unit;
   - reset a unit to standalone.
9. **Restore.** Restore the original configuration with the original tools, or with the GX backup, and compare `system show` and `dump` with the files from the preconditions.

### Open questions to answer in phase 3

From #3 and the M5 specs. Note the answer on #3, or on the issue named.

- Settings 76-80 on each unit of a configured system against its system record. Are they a mirror of it? (#6, #71)
- Does a GX restore of an `.rvms` apply the system record in it? (#71)
- The broadcast restart the tools use against the documented `F` 8 reset.
- What the periodic keep-alive does on the bus, and what happens when it stops (`--no-keepalive`).
- What "Freeze" stops on a unit and what releases it.
- Which of the units the tool marks really leads each phase.
- The limit information of setting 85 on a normal unit and, if one is available, on an external-BF unit.
- What the assistant-related flag in the system record (byte 7 bit 4) means on the device. New units hold it clear, so a first configuration asks to remove assistants.
- Whether the interface forwards pass-through frames to the bus unchanged.
- That "reset to standalone" leaves the unit's other settings untouched (step 6).
- The answer to the legacy dongle query on current firmware.

### Sample files to make

For #72 and #71, saved with the original tools from these units, kept outside the repository:

- `.vms` files from VE.Bus System Configurator: two units in parallel with one AC input; a Quattro with its second AC input used; a Quattro with it unused; a three-phase system. Run `VMS_TESTDATA=dir go test ./pkg/vms/ -run TestRealFile -v` on them and paste the log on #72.
- An `.rvms` downloaded from the GX before and after a system write. Run `RVSC_TESTDATA=dir go test ./pkg/rvsc/ -run TestRealFiles -v` on them.

## Results

| Date | OS | Interface | Units (model, firmware, compatibility ID) | Phase / step | Result | Captures and files | Notes |
|---|---|---|---|---|---|---|---|
| | | | | | | | |
