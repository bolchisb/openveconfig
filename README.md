<div align="center">

# VEConfigure Ported

### An open-source port of Victron Energy's VE.Bus configuration tools.

**Configure Multi, Quattro and MultiPlus inverter/chargers without the original Windows-only software.**

[![License: MIT](https://img.shields.io/badge/license-MIT-yellow.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Platforms](https://img.shields.io/badge/platforms-Windows%20%7C%20Linux%20%7C%20macOS-blue.svg)](#about)
[![Status](https://img.shields.io/badge/status-work%20in%20progress-orange.svg)](#status)
[![Last commit](https://img.shields.io/github/last-commit/bolchisb/victron-veconfigure-ported)](https://github.com/bolchisb/victron-veconfigure-ported/commits)
[![Issues](https://img.shields.io/github/issues/bolchisb/victron-veconfigure-ported)](https://github.com/bolchisb/victron-veconfigure-ported/issues)
[![Stars](https://img.shields.io/github/stars/bolchisb/victron-veconfigure-ported?style=flat)](https://github.com/bolchisb/victron-veconfigure-ported/stargazers)

[About](#about) · [Status](#status) · [Contributing](#contributing) · [License](#license)

</div>

> [!IMPORTANT]
> This is an independent community project. It is not affiliated with, endorsed by, or supported by Victron Energy. "Victron Energy", "VE.Bus" and "VEConfigure" are trademarks of their respective owner. The software is provided as is, without any warranty. Writing wrong settings to an inverter/charger can damage equipment, so you use it entirely at your own risk.

## About

VEConfigure Ported reimplements the functionality of Victron's VE.Bus configuration suite (VEConfigure, VE.Bus Quick Configure, VE.Bus System Configurator and VEFlash) as an open-source Go project. It talks to VE.Bus devices over the MK2/MK3 interface, based on Victron's public MK2 protocol documentation and on analysis of the original applications.

The original tools are Windows-only. Porting them to Go lets us ship a native build for every major platform (Windows, Linux and macOS) from a single codebase.

## Status

Early development. Nothing is usable yet.

## Serial ports

The MK2/MK3 interface shows up as a serial port. Likely interfaces are flagged by USB ID: FTDI FT-X `0403:6015` (MK3-USB) and Prolific PL2303 `067B:2303` (USB-RS232 converter for the RS232 MK2.2). The USB ID of the MK2-USB is unknown, so other ports are probed by protocol.

- **Linux:** add your user to the `dialout` group (`sudo usermod -aG dialout $USER`), then log out and back in.
- **macOS:** use the `/dev/cu.*` node, not `/dev/tty.*`. The `tty.*` dial-in node expects a carrier-detect signal; Apple recommends the `cu.*` callout node for almost all uses.
- **Windows:** use the plain name, such as `COM3` or `COM12`.

## Command-line tool

`veconfig-cli` is for diagnostics, scripting and hardware tests. Every command that talks to a device takes `--port NAME` or `--simulator` (a built-in simulator with two synthetic devices, so no hardware is needed), plus `--address N`, `--baud auto|2400|115200` and `--verbose` (a timestamped hex log of every frame on stderr, for bug reports). Run `veconfig-cli <command> --help` for details.

```sh
veconfig-cli ports --probe                       # list ports; --probe sends one 'V' request to each
veconfig-cli --simulator version                 # interface and device firmware, devices found
veconfig-cli read-setting --port COM3 2          # raw and scaled value, scale, offset, default, min, max
veconfig-cli read-ram --port COM3 4              # raw and scaled value of a RAM variable
veconfig-cli write-setting --port COM3 --yes 2 14.5        # value in display units
veconfig-cli write-setting --port COM3 --yes --raw 2 1450  # value as stored on the device
veconfig-cli --simulator dump > dump.json        # all settings and RAM variables as JSON
```

`write-setting` changes a live device: it is refused without `--yes`, checks the value against the minimum and maximum the device reports, and reads the setting back afterwards.

## Building

Requires [Go](https://go.dev/dl/) (version from `go.mod`). The GUI uses [Fyne](https://fyne.io), which needs cgo and a C toolchain:

- **Windows:** a MinGW-w64 gcc on `PATH` (for example from [MSYS2](https://www.msys2.org/): `pacman -S mingw-w64-x86_64-toolchain`).
- **macOS:** Xcode command line tools (`xcode-select --install`).
- **Linux (Debian/Ubuntu):** `sudo apt-get install gcc libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev`

```sh
git clone https://github.com/bolchisb/victron-veconfigure-ported.git
cd victron-veconfigure-ported
go build ./cmd/veconfig-cli
go build ./cmd/veconfig-gui
go test ./...
```

CI builds both binaries on native runners for windows/amd64, darwin/arm64, darwin/amd64, linux/amd64 and linux/arm64 (see `.github/workflows/ci.yml`).

## Contributing

Suggestions, bug reports and ideas are welcome. Open an [issue](https://github.com/bolchisb/victron-veconfigure-ported/issues) or send a pull request.

This is a clean-room, interoperability-only project. Before contributing, read the [clean-room and licensing policy](docs/CLEAN-ROOM.md): never paste decompiled code, data extracted from Victron software, firmware or help text into code, issues or pull requests.

## License

Released under the [MIT License](LICENSE).
