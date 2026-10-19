# SpeShu fork patch parity audit — v0.6.7-speshu.1

Release: tag `v0.6.7-speshu.1` on the commit that carries this file (its full
SHA and the asset-verification block are recorded on `main` in the post-tag
correction, as for v0.6.5-speshu.1). Base: upstream `aerol-ai/microvm`
tag **v0.6.7** = `88120fdb` (itself v0.6.6 `2f0f6371` + the v0.6.5 line + 57
post-v0.6.6 commits), merged into the fork in `bf69b18f`. The 222 upstream
commits **after** the v0.6.7 tag (through `15847f82`, the unreleased
identity/SSO train — PR #740–775: OIDC, Keycloak, PAT scopes, MCP OAuth,
audit-caller, permissions, dashboard) are **intentionally not carried**; they
wait for their own upstream release.

Method: `git log --no-merges` over the fork's patch range, deduplicated as for
v0.6.5-speshu.1 (the pre-sync main and the sync branch carry the same patch
set). Verdicts below are per patch-group against upstream v0.6.6 + v0.6.7.

## Verdicts

| # | Patch group | Fork commits (deduped) | Verdict |
|---|---|---|---|
| 1 | Mount lifecycle: teardown on die/stop/oom; skip re-mount during stop | `11dafc27` | **keep** — upstream `80518cf8..v0.6.7` touches nothing under `pkg/mounts`; no v0.6.6/v0.6.7 changelog item is a mount change. Contract test `internal/service/mount_teardown_test.go` green. |
| 2 | Mount reliability: sweep-vs-start race, parallel establishment, pinned throughput, `allow_delete` | `98017426` | **keep** — fork-only. |
| 3 | Restart sandbox on mount crash | `bfe0f2da` | **keep** — fork-only (`internal/service/mount_crash_restart_test.go`). |
| 4 | Mount credential-failure detection: scan live mount-process output; structured warn + expvar counter | `c3b5da08`, `6f27b2ad`, `13fa40ef` | **keep** — fork-only observability (`pkg/mounts/metrics.go`, `cred_failure_test.go`). |
| 5 | Cred-file unlink after supervised restart; probe wait outside manager lock | `9fc36a92` | **keep** — fork-only (`pkg/mounts/manager.go`). |
| 6 | S3 adapter structured options, `extra_args` denylist, source guard | `23971a2c` | **keep (extends upstream)** — upstream v0.6.3 `2f42813a` source-injection guard is retained; the fork adds `s3_options.go` on top. Upstream v0.6.6 `b3a43ef8` (registry-only build bases) touches the build path, not mounts. |
| 7 | Reject dash-prefixed mount sources across adapters + spec validation | `0765164d` | **drop (subsumed by v0.6.3)** — unchanged from the v0.6.5-speshu.1 audit. |
| 8 | Tenant image-ref validator (vendored `distribution/reference`) + wiring | `4a3750e8`, `e3d3771a` | **keep** — lives at `internal/service/imageref.go`; upstream v0.6.6's registry-only base policy (`b3a43ef8`, `pkg/docker/build.go`) is a different layer and is also present. |
| 9 | Per-sandbox pids limit (default 1024, parked containers included) | `fb074fc6` | **keep** — no upstream equivalent. |
| 10 | WASM wasmtime hardening: no host env/stream inheritance, fail-closed memory limit, mkstemp outputs | `4284cf23` | **keep** — engine-level hardening in `internal/runtime/wasm/resident.go`; upstream v0.6.6's WASM outside-work-dir path refusal is a different layer (validators + `wasm_module_api_test.go`), both present. |
| 11 | toolboxd fail-closed auth on empty `SB_TOOLBOX_TOKEN` (`SB_TOOLBOX_AUTH_OPTIONAL` escape hatch) | `cfd600b2` | **keep** — complementary to upstream v0.6.6's multi-tenant auth tightening (`d6b865d9`: `/debug/vars` + `/v1/metrics` operator-only, empty-account user tokens refused, toolbox `/admin` refused); neither subsumes the other. |
| 12 | Reject L4 port ranges overlapping daemon listener ports | `c8f65c76` | **keep** — fork-only (`internal/config/l4_port_range_test.go`). |
| 13 | Deep-review remediation: host escape, cluster divergence, secret exposure | `0d292d85` | **keep** — includes `pkg/models/mounts.go` bind-target `:,`/AWS-INI-newline injection rejection (see SDK/wire-surface parity) and the C6f control-plane hop sentinel block, now sitting **alongside** upstream v0.6.6's hop restorations (`c0a0ea8b`, `ab662971`) rather than before them. |
| 14 | toolboxd `Session.finish` self-deadlock / `ExitInfo` | `e6742c62` | **keep** — `finish()` still at `cmd/toolboxd/sessions/session.go:317`; upstream `8ac1e3f1` (atomic `GetOrCreate` claim) and the v0.6.7 shell/route-change survival fix are complementary, both kept. |
| 15 | Exec-stream guardrail (env filter + tests) | `8d4a387a` | **keep** — `cmd/toolboxd/exec_env_filter_test.go` green; upstream v0.6.6 exec-retry fixes do not touch the filter. |
| 16 | S3 mount error handling (`s3.go` retries/tests) | `b2eea19d` | **keep** — fork-only. |
| 17 | Daytona SDK pin for go1.27 build | `b566c8d1` | **drop (superseded)** — unchanged verdict; `go.mod` is upstream v0.6.7's. |
| 18 | Bulk dependency updates | `5811e3f4`, `8ec1262c` | **drop (subsumed)** — upstream dependabot through `88120fdb` covers them. |
| 19 | Test migration/alignment | `e8e4b0ab`, `c3e1adc3`, `7d75304d`, `7de55b68`, `1e66e3c3`, `3ced0a7d`, `8404f4c3`, `1da215b4`, `845e347d`, `8b516d9f`, `214992ca` | **keep** — keeps the suite green on the merged tree; this sync's conflict resolutions extend the group. |
| 20 | Mount re-establish contract test (start-after-stop) | `4a7e9333` | **keep** — pins patch group 1/2 behaviour (`pkg/mounts/recovery_test.go`). |
| 21 | `.gitignore` local `.worktrees/` | `91e1b660` | **keep** — trivial, no upstream impact. |

## Merge conflict resolutions (sync to v0.6.7)

The merge conflicted in four files; each resolution kept both sides' intent:

- `internal/cluster/agent.go` — kept **both** sentinel-restoration blocks: the
  fork's C6f `ErrArtifactCatalogSuperseded` match and upstream v0.6.6's
  `ErrReservationConflict` / `ErrNameConflict` / `ErrMemberStillAlive` /
  `ErrLastVoter` hop restorations (a non-voter-ingress Idempotency-Key retry
  must route to the existing placement).
- `pkg/api/e2b/routes.go` — took upstream's code (B6 `snapshotForwardWrap` for
  `DELETE /templates/{id}`; B5 `clusterroute.Wrap` forwarding `/runtime` by the
  `E2b-Sandbox-Id` header) and kept the fork's "mounted WITHOUT d.Auth on
  purpose" security warning in the comment.
- `pkg/api/e2b/runtime_proxy.go` — kept the fork's fail-closed check (the
  `X-Access-Token` is mandatory on every request unless the operator sets
  `SB_E2B_ALLOW_UNAUTHENTICATED_RUNTIME` for a `meta.Secure=false` sandbox)
  instead of upstream's `meta.Secure`-gated check; upstream's inline
  constant-time compare is subsumed by the fork's `accessTokenValid`.
- `pkg/docker/build.go` — kept both imports and both behaviours: the fork's
  singleflight build dedup and upstream's `distribution/reference`-based
  registry-only base validation.

## Security non-reversion check

- v0.6.3 mount-source command injection (GHSA-5pqm-4fh4-585q), WASM toolhost
  host-exec fail-closed, sandbox-id/bundle-digest/module-ref path checks: kept
  patches touch none of those validators. ✔
- v0.6.4 containerd build-context tar bounds: untouched. ✔
- v0.6.5 sandbox-name rejection + `CAP_NET_RAW` removal: untouched. ✔
- v0.6.6 registry-only bases for account-token builds (`b3a43ef8`), WASM
  outside-work-dir refusal, multi-tenant auth tightening (`d6b865d9`), snapshot
  local-tag naming (`5d6c75a0`): all present in the merged tree; the only place
  the fork differs is `runtime_proxy.go`, where the fork is **stricter** than
  upstream. ✔
- v0.6.7 audit-witness wiring (`1ea3172e`) is inert without
  `SB_AUDIT_WITNESS_BACKEND`; the Caddy `stream_close_delay` change is in
  `packaging/Caddyfile.template`. ✔
- Nothing in this sync dropped a patch; the historical drops (7, 17, 18) remain
  the only ones and each removes code the upstream already carries.

## SDK / wire-surface parity

- **`go.mod` / `go.sum`: identical to upstream tag v0.6.7** (diff empty).
- **`pkg/models`: fork delta is the mount-spec hardening in `mounts.go`** (Docker
  bind-target `:,`/`,` option-injection rejection; AWS shared-credentials INI
  newline rejection for `access_key_id`/`secret_access_key`/`session_token`
  while `rclone_conf`/`private_key_pem` keep their multi-line form) plus four
  mount test files. Nothing else diverges.
- **`sdk/go`: diverged on purpose.** The fork keeps token/secret redaction
  (`sdk/go/pkg/types/redact.go`, `RedactToken`, redacted `String()`) and
  registry push credentials in the build request body; upstream's retry/session
  additions were merged around them. A consumer should treat the fork SDK as the
  source of truth when talking to a fork server.
- **go1.27 consumer build: re-verified.** A scratch module on go1.27.0 with
  `replace github.com/aerol-ai/microvm => <fork>` compiles and runs an import of
  `sdk/go/pkg/microvm` + `sdk/go/pkg/types` (`NewClientWithConfig`,
  `RedactToken`).

## Release

- Tag: `v0.6.7-speshu.1`
- Release commit: the commit carrying this file (see the header; the tagged
  SHA lands in the post-tag verification block on `main`)
- Suite state at release commit: `go build ./...` + `go test ./... -parallel 8`
  green (0 FAIL; run recorded in the sync session log)
- Assets (laoboost/microvm, tag-scoped copies also attached as
  `v0.6.7-speshu.1_<asset>` per the release workflow):
  - https://github.com/laoboost/microvm/releases/download/v0.6.7-speshu.1/install.sh
  - https://github.com/laoboost/microvm/releases/download/v0.6.7-speshu.1/sandboxd_linux_amd64
  - https://github.com/laoboost/microvm/releases/download/v0.6.7-speshu.1/toolboxd_linux_amd64
  - https://github.com/laoboost/microvm/releases/download/v0.6.7-speshu.1/caddy_linux_amd64
  - https://github.com/laoboost/microvm/releases/download/v0.6.7-speshu.1/checksums.txt
  - https://github.com/laoboost/microvm/releases/download/v0.6.7-speshu.1/sbom.cdx.json
  - https://github.com/laoboost/microvm/releases/download/v0.6.7-speshu.1/v0.6.7-speshu.1.intoto.jsonl
- Verification block: filled after the release workflow attaches the assets
  (checksums + provenance subject digests, per RELEASE-VERIFICATION.md).
