# Clean-room and licensing policy

This repository is public. It contains an independent, interoperability-only implementation of the PC side of Victron's VE.Bus configuration tools. This policy defines what may and may not enter the repository, so that the project never redistributes Victron's code, data or other copyrighted material.

It applies to everything in the repository and on the project's GitHub pages: code, comments, documentation, test fixtures, issues, pull requests and commit messages.

## Allowed sources

- **Victron's public documents**, for example "Interfacing with VE.Bus products – MK2 Protocol 3.14", product manuals, the VEConfigure manual and the data communication whitepaper. Cite the document and section.
- **Observations of our own hardware**, for example serial traffic captured with this project's own tool (`veconfig-cli --verbose`) on equipment we own.
- **MIT-compatible community code**, with attribution (see [Third-party code](#third-party-code)).
- **Public community findings** (forum posts, project write-ups), cited by URL and marked as community-reported where they are not confirmed by Victron.

## Never committed

None of the following may appear in the repository, in issues or in pull requests, in whole or in part:

- Decompiled or disassembled code from Victron software, or code transcribed from it.
- Data tables, payloads, images, icons, help text, strings or translations extracted from Victron binaries. That includes built-in demo device descriptors and setting/variable tables. Exception: interoperability facts learnt through reverse engineering (setting, flag and RAM variable IDs, bit positions, scales, limits, enum values, which settings belong together) may be committed when they are restated in our own tables, with our own names and wording, and marked as reverse-engineered (decision of 2026-10-19).
- Victron firmware files (`.vff`, `.vfd`) and Victron assistant packages, or excerpts of them.
- Private reverse-engineering notes, tool output, Ghidra projects, or screenshots of decompiled code.
- Local filesystem paths of contributors' machines.

Test fixtures and simulator profiles are synthetic or taken from our own tool's captures of our own hardware. Captures of the original tools' serial traffic may inform a behavioural spec, but are not committed as fixtures: they can carry Victron data such as settings tables, assistant code or firmware.

## Process

1. Reverse-engineering findings are turned into **behavioural specs written in our own words** (what is sent, when, what the device answers, what the user sees) before anyone implements them. A spec describes behaviour; it never contains code, tables or text copied from a binary.
2. Each statement in a spec is marked as one of: public document, community-reported, observed on our hardware, or reverse-engineered.
3. Reverse engineering and implementation are kept separate where practical: the person or agent implementing a feature works from the public documents and the written spec, not from decompiled output.
4. Reverse-engineering work happens outside this repository and is never pushed to it.

## Third-party code

- **MIT and other permissive licences** (for example yvesf/ve-ctrl-tool, xcellsior/ve-bus-programming, kylehart/mk2vsc, talas9/rvsc-tools, victronenergy/venus-platform; BSD-3-Clause libraries such as go.bug.st/serial): may be reused or adapted. Add an attribution comment at the reuse site and an entry with the licence text in [`THIRD_PARTY_NOTICES.md`](../THIRD_PARTY_NOTICES.md).
- **GPL-3.0 projects** (for example tomjnixon/mk2.py) and **projects with no stated licence**: reference only. Reading them to understand behaviour is fine; copying or transcribing their code is not.
- Check the licence of the exact version you use, not only the project's README.

## Access control

Password, access-level and key-code features accept only values that the user supplies. The project never bypasses, brute-forces, derives or ships such values.

## Legal notes and open points

Nobody on this project has given legal advice. The points below are recorded so that they can be reviewed; they are not a legal opinion.

- **VEConfigure EULA:** not found. No licence agreement for VEConfigure, VE.Bus Quick Configure, VE.Bus System Configurator or VEFlash is published on Victron's website (checked 2026-10-19). The installer may show one; it has not been reviewed.
- **Victron Terms & Conditions of Sale** (©2020, linked as "Terms of use" from the software download page, https://www.victronenergy.com/upload/documents/VICTRON-ENERGY-B.V.-TERMS-&-CONDITIONS-OF-SALE.pdf), reviewed 2026-10-19:
  - Art. 4 "Rights in software, documentation and intellectual property": the Buyer shall not "(a) modify, adapt, alter, translate, or create derivative works from any software residing in or provided by Victron Energy in conjunction with any Products; … (d) reverse assemble, decompile, disassemble, or otherwise attempt to derive the source code for such software without written authorization from Victron Energy except as explicitly allowed under applicable law."
  - Art. 5 "Confidentiality": technical data disclosed by Victron to the Buyer is confidential.
  - Art. 10: Dutch law, courts of Amsterdam.
  - Open question: whether these terms, written for the sale of products, apply to the free PC tools at all.
- **EU Directive 2009/24/EC** (legal protection of computer programs), https://www.legislation.gov.uk/eudr/2009/24/contents:
  - Art. 6(1) allows a licensee to reproduce and translate code without the rightholder's authorisation when that is indispensable to obtain the information needed for the interoperability of an independently created program, the information was not readily available, and the acts are confined to the necessary parts.
  - Art. 6(2) forbids using that information for other goals, giving it to others unless necessary for interoperability, or using it to develop a program substantially similar in its expression.
  - Art. 8 makes contractual provisions contrary to Art. 6 null and void.
  - Other projects (for example talas9/rvsc-tools) cite these articles. Whether and how they apply to this project has not been reviewed by a lawyer.
- **Victron's public statements:** the data communication whitepaper says third-party interfacing must go through the MK2/MK3, Modbus TCP or the VE.Bus to CAN/NMEA2000 interfaces; this project uses the MK2/MK3. On reusing Victron's MIT reference code, Victron staff wrote "We don't require more than the MIT license itself does" (https://communityarchive.victronenergy.com/questions/51744/software-license.html); that statement does not cover the closed tools.
- **Recommendation:** get a legal review before publishing any feature whose behaviour was learnt only through reverse engineering (rather than from public documents, community findings or our own hardware observations).

## Checklist for contributions

Every pull request confirms, through the [pull request template](../.github/pull_request_template.md), that it contains no Victron-extracted content.
