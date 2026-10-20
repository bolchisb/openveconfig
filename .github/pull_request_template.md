## Summary

<!-- What does this change and why? Link the issue (e.g. "Closes #15"). -->

## Tested with

<!-- Simulator, synthetic files, real files (which variable), hardware (setup, see docs/HARDWARE-TESTING.md). -->

## Checklist

- [ ] Contains no Victron-extracted content: no decompiled code, extracted data, firmware, help text or translations ([clean-room policy](https://github.com/bolchisb/openveconfig/blob/main/docs/CLEAN-ROOM.md))
- [ ] Code adapted from third-party projects is MIT-compatible, attributed at the reuse site and listed in `THIRD_PARTY_NOTICES.md`
- [ ] No local filesystem paths
- [ ] Protocol and format facts say where they come from (public document, community, our hardware, reverse-engineered, ours)
- [ ] Write or flash features were not tested on equipment that others depend on; hardware results follow [docs/HARDWARE-TESTING.md](https://github.com/bolchisb/openveconfig/blob/main/docs/HARDWARE-TESTING.md), or the PR says they are still to do
- [ ] GUI text goes through `tr`/`trn` and is in every catalog
- [ ] `gofmt`, `go vet` and `go test ./...` pass
