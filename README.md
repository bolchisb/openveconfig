<div align="center">

# OpenVeConfig

### An open-source port of Victron Energy's VE.Bus configuration tools.

**Configure Multi, Quattro and MultiPlus inverter/chargers without the original Windows-only software.**

[![CI](https://github.com/bolchisb/openveconfig/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/bolchisb/openveconfig/actions/workflows/ci.yml?query=branch%3Amain)
[![Tests](https://img.shields.io/endpoint?url=https%3A%2F%2Fraw.githubusercontent.com%2Fbolchisb%2Fopenveconfig%2Fbadges%2Ftests.json)](https://github.com/bolchisb/openveconfig/actions/workflows/ci.yml?query=branch%3Amain)
[![License: MIT](https://img.shields.io/badge/license-MIT-yellow.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Platforms](https://img.shields.io/badge/platforms-Windows%20%7C%20Linux%20%7C%20macOS-blue.svg)](#about)
[![Status](https://img.shields.io/badge/status-work%20in%20progress-orange.svg)](#status)
[![Last commit](https://img.shields.io/github/last-commit/bolchisb/openveconfig)](https://github.com/bolchisb/openveconfig/commits)
[![Issues](https://img.shields.io/github/issues/bolchisb/openveconfig)](https://github.com/bolchisb/openveconfig/issues)
[![Stars](https://img.shields.io/github/stars/bolchisb/openveconfig?style=flat)](https://github.com/bolchisb/openveconfig/stargazers)

[About](#about) · [Status](#status) · [Contributing](#contributing) · [License](#license)

</div>

> [!IMPORTANT]
> This is an independent community project. It is not affiliated with, endorsed by, or supported by Victron Energy. "Victron Energy", "VE.Bus" and "VEConfigure" are trademarks of their respective owner. The software is provided as is, without any warranty. Writing wrong settings to an inverter/charger can damage equipment, so you use it entirely at your own risk.

## About

OpenVeConfig reimplements the functionality of Victron's VE.Bus configuration suite (VEConfigure, VE.Bus Quick Configure, VE.Bus System Configurator and VEFlash) as an open-source Go project. It talks to VE.Bus devices over the MK2/MK3 interface, based on Victron's public MK2 protocol documentation and on analysis of the original applications.

The original tools are Windows-only. Porting them to Go lets us ship a native build for every major platform (Windows, Linux and macOS) from a single codebase.

## Status

Early development. Nothing is usable yet.

## Serial ports

The MK2/MK3 interface shows up as a serial port. Likely interfaces are flagged by USB ID: FTDI FT-X `0403:6015` (MK3-USB) and Prolific PL2303 `067B:2303` (USB-RS232 converter for the RS232 MK2.2). The USB ID of the MK2-USB is unknown, so other ports are probed by protocol.

- **Linux:** add your user to the `dialout` group (`sudo usermod -aG dialout $USER`), then log out and back in.
- **macOS:** use the `/dev/cu.*` node, not `/dev/tty.*`. The `tty.*` dial-in node expects a carrier-detect signal; Apple recommends the `cu.*` callout node for almost all uses.
- **Windows:** use the plain name, such as `COM3` or `COM12`.

## Command-line tool

`openveconfig-cli` is for diagnostics, scripting and hardware tests. Every command that talks to a device takes `--port NAME` or `--simulator` (a built-in simulator with two synthetic devices, so no hardware is needed), plus `--address N`, `--baud auto|2400|115200` and `--verbose` (a timestamped hex log of every frame on stderr, for bug reports). Run `openveconfig-cli <command> --help` for details.

```sh
openveconfig-cli ports --probe                       # list ports; --probe sends one 'V' request to each
openveconfig-cli --simulator version                 # interface and device firmware, devices found
openveconfig-cli read-setting --port COM3 2          # raw and scaled value, scale, offset, default, min, max
openveconfig-cli read-ram --port COM3 4              # raw and scaled value of a RAM variable
openveconfig-cli write-setting --port COM3 --yes 2 14.5        # value in display units
openveconfig-cli write-setting --port COM3 --yes --raw 2 1450  # value as stored on the device
openveconfig-cli --simulator dump > dump.json        # all settings and RAM variables as JSON
openveconfig-cli apply --port COM3 dump.json         # show what writing the file's settings would change
openveconfig-cli apply --port COM3 --address 0,1 --yes dump.json  # write the changed settings to two units
openveconfig-cli rvsc-show backup.rvms               # units, firmware and settings of a .rvsc/.rvms file
openveconfig-cli rvsc-diff old.rvms new.rvms         # settings that differ between two files
openveconfig-cli rvsc-diff --port COM3 --unit HQ1234ABCDE backup.rvms   # does the file match the device, and what differs
openveconfig-cli rvsc-set backup.rvsc 2 14.6 -o edited.rvsc             # change one setting offline
openveconfig-cli rvsc-apply --port COM3 --unit HQ1234ABCDE backup.rvms  # dry run; --yes writes the unit's settings
```

`write-setting` changes a live device: it is refused without `--yes`, checks the value against the minimum and maximum the device reports, and reads the setting back afterwards.

`apply` is a dry run without `--yes`. With it, it writes only the settings that differ from the device (`--all`: every setting), after checking the device limits and validation (`--force` skips validation only), reads each one back, and lists the settings that need a device reset.

The `rvsc-*` commands work on the `.rvsc`/`.rvms` files of a GX device's backup and VRM Remote VEConfigure. The format is community-reported. `rvsc-set` changes only the setting's bytes and the checksum. `rvsc-apply` refuses a file that does not match the device (firmware, setting info, number of units) and writes only settings, not assistants. Both refuse grid-code settings unless `--allow-grid-code` is given.

## Graphical tool

`openveconfig-gui` is the desktop application. The start screen offers to connect a device, open a settings file (with the recent files) or try the demo. **Connect…** opens the connection dialog: choose a serial port (or **Auto-detect**, or **Demo** for the built-in simulator, which is marked in the header and the window title while active). The header shows the unit being edited and where it comes from; a sidebar leads to the pages. It offers:

- **Settings pages** (General, Inverter, Charger, Virtual switch, Advanced): get the settings from a device, edit them offline (unsent changes, and settings that need a reset or a password, are marked; the sidebar counts the unsent changes of each page), review, discard, send or save the changes from the bar under the page, send the changed or all settings with a per-setting report, reset all to defaults, find a setting by number or name (Ctrl+F), and open/save `.rvsc` files (Ctrl+O, Ctrl+S). Only the settings the device reports as supported are shown.
- **Grid page**: the grid code and the AC input limits. The device enforces the installer password for grid codes; the program never enters or bypasses one. Grid code parameters can be viewed and edited. A changed grid code and changed grid parameters are saved to a settings file with newly computed check words (reverse-engineered), to apply through a GX device; sending them straight to a device is disabled until the write order has been verified on hardware.
- **Overview** page with the connection and live values, and **Tools > VE.Bus monitor** with per-phase values.
- **Tools > Remote panel**: on/off/charger only/inverter only, the AC input current limit (where the device lets a panel override it) and the LEDs.
- English and Romanian (**Options > Language**, applied at the next start), and a light and a dark theme that follow the OS or are chosen in **Options > Theme**.

## Building

Requires [Go](https://go.dev/dl/) (version from `go.mod`). The GUI uses [Fyne](https://fyne.io), which needs cgo and a C toolchain:

- **Windows:** a MinGW-w64 gcc on `PATH` (for example from [MSYS2](https://www.msys2.org/): `pacman -S mingw-w64-x86_64-toolchain`).
- **macOS:** Xcode command line tools (`xcode-select --install`).
- **Linux (Debian/Ubuntu):** `sudo apt-get install gcc libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev`

```sh
git clone https://github.com/bolchisb/openveconfig.git
cd openveconfig
go build ./cmd/openveconfig-cli
go build ./cmd/openveconfig-gui
go test ./...
```

CI builds both binaries on native runners for windows/amd64, darwin/arm64, darwin/amd64, linux/amd64 and linux/arm64 (see `.github/workflows/ci.yml`).

Releases are made by pushing a `v*` tag (for example `v0.1.0`; a tag with a `-`, such as `v0.1.0-rc1`, becomes a pre-release) that points to a commit on `main`. The release workflow (`.github/workflows/release.yml`) refuses tags on other branches, runs the tests, and attaches the archives for all five platforms plus `SHA256SUMS.txt` to the GitHub release. The macOS binaries are not signed or notarized yet.

## Contributing

Suggestions, bug reports and ideas are welcome. Open an [issue](https://github.com/bolchisb/openveconfig/issues) or send a pull request.

This is a clean-room, interoperability-only project. Before contributing, read the [clean-room and licensing policy](docs/CLEAN-ROOM.md): never paste decompiled code, data extracted from Victron software, firmware or help text into code, issues or pull requests.

To add or improve a GUI translation, follow the rules in [ADR 0001](docs/adr-0001-localization.md).

## License

Released under the [MIT License](LICENSE).
