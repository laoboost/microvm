# SpeShu fork patch parity audit — v0.6.11-speshu.1

Base: upstream `aerol-ai/microvm` tag **v0.6.11** = `3b16d96a`, merged into the
fork in `c5750c30` (2026-11-04; pre-merge fork main `d45a414c`, backup branch
`pre-upstream-sync-backup-2026-11-04`). Previous sync: v0.6.7 = `88120fdb`
(into `bf69b18f`, audit at tag `v0.6.7-speshu.1`). The upstream range absorbed
here is v0.6.8–v0.6.11: **742 commits**, including the identity/SSO train
(PR #740–775) that the v0.6.7 sync deliberately skipped and that v0.6.8 then
released.

Method: `git log --no-merges` over the fork's patch range
(`88120fdb..d45a414c`), plus a per-file re-check of each group against the
merged tree and the v0.6.8–v0.6.11 changelog (`CHANGELOG.md` at `3b16d96a`).
The per-file merge resolutions this sync are recorded in
`/tmp/w1-merge-resolutions.md` and distilled below.

**Absorbed features ship disabled.** The v0.6.8 identity/SSO train (scoped
`avm_` tokens, OIDC SSO, roles) is inert by default:
`SB_IDENTITY_ENABLED` defaults to `false` (`internal/config/config.go:2287`);
empty OIDC issuers with identity on is tokens-only mode. The v0.6.9/v0.6.10
key-custody machinery defaults to plain-`file` custody and only enforces
wrapped keys for `SB_ENTERPRISE_MODE=true` servers with a key store. Operators
who do not opt in see no behavior change on these axes.

## Verdicts

| # | Patch group | Fork commits (deduped) | Verdict |
|---|---|---|---|
| 1 | Mount lifecycle: teardown on die/stop/oom; skip re-mount during stop | `11dafc27` | **keep** — the fork's teardown/re-establish paths in `pkg/mounts` are untouched by upstream v0.6.8–v0.6.11. Upstream PR #722 (`xtenant/02-mounts-owner-only`, `c098a390`) adds an owner-scoped refusal of raw host-side mounts in `internal/service/service.go` — an authorization layer, not lifecycle; upstream PR #860's readiness fail-fast (`ef872faf`) complements it. Contract tests `internal/service/mount_teardown_test.go`, `pkg/mounts/recovery_test.go` green. |
| 2 | Mount reliability: sweep-vs-start race, parallel establishment, pinned throughput, `allow_delete` | `98017426` | **keep-extends-upstream** — parallel establishment, pinned throughput and `allow_delete` kept. One mechanism **dropped as superseded**: the fork's `inFlight` snapshot gate gave way to upstream's per-ID `mounting`/`sweeping` ownership (PR #860, `21321c2a`) — the same sweep-vs-start race (prod 2026-09-09) fixed strictly stronger: the claim is registered under the mutex before the directory exists, and double `MountAll` is refused (`ErrMountsEstablished`). Still test-pinned: `pkg/mounts/sweep_inprogress_test.go`, `start_race_test.go`; the merged tree records the succession at `pkg/mounts/manager.go:178`. |
| 3 | Restart sandbox on mount crash | `bfe0f2da` | **keep** — fork-only (`onMountCrash` at `pkg/mounts/manager.go:56-58,111`; `internal/service/mount_crash_restart_test.go`). |
| 4 | Mount credential-failure detection: scan live mount-process output; structured warn + expvar counter | `c3b5da08`, `6f27b2ad`, `13fa40ef`, `e70ab278`, `765b46fc`, `7519c11a`, `c85b982e` | **keep** — fork-only observability (`pkg/mounts/metrics.go`, `cred_failure_test.go`); the merged `process.go` signature keeps the fork's `onCredFailure` callback **and** upstream's `*procExit` return. |
| 5 | Cred-file unlink after supervised restart; probe wait outside manager lock | `9fc36a92` | **keep** — fork-only (`pkg/mounts/manager.go:577-630`, `removeRestartCred`). |
| 6 | S3 adapter structured options, `extra_args` denylist, source guard, absolute-endpoint scheme validation | `23971a2c`, `92bbf8e2`, `2fd520c0` | **keep (extends upstream)** — fork delta at `pkg/mounts/adapters/s3_options.go` + `s3_fork_flags_test.go`. Upstream PR #860 (`fix/s3-mount-wait`, `5646ac4f`) touches mount readiness/ownership, not adapter option handling; the v0.6.3 source-injection guard stays subsumed under it all. |
| 7 | Reject dash-prefixed mount sources across adapters + spec validation | `0765164d` | **drop (subsumed by v0.6.3)** — unchanged from the v0.6.5/v0.6.7 audits; v0.6.8–v0.6.11 does not re-open the question. |
| 8 | Tenant image-ref validator (vendored `distribution/reference`) + wiring | `4a3750e8`, `e3d3771a` | **keep-extends-upstream** — fork's `ValidateImageRef` (`internal/service/imageref.go:38`, wired at `internal/service/service.go:1939`) now sits beside upstream v0.6.8's `authorizeTenantImageRef` string-match refusal (`internal/service/image_distribution.go:73`, from the xtenant hardening `b9f7a1c5` plus PR #722): upstream refuses dangerous *forms* (e.g. the cluster `_imported` namespace), the fork validates *parseability*. Neither subsumes the other; both enforced. |
| 9 | Per-sandbox pids limit (default 1024, parked containers included) | `fb074fc6` | **keep** — the binding constraint holds: `getEnvInt("SB_SANDBOX_PIDS_LIMIT", 1024)` at `internal/config/config.go:2043`; upstream's 4096 default was **not** taken. Upstream v0.6.9's `gvisorpids` wiring (`pkg/gvisorpids`, `internal/runtime/containerd/lifecycle.go:206`) layers on top with the `int64` cast carrying the fork value; `containerd/pids_limit_test.go` assertions unchanged. |
| 10 | WASM wasmtime hardening: no host env/stream inheritance, fail-closed memory limit, mkstemp outputs | `4284cf23` | **keep** — no upstream commit in v0.6.8–v0.6.11 touches the wasmtime engine. Path correction: upstream moved the engine; the hardening now lives at `pkg/wasm/engine_wasmtime.go:121-156` ("Nothing is inherited"), with `pkg/wasm/engine_wasmtime_hardening_test.go` pinning it. Upstream's per-owner WASM module catalogue (`aa13e5c1`) is a different layer, both present. |
| 11 | toolboxd fail-closed auth on empty `SB_TOOLBOX_TOKEN` (`SB_TOOLBOX_AUTH_OPTIONAL` escape hatch) | `cfd600b2` | **keep-extends-upstream** — merged `requireAuth` is upstream's `identity()`-based token validation + fail-closed no-token 401 **with** the fork's `authOptional` escape hatch inside the `token == ""` branch (`cmd/toolboxd/main.go:79-106`); coverage tests exercise both subtests. Upstream v0.6.10's "serve nothing without a sandbox token" (`0c8ebba5`) is toolbox-side, complementary. |
| 12 | Reject L4 port ranges overlapping daemon listener ports | `c8f65c76` | **keep** — fork-only (`internal/config/config.go:2773`, `l4_port_range_test.go`). |
| 13 | Deep-review remediation: host escape, cluster divergence, secret exposure | `0d292d85` | **keep-extends-upstream** — the fork's netrules IPv6 fail-closed precondition (`verifyIPv6Disabled`, `pkg/docker/netrules/manager.go:117-135`) and bridge-ACCEPT `Append` (`ensure_chain.go:137`, pinned by `chain_order_test.go` "bridge ACCEPT must never leapfrog a per-IP DROP") coexist with upstream v0.6.9's deny-floor work (PR #888 `fix/egress-floor-fail-closed`, `3c789c10`–`769def0e`): the merged `ReassertChain` keeps upstream's floor-jump restoration **and** the fork's `ensureLinkLocalDrop()`. The C6f sentinel block and upstream's hop restorations auto-merged side by side in `internal/cluster/agent.go`. install.sh's subshell env-write hardening (`0d292d85`) kept; the upstream install.sh test extractor was generalized to accept `() {` and `() (` rather than weakening the fork. |
| 14 | toolboxd `Session.finish` self-deadlock / `ExitInfo` | `e6742c62` | **keep** — `finish()` still at `cmd/toolboxd/sessions/session.go:317`; no upstream v0.6.8–v0.6.11 commit restructures session exit. |
| 15 | Exec-stream guardrail (env filter + tests) | `8d4a387a` | **keep** — `mergeEnvForExec` is `childEnviron` + request env minus privileged keys, kept at all three call sites (`cmd/toolboxd/exec_stream.go:483`, `main.go:745`, `daytona_code_run.go:107`); upstream's raw-env path not taken. |
| 16 | S3 mount error handling (`s3.go` retries/tests) | `b2eea19d` | **keep** — fork-only error taxonomy in `pkg/mounts/adapters/s3.go`; upstream PR #860 adds readiness fail-fast, a different concern. |
| 17 | Daytona SDK pin for go1.27 build | `b566c8d1` | **drop (superseded)** — unchanged verdict; `go.mod` is upstream v0.6.11's (SDKs at 0.8.0 per upstream `c9623abd`). |
| 18 | Bulk dependency updates | `5811e3f4`, `8ec1262c` | **drop (subsumed)** — upstream dependabot through `3b16d96a` covers them. |
| 19 | Test migration/alignment | `e8e4b0ab`, `c3e1adc3`, `7d75304d`, `7de55b68`, `1e66e3c3`, `3ced0a7d`, `8404f4c3`, `1da215b4`, `845e347d`, `8b516d9f`, `214992ca` | **keep** — keeps the suite green on the merged tree; extended again by this sync (see "Merge conflict resolutions" — new-signature stubs, fake `Append` on upstream rule backends, `authedRoutes` helper adoption, four rounds of upstream-test alignment). |
| 20 | Mount re-establish contract test (start-after-stop) | `4a7e9333` | **keep** — pins patch group 1/2 behaviour (`pkg/mounts/recovery_test.go`). |
| 21 | `.gitignore` local `.worktrees/` | `91e1b660` | **keep** — trivial, no upstream impact. |

**Also kept (this sync's binding constraints):**

- **Fork pids default 1024** — see group 9; explicitly set at
  `internal/config/config.go:2043`, comment documents both values.
- **Five fork SDK egress create fields stay on the create path** —
  `EgressProfiles`, `NetworkEgressMode`, `NetworkEgressRules`,
  `EgressWithheldEnv`, `MaskRequestHost` on `models.CreateSandboxRequest`
  (`pkg/models/types.go:672-712`; `sdk/go` `CreateSandboxOptions` is an alias),
  stamped by the service create at `internal/service/service.go:2477-2489`
  alongside upstream's own egress fields (`NetworkBlockAll/AllowOut/DenyOut`).
  Provenance correction to the sync spec's call-out: there was no
  `perf/egress-attach-*` collision — `pkg/models/types.go` is byte-identical
  to upstream v0.6.11 (these five fields are upstream's own, verified present
  and identical on both sides) and `pkg/egresspolicy` predates v0.6.6, so both
  stamp sets simply stand as merged.

## Merge conflict resolutions (sync to v0.6.11)

48 files conflicted (the three known v0.6.7 conflict files —
`internal/cluster/agent.go`, `pkg/api/e2b/routes.go`,
`pkg/api/e2b/runtime_proxy.go` — auto-merged this time, both sides' intent
preserved). Distilled by area from the implementer's resolution record
(`/tmp/w1-merge-resolutions.md`):

**Networking / netrules (security-relevant).** `manager.go` took both sides'
additive fields (fork `bridgeName`/`ipv6Disabled` + `verifyIPv6Disabled`; upstream
`floorFirst`/`floorChain`/`nodeGuard`/`setsReady` + `topPos()`). `ensure_chain.go`:
the fork's `Append` for bridge ACCEPTs was kept over upstream's
`Insert(topPos())` — `Insert` at the floor position still leapfrogs per-sandbox
DROPs on re-assert, the exact bug the fork fixed and `chain_order_test.go` pins;
`Append` satisfies upstream's floor invariant even more strongly. `ReassertChain`
keeps upstream's floor-jump restoration **and** the fork's `ensureLinkLocalDrop`,
with error propagation preserved. Test fakes for upstream rule backends gained
`Append` mirroring each fake's `Insert`.

**Mounts.** `manager.go`: upstream's `mounting`/`sweeping` ownership superseded
the fork's `inFlight` gate (see group 2 — dropped as superseded, strictly
stronger same-race fix, still test-pinned). Kept: `onMountCrash`,
`ValidateSandboxID`, the parallel-establishment doc, cred-failure handlers at
both spawn sites, `state.cmd/output/exit` publishing, and
`waitForMountProbe(..., exit.done)` at the restart site. `process.go` merged
the fork's `onCredFailure` parameter with upstream's `*procExit` return. Test
stubs gained the new signatures (exit channel, `watchExit` states).

**internal/service.** `service.go`: kept the fork's `models.ValidateSandboxID`
service-boundary check **and** upstream's `beginCreateLease` idempotency lease.
`toolbox_proxy.go`: the fork's blanket `Sec-WebSocket-Protocol` deletion kept in
all three places (pinned by `toolbox_proxy_security_test.go` "no header
upstream at all"; upstream's surgical strip-keeping-the-marker would fail that
test); upstream's `callCtx` lifetime tracking taken; the now-unused
`stripSmuggledBearerSubprotocol` retained for upstream parity (see Judgment
calls). `wasm_module_api.go` and `isolate_bundles.go`: kept both the fork's
scoped-tenant/digest in-use checks and upstream's owner scoping. Post-merge
alignment: the fork's wasm host-path gate now wraps `wasmmod.ErrModuleNotFound`
(satisfies upstream's new test without weakening the operator-only refusal),
and the two host-path gates moved from `ownerScope` to `hostCapable` per
upstream's "scope is not power" audit, registered with reasons.

**Cluster.** `placement.go`: fork's reservation-overwrite stamping kept with
upstream's new `admitReservationCommands` signature. `client.go`: both fork
tombstones (`deliberatelyDeleted`) and upstream catch-up fields kept.
`egress_profiles_test.go`: upstream's stricter settle helper replaces the
fork's (same flake fix).

**Runtime (containerd / firecracker).** Pids: upstream `int64` types with the
fork's 1024 default intact (group 9); `gvisorpids` wiring taken; the fork's
warm-pool fork-bomb comment preserved in the new free-function
`resourceSpecOpts`. Firecracker warm-acquire: upstream's 6-arg
`tryAcquireWarm` call kept, matching the fork's own 6-parameter
`warmacquire.go`; the fork's removal of upstream's dead `tapSlot` parameter
**kept** (upstream had kept the param; it is overwritten before any read —
documented in the kept comment at `internal/runtime/firecracker/warmacquire.go:151`).

**toolboxd.** `requireAuth` merges upstream `identity()` + fork escape hatch
(group 11); both startup warnings kept; `mergeEnvForExec` kept over upstream's
raw env (group 15). Coverage tests adopted upstream's `authedRoutes` helper
(82 call sites) with one two-subtest exception covering both auth behaviors.

**Config (binding constraints).** Fork defaults kept: pids 1024, APIHost
`127.0.0.1` loopback, CaddyAdminURL `unix:///run/caddy/caddy-admin.sock` +
`validateCaddyAdminURL`; upstream's `validateIdentity*` validators coexist.
Upstream's new fields taken (`SandboxInterconnect`, `SandboxNodePortsAllow`,
`DiskLimitCheckInterval`, `ToolboxDeadPeerTimeout`).

**Daemon / docker.** Fork's error-returning `wireDockerNetnsPool` kept with
fixed scoping; upstream's free-function `parkDefaultResources(pidsLimit int64)`
taken (the parameter carries the value, the fork's `c.pidsLimit` block
dropped as redundant).

**SDKs.** TypeScript/Rust/Java: the fork's credential-stripping redirect
followers, leak-proof token storage (`#patToken` + WeakMap getter) and
redacting `Debug` kept; upstream's `team` support, retry-after machinery and
0.8.0 error-type rename layered in; `npm run build` and `cargo test` (110
passed) green; Java unverified locally (no JDK/maven — mechanical changes only).

**Scripts / Terraform / docs.** install.sh: fork's key-not-in-argv and
never-print-the-PAT kept over upstream's `echo "PAT token: …"`, plus all
upstream flags. Restore script: fork's allowlist/traversal/symlink/setuid
pre-extraction validation + staged extraction kept **and** upstream's
key-custody set-aside/post-processing kept (the safety restore allowlist
gained the three `credential_encryption.key*` files upstream's backup now
emits). Terraform: see Judgment calls — one knob, named
`allow_admin_from_anywhere`. docs: fork's `StrictHostKeyChecking=yes` note and
upstream's `mount_tool_unavailable` bullet both kept.

**Dropped as superseded (complete list):** fork mount `inFlight` gate →
upstream `mounting`/`sweeping` ownership (group 2); upstream's
`Insert`-at-`topPos` bridge-ACCEPT change → fork `Append` (kept, stronger);
upstream's `stripSmuggledBearerSubprotocol` call sites → fork blanket header
deletion (helper retained unused); upstream's 4096 pids default → fork 1024.

**Post-merge test-alignment (upstream tests meeting fork behavior — no fork
test weakened or deleted):** generalized install.sh brace/subshell extractor
(`pkg/caddy`, `internal/runtime/firecracker`); node-test summary regex accepts
`ℹ pass N`; `runtime_proxy_stop_test` uses the documented
`SB_E2B_ALLOW_UNAUTHENTICATED_RUNTIME=1` escape hatch; upstream's
subprotocol-marker test adapted to expect `""` (its no-PAT-leak assertion
unchanged); restore test gained the three key files; one stale duplicate of an
upstream-evolved test deduplicated (newer variant kept). Full suite green on a
proxy-clean environment.

## Judgment calls

1. **Terraform /0 gating (review follow-up).** The fork's pre-merge regex
   rejected `0.0.0.0/0` unconditionally while its own error message documented
   an `allow_public_admin` opt-in — the two could never agree, making the
   documented opt-in unusable. The merged tree fails closed by default
   (`admin_allowed_cidrs` is required, `[]` fails plan) and gates `/0` solely
   on the explicit `allow_admin_from_anywhere = true` opt-in:
   `locals.tf:465-468` precondition, `network.tf:128-129` admin_ssh
   precondition, `variables.tf:78,99-112` validation/variable. Verified
   internally consistent (variable, both preconditions, the tfvars example and
   both error messages all name the same knob; the reserved/documentation-range
   validation no longer lists `/0` and its comment says so). Verdict: **keep —
   upstream's audited precondition design, matching the fork's intent** (same
   UC-328 hardening campaign; world-open admin still requires an explicit
   opt-in, now a working one).
2. **Stale `allow_public_admin` strings.** `locals.tf:468`'s error message and
   `variables.tf:99`'s comment still named the pre-normalization knob.
   Verdict: **keep-but-fix — applied**: both strings now name
   `allow_admin_from_anywhere`; the `variables.tf` comment was also corrected
   (it claimed `/0` "is rejected here", which the validation no longer does —
   it is gated by the precondition instead).
3. **`stripSmuggledBearerSubprotocol` retention.** The helper is unused since
   the fork strips the whole `Sec-WebSocket-Protocol` header (pinned by the
   fork's security test); deleting upstream code was judged the worse option.
   Verdict: **keep — retention comment added**
   (`internal/service/toolbox_proxy.go`, "Retained for upstream parity; unused
   since the fork strips the whole header.").

## Security non-reversion check

- v0.6.3 mount-source injection, v0.6.4 build-tar bounds, v0.6.5 sandbox-name
  rejection + `CAP_NET_RAW` removal: untouched by this sync. ✔
- v0.6.6 registry-only bases, WASM outside-work-dir refusal, multi-tenant auth
  tightening, snapshot local-tag naming: all present; the fork remains
  **stricter** at `pkg/api/e2b/runtime_proxy.go` (fail-closed token) and
  `internal/service/toolbox_proxy.go` (blanket header strip). ✔
- v0.6.7 audit-witness wiring inert without `SB_AUDIT_WITNESS_BACKEND`. ✔
- v0.6.8 xtenant gap closures (PR #722 `c098a390`; `b9f7a1c5` bundles/imported
  refs/toolbox) present; the SSO/roles/caller-audit train ships **disabled**
  (see header). ✔
- v0.6.9 fences/limits (egress deny floor PR #888, block-set, slot pools,
  list-gate shedding) present and layered around the fork's netrules hardening
  (group 13). ✔
- v0.6.10 key custody + keyed creates/HMAC digests present; custody enforces
  only for enterprise servers with a key store, default `file`. ✔
- v0.6.11 firecracker sparse-snapshot copy and restore-jailer chown fixes
  present. ✔
- Historical drops (7, 17, 18) remain the only code-removal drops; this sync
  added one mechanism drop (group 2's `inFlight` gate), subsumed by a strictly
  stronger upstream fix of the same race. ✔

## SDK / wire-surface parity

- **`go.mod` / `go.sum`: identical to upstream tag v0.6.11** (diff empty).
- **`pkg/models`: fork delta is the mount-spec hardening in `mounts.go`** (unchanged
  from v0.6.7 audit) **plus the five fork egress create fields** (see Verdicts),
  which ride the same `CreateSandboxRequest` upstream extended.
- **SDKs (TS/Rust/Java)**: fork token redaction + credential-stripping redirect
  followers kept with upstream 0.8.0 (`team`, retry-after) layered in; go SDK
  unchanged apart from riding `models`.
- **go1.27 consumer build**: inherited from the v0.6.7 audit; the SDK surface
  the check imports is unchanged by this sync.

## Release

- Tag: `v0.6.11-speshu.1` at `f1b474ae26f89021018422d5f839166249432ceb`
  (published 2026-11-04 on github.com/laoboost/microvm, marked Latest —
  `releases/latest` resolves to this tag)
- Release commit: `f1b474ae` (the commit carrying this file)
- Suite state at release commit: `go build ./...` + `go test ./... -parallel 8`
  green at the merge commit (Task 1, proxy-clean environment); re-verified for
  this audit's edits: `go build ./...` and
  `go test ./internal/service/ ./pkg/docker/netrules/ -count=1` green.
- Assets (laoboost/microvm, 58 files: `install.sh`, `sandboxd`/`toolboxd`/
  `caddy`/`aerolvm` per-arch binaries, the cluster scripts, `checksums.txt`,
  `sbom.cdx.json`, `v0.6.11-speshu.1.intoto.jsonl`; tag-scoped copies also
  attached as `v0.6.11-speshu.1_<asset>` per the release workflow):
  - https://github.com/laoboost/microvm/releases/download/v0.6.11-speshu.1/install.sh
  - https://github.com/laoboost/microvm/releases/download/v0.6.11-speshu.1/sandboxd_linux_amd64
  - https://github.com/laoboost/microvm/releases/download/v0.6.11-speshu.1/toolboxd_linux_amd64
  - https://github.com/laoboost/microvm/releases/download/v0.6.11-speshu.1/caddy_linux_amd64
  - https://github.com/laoboost/microvm/releases/download/v0.6.11-speshu.1/checksums.txt
  - https://github.com/laoboost/microvm/releases/download/v0.6.11-speshu.1/sbom.cdx.json
  - https://github.com/laoboost/microvm/releases/download/v0.6.11-speshu.1/v0.6.11-speshu.1.intoto.jsonl
- Verification block: filled post-tag below (checksums + provenance, per
  RELEASE-VERIFICATION.md).

## Verification (post-tag)

- Tagged commit: `f1b474ae26f89021018422d5f839166249432ceb` (tag
  `v0.6.11-speshu.1`; verified via the GitHub refs API)
- Release workflow: `release.yml` run `41815962828` (5 attempts) — the final
  attempt's "Publish release" job is red although every asset upload in its log
  reports success and the release carries the complete 58-asset set (three
  assets were left untouched by digest match); the step log shows no error
  line. Read the red status as a publish-step exit-code fault, not an asset
  fault — but re-check the uploader before the next release.
- Checksums verified out-of-band against the attached `checksums.txt`:
  `sha256sum -c checksums.txt --ignore-missing` passed for `install.sh`,
  `sandboxd_linux_amd64`, `toolboxd_linux_amd64`, `caddy_linux_amd64`
- Provenance: `v0.6.11-speshu.1.intoto.jsonl` attached (sigstore bundle). As
  for v0.6.7-speshu.1 the DSSE signature was not verified locally (this
  machine's `gh` 2.46.0 has no `attestation` subcommand and no cosign); verify
  with `gh attestation verify … --repo laoboost/microvm --signer-workflow
  laoboost/microvm/.github/workflows/release.yml` from a newer gh.
