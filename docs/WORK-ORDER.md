# Work order

The order the open issues that do **not** need hardware are worked through, easiest
first, so that work can be picked up again without re-deciding what comes next.

Issues blocked on a real unit carry `needs:hardware-test` and are not in this list;
they wait for a session under the [hardware test plan](HARDWARE-TESTING.md) (#3).
To see this list on GitHub:

```
is:issue state:open -label:needs:hardware-test
```

Each entry says what is actually left, checked against the code rather than against
the issue title. When an issue is finished: update [the changelog](CHANGELOGS/CHANGELOG.md)
if the repository changed, record on the issue what was verified, and close it.

| # | Issue | What is left | State |
|--:|---|---|---|
| 1 | #77 RE spec: assistant area transfer | Spec written privately, with its open points tracked as hardware checks on #3 | **done** 2026-10-22 |
| 2 | #35 GUI: System Configurator | Whole scope implemented, seven tests; all acceptance criteria were already met | **done** 2026-10-22 |
| 3 | #8 RE: Assistants | Specs written; the ESS build matches a real unit byte for byte; the decision on how packages reach users is in CONTRIBUTING | **done** 2026-10-22 |
| 4 | #48 Roadmap / tracking | Epics ticked (#47, M1, M2, M3) and the working order linked. Stays open: it tracks the whole project | **done** 2026-10-22 |
| 5 | #32 Assistants: compiler | Supported list measured and in the README, README brought up to date. Left: the byte-identical check, which needs the read-back function `RB3` in the assembler, or the backup `essAnswers` belongs to | |
| 6 | #33 GUI: Assistants tab | Replacing an assistant in the file is implemented and tested. Left: removing and reordering, and the read-back fidelity that waits on `RB3` (#32) | |
| 7 | #72 pkg/vms on real files | The test is small (follow `RVSC_TESTDATA`). Blocked on sample `.vms` files made with the original tool | |
| 8 | #9 RE: Grid codes, access levels, passwords | The per-code parameter lists exist in `pkg/gridcode`. The unlock flow and the access-level semantics are thin and partly wait on hardware | |
| 9 | #12 RE: VFF container and reprogram protocol | The container is done and the reprogram protocol is written up privately. Left: the device/firmware support matrix and the decision on interface self-update | |
| 10 | #40 Epic: M0 | Closes once #8, #9 and #12 are finished | |

## Why this order

The first four are mostly verification: the work was done and the issues were never
updated. They cost little and take the list from ten to six. After them the remaining
work is real implementation (#32, #33), one item that depends on files only the user
can produce (#72), and the two reverse-engineering issues that feed the firmware
milestone (#9, #12), which is the highest-risk area and is left last.
