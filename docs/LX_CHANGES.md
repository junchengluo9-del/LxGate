# Lx-ui changes relative to upstream 3x-ui

This file is the human-readable modification statement required by GPL-3.0
section 5a. Dates are in UTC+8. The authoritative record is the git history.

Fork point: upstream `a8d0700b` (3x-ui v3.10.2 + 2 commits), 2026-10-18.

## 2026-10-19 — Milestone 1

- Phase 0: repository set up, NOTICE and this file added. LICENSE (GPL-3.0) unchanged.
- Phase 2: merged 12 upstream pull requests into `lx-base` (list in docs/UPSTREAM_SYNC.md).
- Removed the upstream AI issue-analyst / PR-review workflows (`claude-*`) and
  their tests.
- Phase 1 — rebrand to Lx-ui, version **1.0.0**:
  - `tools/rebrand.env` is the single source of the identity
    (`LXUI_REPO="<ORG>/Lx-ui"` placeholder); `tools/rebrand.sh` applies it
    reproducibly (the mechanical rebrand is its own commit);
    `tools/check-rebrand.sh` (`make rebrand-check`, part of `make verify`)
    fails on stray upstream download/update/image URLs.
  - Names and paths: `x-ui` → `lx-ui` (binary, CLI, `lx-ui.service`,
    `/usr/local/lx-ui`, `/etc/lx-ui/lx-ui.db`, `/var/log/lx-ui`,
    `/etc/default/lx-ui`), release assets `lx-ui-linux-<arch>.tar.gz`,
    GHCR image `ghcr.io/<owner>/lx-ui` (Docker Hub publishing removed).
  - Install/update/menu scripts download from `LXUI_REPO`; the panel's
    self-update and release lookup use `config.GetReleaseRepo()` and are
    disabled while the placeholder is in place.
  - `LXUI_*` environment variables, with the `XUI_*` names kept as aliases.
  - Migration from 3x-ui: automatic copy of `/etc/x-ui` on first start
    (never modifies the legacy data) and `install.sh --migrate-from-3x-ui`
    with automatic rollback.
  - Removed: sponsor feed/page/API (`/sponsors`), donation links, upstream
    READMEs in languages other than English/Chinese.
  - Frontend brand from `frontend/src/lib/brand.ts`; README in English and
    Chinese crediting 3x-ui.
  - Unchanged on purpose: Go module path `github.com/mhsanaei/3x-ui/v3`,
    database schema, API, internal `3xipl` fail2ban jail names.
  - Known leftovers: logo/icon images are still the upstream artwork (renamed
    files); the paused docs site (`docs/content`) still describes 3x-ui
    paths in places and mentions `/sponsors`.

## 2026-10-19 — Milestone 2 (Phase 3–5)

Status legend: **confirmed** = root cause shown by a test that fails on the
old code; **hypothesis** = plausible cause, not reproduced; **mitigation** =
change that limits damage regardless of the cause.

### Fixes (Phase 3)

- **#6874 TUIC per-user routing: confirmed.** The TUIC bridge fed Xray
  through a no-auth SOCKS relay, so Xray never knew the user and `user`
  routing rules could not match. The relay SOCKS inbound now carries one
  account per TUIC client email (the AmneziaWG approach; disabled clients
  keep their account so toggling does not restart Xray), and a restart is
  only flagged when an email is added or renamed. Proven with an in-process
  xray-core test (`internal/tuic/xray_user_routing_test.go`). Not verified:
  per-user matching for UDP ASSOCIATE traffic.
- **#6870 multi-node client desync: confirmed (logic) / hypothesis (trigger).**
  The master latched a node-reported `enable=false` whenever node and master
  limits matched. It now accepts a node disable only when its own accounting
  (with this tick's deltas) or the node's reported counters prove quota
  exhaustion, or the client is expired; otherwise the master row stays
  enabled, the lifecycle lift marks the node dirty and the client is
  re-pushed. Same rule in Go (`nodeDisableIsStale`) and SQL
  (`ClientTrafficEnableMergeExpr`, SQLite + Postgres). Regression tests in
  `internal/web/service/node_disable_desync_test.go` fail on the old code.
  The real-world trigger (a node acting on a stale `client_global_traffics`
  row after a master reset, or a lagging reconcile) is a hypothesis; the
  node-side window (up to one global push, ~30 s) is not changed. The
  Postgres SQL path was not run locally.
- **#6541 node sync load: mitigation.** New settings
  `nodeHeartbeatInterval` / `nodeTrafficInterval` (5–300 s, default 5 = old
  behaviour) and `nodeSyncJitter` (0–60 s, default 0), applied at panel
  start (Settings → General). The 30 s global-usage push skips a node whose
  payload digest is unchanged (forced refresh every 5 min, full push after
  the node was offline or a push failed). Not done (needs a node protocol
  change): incremental snapshots, merged heartbeat+traffic request, idle
  back-off. Node-down alerts count failed probes, so a longer heartbeat
  interval delays them.
- **#6871 Xray update under the systemd sandbox: mitigation; root cause NOT
  reproduced (hypothesis: a sandbox restriction).** Panel Xray updates now
  keep Xray running during download, stage and validate the new binary in the
  panel's own environment (`xray version` must match, `xray run -test -c
  <generated config>` must pass — `-test` runs the core-creation step that
  fails in the issue), keep the old binary as `<bin>.bak`, and roll back
  automatically if the new core is not running 3 s after start. EPERM errors
  carry a sandbox hint. Reproduction attempt (no systemd on the build box):
  the sandbox was emulated with seccomp `@system-service` lists from systemd
  252 (Debian 12) and 257, the unit's capability bounding set,
  NoNewPrivileges, address-family/namespace restrictions and a read-only
  `/proc/sys` except `/proc/sys/net`; Xray v26.10.10 and v26.10.17 both pass
  `-test` and start with REALITY + Shadowsocks + WireGuard kernel-TUN + TUN
  configs. Denying `ioctl` reproduces the exact error text with a WireGuard
  kernel-TUN outbound, and `-test` catches it. `lx-ui.service` is therefore
  unchanged; `docs/systemd/90-lxui-relaxed.conf.example` is an opt-in escape
  hatch.
- **Xray version policy (new).** `internal/xray/compat.go`: _recommended_
  v26.10.10 (bundled; matches the go.mod xray-core module), _verified_
  v26.9.30 / v26.7.28 / v26.7.11 / v26.6.27 (pins upstream shipped since the
  v26.6.27 minimum; not re-tested against this build), _experimental_
  everything else ≥ v26.6.27. The picker shows tiers (`/getXrayVersionTiers`),
  always offers recommended + verified (even when GitHub is unreachable) and
  lists/installs experimental versions only with `xrayAllowExperimental`
  (default off). A test fails if release.yml / DockerInit.sh drift from the
  recommended version.

### CI / release (Phase 4)

- `release.yml`: single `XRAY_VERSION`, Xray archives verified against the
  XTLS `.dgst` SHA2-256, artifacts `lx-ui-linux-<arch>.tar.gz` (+ `.sha256`)
  for amd64/arm64/armv7/armv6/386/armv5/s390x and `lx-ui-windows-amd64.zip`,
  `config.releaseRepo` injected from `GITHUB_REPOSITORY`.
- `Dockerfile` takes `LXUI_RELEASE_REPO` (docker.yml passes the repository);
  `DockerInit.sh` verifies the Xray checksum; image `ghcr.io/<owner>/lx-ui`.
- `ci.yml`: `rebrand-check` job.
- Validated with actionlint 1.7.12 + shellcheck 0.10.0 (only pre-existing
  shellcheck notes remain). **Never run on GitHub.**

### Verification (Phase 5)

- `make verify` passes locally (codegen freshness, rebrand guard,
  golangci-lint 2.15, oxlint, oxfmt, tsc, Go + vitest suites, frontend build,
  Storybook build). Lint findings fixed on the way.
- New tests: real-SQLite legacy migration (`legacy_migrate_db_test.go`),
  brand/rebrand consistency and guard under `go test`
  (`internal/config/rebrand_consistency_test.go`).
- End-to-end on the build box: `make node-e2e` (real master + node panel
  processes, 36 sub-tests) and a smoke script (build, temp DB, login/CSRF,
  API token, inbound add with Xray binding the port, version tiers, live
  switch to a verified Xray and back with `.bak`, legacy DB migration with the
  real binary), plus a local `docker build` (amd64) of the image, which
  starts Xray 26.10.10 and serves the login page. The docker build exposed a
  milestone-1 regression (`go build main.go` no longer compiled after the
  migration helper moved to its own file), fixed by building the package.
- Not testable here: GitHub Actions, real systemd units / install.sh /
  update.sh on a VM, Postgres, multi-arch (QEMU) images, Windows build,
  panel self-update against a real release repository.

## 2026-10-20 — Hotfix: "nodes don't work"

Investigated with the real binary, a real Xray core and a temp DB
(`/workspace/lx-reports/e2e/traffic_e2e.py`: 15 inbounds — VLESS REALITY/Vision,
VLESS/VMess/Trojan over TCP/WS/gRPC with and without TLS, Shadowsocks
AES-GCM and 2022 — each driven through a real Xray client built only from the
subscription's share link; all carry traffic and the panel counts it).

- **Fixed — panel Xray update always failed on real installs (confirmed on a
  live server).** The pre-update config test ran the staged binary as
  `bin/.xray-new-*` with `cmd.Dir=bin`, i.e. `bin/bin/...`, so with the default
  relative bin folder every switch failed with `fork/exec ...: no such file or
  directory` (the current core kept running). Paths are now absolute;
  `TestInstallXrayArchiveWithRelativeBinFolder` reproduces the live error on
  the old code. Verified end to end: panel switch v26.10.10 → v26.9.30 →
  v26.10.10 with a relative bin folder.
- **Fixed — install.sh / update.sh with no stable release.** GitHub's
  `releases/latest` ignores pre-releases, so the one-line installer failed with
  "Failed to fetch lx-ui version" while only `dev-latest` existed. Both now fall
  back to `dev-latest` (with a warning) when its archive exists for the
  architecture, and explain the situation otherwise. Verified in a clean
  Debian 12 container: download, SHA-256 check, extraction, binary + Xray
  26.10.10 + geo files present, panel answers on the configured port/path.
- **Documented + surfaced — REALITY client gates of Xray-core (not a panel
  bug, but the most likely cause of "online but no traffic").**
  - v26.9.8+ (`xtls/reality` 8cdf7bf9c7f0, includes the bundled v26.10.10)
    reject ClientHellos without an `X25519MLKEM768` key share before X25519;
    not configurable. Measured locally: server v26.10.10 accepts Xray clients
    with fp chrome/firefox/safari, rejects ios/edge/qq/random; servers v26.3.27,
    v26.6.27 and v26.7.28 accept all of them. Shadowrocket, sing-box and Mihomo
    raw links are reported upstream to lack the key share (not tested here).
  - v26.7.11–v26.9.7 default an empty `minClientVer` to 26.3.27.
  - `xray.RealityGateOf` classifies cores; `/getXrayVersionTiers` returns
    `realityGate` and the version picker shows a tag + explanation (13 locales).
    README gained a troubleshooting section (also: ICMP ping is often blocked by
    cloud firewalls and says nothing about the proxy).
- Not changed: the default config template (freedom `finalRules` blocking
  `geoip:private`) is upstream's and correct; it does block destinations that
  resolve to private/benchmark ranges, e.g. on hosts whose resolver is a
  fake-IP DNS (the dev box is one — the e2e test therefore targets an IP
  literal).

## 2026-10-20 — v1.0.1

First stable release after 1.0.0. Summary of the fixes since 1.0.0:

- Fixed switching the Xray core from the panel failing with
  `fork/exec bin/.xray-new-*: no such file or directory` (the pre-update
  config test now uses absolute paths).
- `install.sh` / `update.sh` fall back to the rolling `dev-latest` build when
  the repository has no stable release yet (checksum still verified).
- The Xray version list now labels which cores enforce a REALITY client gate
  (ML-KEM on v26.9.8+, minClientVer on v26.7.11–v26.9.7). Clients without
  ML-KEM (for example Shadowrocket) show "online but no traffic" on those cores;
  see the README troubleshooting section.
- Docs site: removed the deleted `/sponsors` API, formatted the LX docs.
- Release workflow: tagged releases are now published as a normal release and
  marked latest once every platform asset is uploaded, so `releases/latest`,
  `install.sh`, `update.sh` and the panel update check resolve the stable tag.
  `dev-latest` stays a prerelease that is never marked latest.

## 2026-10-28 — v1.0.2

- The bundled Xray core moves from v26.10.10 to v26.10.21. Upstream withdrew
  v26.9.8, v26.10.10 and v26.10.17 (root-CA security issue); the panel now
  marks those three as unsupported and will not install them.
- `update.sh` accepts `KEEP_XRAY=1` to keep the Xray core picked in the panel
  instead of replacing it with the bundled one.
- Measured on a local server (VLESS + REALITY + Vision): v26.9.8 and later
  only accept clients whose ClientHello carries X25519MLKEM768 — Xray/v2rayN
  with fp chrome/firefox/safari, and Mihomo with the panel's Clash
  subscription (`support-x25519mlkem768: true`). sing-box 1.14 (all fps),
  Xray fp `ios`, and Mihomo without that field fall back to the target site:
  "connected" but no traffic.

## 2026-10-28 — v1.0.3

- **Bundled Xray core is now v26.6.27** (was v26.10.21). v26.6.27 is the
  newest core without a REALITY client gate, so Shadowrocket, Clash/Mihomo
  (with or without `support-x25519mlkem768`), sing-box and Xray clients all
  work. v26.10.21 stays selectable in Overview → Xray version.
- Switching to a gated core (≥ v26.9.8 ML-KEM only, v26.7.11–v26.9.7 min client
  version) now shows a red warning that Shadowrocket/older Clash/sing-box will
  stop working and requires "Switch anyway" (all 13 locales).
- `update.sh` keeps the installed Xray core by default when it is a supported
  release (v26.6.27, v26.7.11, v26.7.28, v26.9.30, v26.10.21); `KEEP_XRAY=0`
  forces the bundled core, `KEEP_XRAY=1` keeps any core. Tested in Debian 12.
- Security caveat: v26.6.27 predates the bundled root-CA store pulled in
  v26.9.8–v26.10.17 (it uses the OS CA store) but lacks the Go 1.27.3 root
  filtering of v26.10.21. Only outbound TLS verification is affected, not the
  REALITY server; keep `ca-certificates` updated.

## 2026-10-28 — v1.0.4

- Security: Go toolchain bumped to 1.27.3 (`go.mod`), fixing GO-2026-6809 in
  `crypto/x509` (reachable via the REALITY target scan's certificate check).
- Security: frontend `npm audit fix` (non-breaking) — DOMPurify bumped to
  3.4.17 (critical advisories via swagger-ui-react). The remaining low-severity
  `deepmerge`/`swagger-client` advisory needs a breaking swagger-ui-react
  downgrade and is left as is.
- CI: docs Pages deploy is skipped unless the repo variable `PAGES_ENABLED=true`.
- No functional change; bundled Xray stays v26.6.27.

## 2026-11-11 — v1.0.5

- The project repository moved to `junchengluo9-del/Lx-ui`. Install, update,
  self-update and documentation URLs now point at the new repo. Behaviour and
  the bundled Xray core (v26.6.27) are unchanged.
- Test: `TestNextWeeklyRenewal_RepeatedMidnight` now uses `America/New_York`,
  because tzdata 2026f no longer has a November 2026 fallback in
  `America/Havana`.

## 2026-11-11 — v1.0.6

- Security: Go toolchain bumped to 1.27.4, fixing GO-2026-6859 in `net/http`
  (HTTP/1 server crash from a MaxBytesReader header race).
- Security: `github.com/klauspost/compress` v1.20.2 (GO-2026-6865) and
  `github.com/andybalholm/brotli` v1.2.7 (GO-2026-6888).
- No functional change; bundled Xray stays v26.6.27.

## 2026-11-14 — sync with upstream v3.11.0 (development branch, no release tag)

Lx-ui now tracks upstream `5297fd3c` (v3.11.0 plus 244 commits, 892 files
changed). This is a modification of upstream code, so it is recorded here even
though no release tag was cut with it. The sync was merged from upstream's
tree with the rebranded version of every conflicted file as the starting point,
then Lx-ui's own delta re-applied on top (see `docs/UPSTREAM_SYNC.md`).

Included: everything upstream shipped since the fork point that is a bug fix,
security fix, performance fix or feature — the web/API hardening series
(input bounds, CSRF/session handling, login throttling, connection caps),
subscription rendering and external-subscription trust, node push/answer
budgets and lifecycle fixes, TUIC and AmneziaWG resource bounds, traffic
accounting fixes (reset, quota re-enable, hand-disabled clients), SQLite and
Postgres parity, Xray-core compatibility work (XDRIVE, TUN WFP values, mux
notes, XHTTP h2Params, geodata refusals, freedom finalRules), the screen-based
Telegram bot rewrite, per-client host rules, SNI pools, subscription ordering,
the single route manifest, the Slovak locale, and upstream's documentation.

Kept as Lx-ui's own (not upstream's):

- The bundled Xray-core stays **v26.6.27** (client compatible: no REALITY
  ML-KEM gate), with the recommended/verified/experimental tiers, the REALITY
  gate labels, the pre-update validation and the automatic rollback on a core
  that will not start (#6871). Upstream now ships v26.11.9; that version is
  still installable from the panel as an experimental core.
- `nodeHeartbeatInterval` / `nodeTrafficInterval` / `nodeSyncJitter` and the
  unchanged-payload push skip (#6541).
- `update.sh` keeps the installed core by default (`KEEP_XRAY`), and
  `install.sh`/`update.sh` still fall back to the `dev-latest` prerelease when
  no stable release exists.
- The 3x-ui → Lx-ui migration path (`/etc/x-ui` copy on first start and
  `install.sh --migrate-from-3x-ui`) is unchanged.

Replaced by upstream's own fix (Lx-ui's parallel code removed):

- TUIC per-user routing: upstream's #6875 keeps one SOCKS account per client
  email and adds a relay policy level so relayed traffic is not billed twice.
  Lx-ui's `RelayAccountEmails`/`RecordRelayAccounts`/`RelayAccountsCover` and
  their tests are gone; `internal/tuic/xray_user_routing_test.go` exercises the
  new signature.
- Node-disable latch (#6870): `nodeDisableIsStale` and
  `ClientTrafficEnableMergeExpr` are upstream's; Lx-ui's regression test was
  adapted to the new arguments.
- Quota/disabled-client reset logic, the serialized traffic writer helpers and
  the clash HTTP-header obfs mapping follow upstream's newer implementations.

Excluded: the sponsor module (`/sponsors`, `SponsorCard`, `SponsorSlot`, the
sponsor API/service and its logo-cache fix), donations (sidebar donate button,
`donate.sanaei.dev`), upstream's non-English READMEs, upstream's `CLAUDE.md` /
`.github/claude/*` AI workflows, and every upstream download/update/image URL
that the rebrand guard rejects.

Adaptations worth knowing when reviewing upstream later:

- `internal/web/service/server_xray_update_test.go` (upstream's end-to-end
  `UpdateXray` test) now feeds a fake core that answers `xray version` and
  expects Lx-ui's validation-then-rollback outcome; upstream's own test assumed
  the old "swap, then fail" flow.
- `TestUpdateXray` uses v26.7.28 rather than v26.7.1, because v26.7.1 is below
  the panel's verified set and would be refused as experimental.
- `internal/tuic/socks_bridge_test.go` lost the tests of the removed relay
  helpers.
- Locale files are upstream's plus the 35 Lx-ui-only keys (`xrayTier`,
  `xrayRealityGate`, `xrayRealityGateTip`, `xraySwitchGateWarn`, the node sync
  settings, and the Xray switch warnings).
- `docs/public/openapi.json` is regenerated together with
  `frontend/public/openapi.json`, so `make docs-openapi-check` keeps passing.

Verification on this box (Linux amd64, Go 1.27.4, Node 26.13.1, TypeScript 7):
`go build ./...`, `go vet ./...`, `tools/check-rebrand.sh`, `npm run gen`
(no drift: generated schemas + OpenAPI regenerated from the merged Go sources),
`tsc --noEmit` and `vite build` are green; `go test` passes for
`internal/config`, `internal/xray` (+ dnsconf, geodata), `internal/tuic`,
`internal/sub`, `internal/database`, `internal/web/job`, `internal/web/runtime`,
`internal/web/service` (whole package), `internal/web/service/tgbot` and
`internal/web/service/outbound`. Not run here: `internal/web/controller`,
`internal/web/service/panel`, the amneziawg suites, `internal/util/*` beyond
common/cronspec/crypto/json_util/ldap, `node-e2e`, Postgres, oxlint (the binary
aborts in this sandbox), vitest (worker threads cannot start) and Storybook;
GitHub Actions covers those on the pull request.
