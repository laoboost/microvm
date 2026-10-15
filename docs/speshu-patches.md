# SpeShu fork patch parity audit — v0.6.5-speshu.1

Release commit: `76021631ceccaa8452c34ca0aa2f2b5cede052e6` (this file's first revision is
in that commit; the SHA/verification block below was corrected one commit later on main —
the tagged copy's Release section still names the pre-audit `8ffc4196`). Base: upstream `aerol-ai/microvm`
tip `80518cf8` (v0.6.5 tag `b32d3575` plus 103 post-v0.6.5 upstream commits merged in
`20e75009`), i.e. this release carries upstream v0.6.5 **and** everything upstream main
had merged through 2026-10-15.

Method: `git log --no-merges 80518cf8..HEAD`, deduplicated — the pre-sync main and the
sync branch each carried the same patch set, so many patches appear twice in history
(e.g. `23971a2c`/`098ef4e2` are one patch). Verdicts below are per patch-group.

## Verdicts

| # | Patch group | Fork commits (deduped) | Verdict |
|---|---|---|---|
| 1 | Mount lifecycle: teardown on die/stop/oom; skip re-mount during stop | `11dafc27` | **keep** — no upstream equivalent (v0.6.0–v0.6.5 changelog has none); prevents stale host mounts after sandbox stop. |
| 2 | Mount reliability: sweep-vs-start race, parallel establishment, pinned throughput, `allow_delete` | `98017426` | **keep** — fork-only. |
| 3 | Restart sandbox on mount crash (in-place respawn cannot heal a live VM) | `bfe0f2da` | **keep** — fork-only. |
| 4 | Mount credential-failure detection: scan live mount-process output; structured warn + expvar counter; grafana fixture | `c3b5da08`, `6f27b2ad`, `13fa40ef` | **keep** — fork-only observability. |
| 5 | Cred-file unlink after supervised restart; probe wait outside manager lock | `9fc36a92` | **keep** — fork-only. |
| 6 | S3 adapter structured options, `extra_args` denylist, source guard | `23971a2c` | **keep (extends upstream)** — upstream v0.6.3 `2f42813a` (GHSA-5pqm-4fh4-585q) already rejects option-shaped/dash-prefixed sources at spec level; the fork adds the structured-options adapter path and denylist on top. Post-merge both live in `pkg/mounts/adapters/source_guard.go` + `s3_options.go`; the upstream dash guard is retained, not reverted. |
| 7 | Reject dash-prefixed mount sources across adapters + spec validation | `0765164d` | **drop (subsumed by v0.6.3)** — upstream `2f42813a` implements exactly this (`checkSource`, leading `-`, control bytes); merged tree keeps upstream's version plus the fork's grammar checks. |
| 8 | Tenant image-ref validator (vendored `distribution/reference`) + wiring | `4a3750e8`, `e3d3771a` | **keep** — no upstream equivalent. |
| 9 | Per-sandbox pids limit (default 1024, parked containers included) | `fb074fc6` | **keep** — no upstream equivalent. |
| 10 | WASM wasmtime hardening: no host env/stream inheritance, fail-closed memory limit, mkstemp outputs | `4284cf23` | **keep** — upstream v0.6.3 fail-closed the *toolhost* exec routes; this hardens the *engine*, a different surface. No v0.6.3 fix reverted. |
| 11 | toolboxd fail-closed auth on empty `SB_TOOLBOX_TOKEN` (`SB_TOOLBOX_AUTH_OPTIONAL` escape hatch) | `cfd600b2` | **keep** — no upstream equivalent. |
| 12 | Reject L4 port ranges overlapping daemon listener ports | `c8f65c76` | **keep** — no upstream equivalent. |
| 13 | Deep-review remediation: host escape, cluster divergence, secret exposure | `0d292d85` | **keep** — fork-only hardening; reviewed against v0.6.3–v0.6.5 fixes, no overlap conflict. |
| 14 | toolboxd `Session.finish` self-deadlock / `ExitInfo` | `e6742c62` | **keep** — fork-only correctness fix. |
| 15 | Exec-stream guardrail (env filter + tests) | `8d4a387a` | **keep** — fork-only. |
| 16 | S3 mount error handling (`s3.go` retries/tests) | `b2eea19d` | **keep** — fork-only; note `40a6b8a2` (its twin) also reworked `scripts/install.sh`; that install.sh rework was superseded by upstream v0.6.5 #627's GitHub-release Caddy install, and the merged tree carries upstream's version. |
| 17 | Daytona SDK 0.177 + runtime-spec `<1.3` pin for go1.27 build | `b566c8d1` | **drop (superseded)** — the go 1.27 toolchain issue upstream resolved itself; the merged tree uses upstream's `runtime-spec v1.3.0` with `845e347d` adapting pids resize to the v1.3 pointer `Limit`, and Daytona SDK 0.177 is upstream's own version now. Keeping the pin would *revert* upstream. |
| 18 | Bulk dependency updates | `5811e3f4`, `8ec1262c` | **drop (subsumed)** — upstream dependabot merges through `80518cf8` cover them. |
| 19 | Test migration/alignment (incarnation-fenced, C6a, mTLS semantics; fakes; follower leader-view wait; mount/pids/cluster-cap adaptations; gofmt) | `e8e4b0ab`, `c3e1adc3`, `7d75304d`, `7de55b68`, `1e66e3c3`, `3ced0a7d`, `8404f4c3`, `1da215b4`, `845e347d`, `8b516d9f`, `214992ca` | **keep** — keeps the suite green on the merged tree. |
| 20 | Mount re-establish contract test (start-after-stop) | `4a7e9333` | **keep** — pins patch group 1/2 behavior. |
| 21 | `.gitignore` local `.worktrees/` | `91e1b660` | **keep** — trivial, no upstream impact. |

## Security non-reversion check

- v0.6.3 mount-source command injection (GHSA-5pqm-4fh4-585q): dash/control-char
  rejection present in the merged tree (`pkg/mounts/adapters/source_guard.go`,
  `pkg/models`); fork patch 6 extends rather than replaces it. ✔
- v0.6.3 WASM toolhost host-exec fail-closed: untouched by kept patches (patch 10
  hardens a different layer, engine-level). ✔
- v0.6.3 sandbox-id/bundle-digest/module-ref path checks, v0.6.4 build-context tar
  bounds, v0.6.5 name rejection + `CAP_NET_RAW` removal: none of the kept patches
  touch those validators or capabilities. ✔
- The only drops (7, 17, 18) remove code the upstream already carries in a form the
  merged tree uses; none removes a security fix.

## SDK / wire-surface parity (drives task 006)

- **`go.mod` build inputs: identical to upstream tip `80518cf8`.** Daytona SDK is
  `v0.177.0` and `runtime-spec` is `v1.3.0` in both; no fork pin survives (see
  verdicts 17–18). Task 006 needs **no pin** — the fork's build inputs are
  upstream's, and pids-resize code is already adapted to runtime-spec v1.3.
- **`pkg/models`: identical non-test code.** Fork-only delta is two test files
  (`mounts_sshfs_source_test.go`, `mounts_test.go`).
- **`sdk/go`: diverged.** The fork keeps its deep-review changes where upstream
  evolved differently: token/secret redaction (`sdk/go/pkg/types/redact.go`,
  `RedactToken`, redacted `String()`), registry push credentials moved from the
  `X-Registry-Auth` header into the build request body, and the apiclient
  client/session changes conflict-resolution kept from the fork side in merge
  `20e75009`. A consumer should treat the fork's SDK as the source of truth when
  talking to a fork server; upstream SDK and fork SDK are not interchangeable.
- **go1.27 consumer build: verified.** A scratch module on go 1.27.0 with
  `replace github.com/aerol-ai/microvm => <fork>` compiles and runs an import of
  `sdk/go/pkg/microvm` + `sdk/go/pkg/types` (`NewClientWithConfig`,
  `RedactToken`). The v0.5.5-speshu.1 go1.27 pins are not needed.

## Release

- Tag: `v0.6.5-speshu.1`
- Release commit: `76021631ceccaa8452c34ca0aa2f2b5cede052e6` (tag `v0.6.5-speshu.1`)
- Suite state at release commit: `go build ./...` + `go test ./... -parallel 8` green
  (run recorded in the task log; the release workflow re-runs the suite before build).
- Asset URLs (laoboost/microvm, tag-scoped copies also attached as
  `v0.6.5-speshu.1_<asset>` per the release workflow):
  - https://github.com/laoboost/microvm/releases/download/v0.6.5-speshu.1/install.sh
  - https://github.com/laoboost/microvm/releases/download/v0.6.5-speshu.1/sandboxd_linux_amd64
  - https://github.com/laoboost/microvm/releases/download/v0.6.5-speshu.1/toolboxd_linux_amd64
  - https://github.com/laoboost/microvm/releases/download/v0.6.5-speshu.1/caddy_linux_amd64
  - https://github.com/laoboost/microvm/releases/download/v0.6.5-speshu.1/checksums.txt
  - https://github.com/laoboost/microvm/releases/download/v0.6.5-speshu.1/v0.6.5-speshu.1.intoto.jsonl
  (checksums verified locally against the attached binaries per
  RELEASE-VERIFICATION.md — `sha256sum -c checksums.txt --ignore-missing` passed for
  install.sh, sandboxd/toolboxd/caddy linux_amd64; provenance bundle
  `v0.6.5-speshu.1.intoto.jsonl` inspected directly: builder
  `https://github.com/laoboost/microvm/.github/workflows/release.yml@refs/tags/v0.6.5-speshu.1`,
  source gitCommit `76021631…`, and the sandboxd/install.sh subject digests match the
  downloaded assets. The DSSE signature itself was not verified locally — this
  machine's `gh` (2.46.0) has no `attestation` command and no cosign; use
  `gh attestation verify … --repo laoboost/microvm --signer-workflow
  laoboost/microvm/.github/workflows/release.yml` from a newer gh. The release
  workflow run: https://github.com/laoboost/microvm/actions/runs/38694053569)
