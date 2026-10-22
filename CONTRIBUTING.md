# Contributing to OpenVeConfig

Thank you for helping. This project writes to inverter/chargers that people depend on, and it reimplements tools that are not open source. So two rules come before everything else:

1. **Clean room.** Never put decompiled code, data or text extracted from Victron software, firmware, or Victron's help text into code, comments, issues, pull requests or commit messages. Read [Clean room](#clean-room) below before your first contribution.
2. **Hardware safety.** Do not test write or flash features on equipment that others depend on. Follow the [hardware test plan](docs/HARDWARE-TESTING.md): read-only first, a backup before any write, and stop when an abort criterion is met.

## Clean room

This repository is public and holds an independent, interoperability-only implementation of the PC side of Victron's VE.Bus configuration tools. It never redistributes Victron's code, data or other copyrighted material. The rules apply to everything here: code, comments, documentation, test fixtures, issues, pull requests and commit messages.

**Allowed sources.** Victron's public documents (cite the document and section); observations of our own hardware, for example serial traffic captured with this project's own tool; MIT-compatible community code, with attribution; public community findings, cited by URL and marked community-reported where Victron has not confirmed them.

**Never committed**, in whole or in part:

- decompiled or disassembled code from Victron software, or code transcribed from it;
- data tables, payloads, images, icons, help text, strings or translations extracted from Victron binaries, including demo device descriptors and setting or variable tables;
- Victron firmware files (`.vff`, `.vfd`) and Victron assistant packages, or excerpts of them;
- private reverse-engineering notes, tool output, decompiler projects or screenshots of decompiled code;
- local filesystem paths of contributors' machines.

Reverse-engineering findings become behavioural specs in our own words before anyone implements them, each statement marked with its source, and that work stays out of this repository. Test fixtures and simulator profiles are synthetic or come from our own captures of our own hardware.

**Access control.** Password, access-level and key-code features accept only values the user supplies. The project never checks, derives, stores, brute-forces or ships such values. Where the original tool checks a password itself, this one leaves that to the device.

**Short on-screen labels.** Settings, grid, virtual-switch and assistant pages use the original tool's short on-screen words, kept as they are because installers know them (decided 2026-10-20). Help lines, notes and anything longer are this project's own wording, and a label with no counterpart in the original is ours.

**Assistant packages.** Assistant definitions are Victron content and are never shipped with this project, in any form. They reach users through their own VEConfigure installation: the program reads `.vap`/`.dvap` packages at run time from the folder VEConfigure downloads them into, or from a folder the user chooses, and can fetch them from Victron's update server into that folder on the user's behalf. Nothing taken from a package — no source, no help text, no compiled output — is committed, including as a test fixture; the tests that need one skip unless the user points a variable at their own files. (#8)

## Setup

Install Go (the version in `go.mod`) and the C toolchain that Fyne needs for the GUI; the steps per OS are in the README under [Building](README.md#building). Then:

```sh
go build ./cmd/openveconfig-cli
go build ./cmd/openveconfig-gui
go test ./...
```

No hardware is needed for most work:

- Every CLI command that talks to a device takes `--simulator`. For `system` commands it is a VE.Bus system of two units in parallel.
- In the GUI, the connection dialog offers **Demo (no hardware)** (two devices) and **Demo: a VE.Bus system of three new units**.
- The simulator (`pkg/simulator`) can inject faults; tests use it to cover failure paths.

Some tests check real files that are never committed. They skip unless their variable points to a directory of your own files:

| Variable | Files | Test |
|---|---|---|
| `RVSC_TESTDATA` | `.rvsc`/`.rvms` from a GX device or VRM | `go test ./pkg/rvsc/ -run TestRealFiles -v` |
| `VMS_TESTDATA` | `.vms` from VE.Bus System Configurator | `go test ./pkg/vms/ -run TestRealFile -v` |
| `VFF_TESTDATA` | `.vff` firmware files | `go test ./pkg/vff/ -run TestRealFiles -v` |

The tests log no serial numbers or long IDs, so their output can be pasted into an issue.

## Code

- Run `gofmt`, `go vet ./...` and `go test ./...` before you push; CI runs them on Linux, macOS and Windows.
- Protocol, file-format and system-configuration code needs tests. Use the simulator or synthetic fixtures built by the test itself (see `internal/rvsctest`, `internal/vfftest`), never files made by the original tools.
- Say where every protocol or format fact comes from, in a comment next to it: a public Victron document (with section), a community source (with URL, marked community-reported), our own hardware, reverse-engineered (with the issue), or our own choice ("ours"). Points that are not known stay marked as open; do not fill them in by guesswork.
- Keep changes small and in the style of the code around them.
- GUI text goes through `tr`/`trn` and into every catalog; see [ADR 0001](docs/adr-0001-localization.md). `TestCatalog`, `TestTranslationsComplete` and `TestNoHardCodedUIStrings` enforce it.
- Code adapted from other projects must be MIT-compatible, attributed where it is used and listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Issues and pull requests

- The plan is in the milestones (M0 to M6, Cross-cutting) and the [roadmap](https://github.com/bolchisb/openveconfig/issues/48); each milestone has an epic with its task issues.
- Label issues by area (`area:protocol`, `area:settings`, `area:system-config`, `area:gui`, `area:firmware`, …) and add `risk:high` to anything that writes to devices.
- Branches: only maintainers create `feature/*` branches. Automated agents and bots use other names, such as `m5/...`, `fix/...` or `ci/...`.
- A pull request links its issue, describes what was tested (simulator, synthetic files, real files, hardware) and fills in the checklist of the [template](.github/pull_request_template.md).
- Results on real hardware go into the results table of [docs/HARDWARE-TESTING.md](docs/HARDWARE-TESTING.md) and onto the issue; a result that contradicts a spec gets its own issue.
