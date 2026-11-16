# Upstream synchronisation log

Upstream: https://github.com/MHSanaei/3x-ui (git remote `upstream`, push disabled).

## Branch model

| Branch                   | Purpose                                                                                                                                                |
| ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `upstream-main`          | Read-only mirror of `upstream/main`. Never commit here. Refresh with `git fetch upstream && git branch -f upstream-main upstream/main`.                |
| `lx-base`                | Upstream code **before** the rebrand + upstream PRs merged by Lx-ui. Upstream fixes are cherry-picked here first because they apply without conflicts. |
| `main`                   | Lx-ui (lx-base + rebrand + Lx-ui's own work).                                                                                                          |
| `release/x.y`            | Maintenance branches, fixes only.                                                                                                                      |
| `feat/*`, `fix/*`        | Topic branches merged into `main` via review.                                                                                                          |
| `sync/upstream-YYYYMMDD` | Temporary branch used while syncing upstream.                                                                                                          |

## Sync procedure

1. `git fetch upstream`
2. `git log --oneline <last-synced>..upstream/main` and classify each commit.
3. Cherry-pick fixes onto `lx-base` (`git cherry-pick -x <sha>`), then merge
   `lx-base` into `main`. When a commit conflicts with the rebrand on `main`,
   re-run `tools/rebrand.sh` after the merge (it is idempotent).
4. Record the new sync point below.

For a large gap the procedure above is impractical (hundreds of commits touch
the same files the rebrand rewrote), so the 2026-11-14 sync merged
`upstream/main` directly into a `sync/*` branch and resolved conflicts with
this method: for every conflicted file, start from the **rebranded** upstream
version (apply `tools/rebrand.sh` to a worktree of `upstream/main`) and re-apply
Lx-ui's own delta (rebranded base → `main`, i.e. `git diff <rebranded base>
main -- <file>`). That keeps upstream's content authoritative while never losing
a Lx-ui change, and leaves nothing for `tools/rebrand.sh` to guess at.
`docs/LX_CHANGES.md` records what was kept, adapted or dropped.

## Sync points

| Date (UTC+8) | Upstream commit | Notes                                                                                      |
| ------------ | --------------- | ------------------------------------------------------------------------------------------ |
| 2026-10-19   | `a8d0700b`      | Fork point (v3.10.2 + #6859, #6666).                                                       |
| 2026-11-14   | `5297fd3c`      | v3.11.0 + 244 commits (245 total, 892 files). Sponsor/donate/branding excluded; see below. |

## Upstream pull requests merged into lx-base

Merged 2026-10-19 with `git merge --no-ff refs/pull/<N>/head` (one merge commit
each, message carries `Upstream-PR: MHSanaei/3x-ui#<N>`). All 12 merged without
textual conflicts; `go build`, `go vet`, `go test ./...` (5081 pass / 0 fail),
`npm run gen` (no drift), lint, typecheck and vitest (1852 pass) were green afterwards.

| Order | PR    | Title                                                                                  |
| ----- | ----- | -------------------------------------------------------------------------------------- |
| 1     | #6837 | chore(deps): bump i18next 26.4.3 -> 26.4.4 (ReDoS fix)                                 |
| 2     | #6862 | fix(sub): send no-store on every subscription body                                     |
| 3     | #6850 | fix(outbound): correctly dial IPv6 literals in TCP probes                              |
| 4     | #6857 | fix(inbounds): preserve formatting in Advanced JSON editor (#6854)                     |
| 5     | #6843 | fix(sub): pass numeric XMUX counts to Clash reuse-settings (#6841)                     |
| 6     | #6838 | feat(sub): map shadowsocks tcp http header to mihomo obfs plugin (#6735)               |
| 7     | #6825 | feat(sub): export tls cipher suites as cs query param in share links                   |
| 8     | #6831 | fix(hosts): carry an inbound update's externalProxy change onto its imported hosts     |
| 9     | #6851 | fix(clients): keep a re-created client from being orphaned by a stale delete tombstone |
| 10    | #6833 | fix(traffic): re-enable quota-disabled clients on inbound and reset-all resets         |
| 11    | #6830 | fix(iplimit): keep recording client IPs with fail2ban disabled (#6722)                 |
| 12    | #6768 | feat(notifications): failed-check threshold for node.down alerts                       |

Deliberately not merged: #6866 (empty description, rewrites install.sh, -1951
lines), #6829 (superseded upstream by #6859), #6799 (conflicts), #6845
(sponsor module is removed entirely in Lx-ui), #6847 (overlaps the planned
Xray version pinning), plus large feature/draft PRs. See
`/docs/LX_CHANGES.md`.

## 2026-11-14 sync — what a later sync must not re-introduce

All 12 PRs above are in `upstream/main` now (as `0729fc6d`, `07910bf6`,
`5a135d2a`, `864cbd23`, `0b90dc2b`, `2b759f39`, `3acdfd9d`, `47be0635`,
`1dbb3743`, `49c0600f`, `2f62dadd` and the i18next bump inside `775b2b0b`), so a
sync no longer picks them separately — the merge brings them in once.

Included from upstream (all of it, minus the list below):

- v3.11.0 and the 2026-10/11 hardening series: web/API input bounds, CSRF and
  session handling, login throttling, subscription rendering and external
  subscription trust, node push/answer budgets, TUIC and AmneziaWG resource
  bounds, traffic-accounting correctness, SQLite/Postgres parity, Xray-core
  compatibility work (XDRIVE, TUN WFP, mux notes, XHTTP h2Params, geodata
  refusals), the screen-based Telegram bot rewrite, the host/client rule
  binding feature, SNI pools, drag-and-drop ordering, the route manifest, the
  Slovak locale, and the docs.

Excluded or replaced (do not merge these back):

- **Sponsor module** — `/sponsors` page, `SponsorCard`, `SponsorSlot`, sponsor
  API/service and `878d37c2` (sponsor logo cache). Lx-ui has no sponsor feed.
- **Donations** — the sidebar donate button/`donate.sanaei.dev` link and the
  `menu.donate` UI.
- **Upstream branding** — non-English upstream READMEs (upstream ships
  `README.ru_RU` etc.; Lx-ui keeps English + Chinese only), upstream
  `CLAUDE.md` / `.github/claude/*` AI workflows (removed in milestone 1).
- **Bundled Xray-core** — upstream pins v26.10.31/v26.11.9 in `release.yml` and
  `DockerInit.sh`; Lx-ui keeps its client-compatible v26.6.27 pin
  (`xray.RecommendedXrayVersion`), with newer cores installable from the panel.
  `TestRecommendedXrayVersionMatchesPins` fails if the two drift.
- **TUIC per-user routing** — Lx-ui's #6874 implementation
  (`RelayAccountEmails`/`RecordRelayAccounts`/`RelayAccountsCover`) is replaced
  by upstream's #6875 (`SocksInboundSettings(emails, password, userLevel)` plus
  the relay policy level, which also stops double billing). Lx-ui's parallel
  helpers and their tests were dropped.
- **Node-disable latch (#6870)** — Lx-ui's `nodeDisableIsStale` was replaced by
  upstream's `#6870/#6876` version (separate `nodeUsed`, `nodeUp`/`nodeDown`
  parameters in `ClientTrafficEnableMergeExpr`). Lx-ui's regression test was
  adapted to the new signature.
- **Reset/traffic writer refactors** — upstream's `quotaDisabledEmails(within)`,
  `handDisabledEmails`, `zeroClientTraffics` and `ResetAllClientTraffics`
  return values supersede Lx-ui's earlier variants.
