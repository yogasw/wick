# Wick Mesh — execution checklist and acceptance ledger

Source of truth: [approved original 25-section plan](implementation-plan.md).
Branch: feat/wick-mesh. Target PR: develop. Do not merge or deploy to production without authorization.
Last verified CI checkpoint: 89f3b2e3f415ba582f1930fa193e5093cb84f08d, baseline and mesh-plugin success [45795654415](https://github.com/yogasw/wick/actions/runs/45795654415). Plan copied in 7dec1d9. This is NOT completion of Mesh.

## Execution loop (mandatory on every run)
1. Read this ledger and the original plan from GitHub. Read current branch head and CI logs; reconcile concurrent commits. Termux is not a prerequisite.
2. Pick the highest-priority unfinished dependency below. Inspect real Wick source and interfaces. Implement usable behavior and regression tests, not just scaffolding.
3. Commit code and tests atomically on feat/wick-mesh using current head; verify ref update. Record exact paths and commit.
4. Trigger/inspect isolated CI. On failure inspect exact failing step and logs, fix and rerun. Cancelled or queued is not PASS. Batch related changes so CI can finish.
5. Self-review for correctness, security, lifecycle, usability, accessibility and omissions. Fix findings. Record test evidence tied to SHA. Update this ledger with completed acceptance items and next concrete task.
6. Repeat within the execution budget; on a real blocker try permitted alternatives and work on independent items. Never spend an entire successful GitHub-accessible run merely rereading the plan or reporting status. If no code commit is possible, record the exact error, alternatives attempted, and actionable remaining work. Resume next scheduled run.
7. Goal is complete only when every gate is checked with evidence, not when one suite passes. Open review-ready PR to develop, report evidence and stop schedule only after all acceptance criteria verified. No production deploy. Do not claim unlimited continuous execution between scheduled runs.

## Current implementation inventory (verified at 44ff598)
- Service plugin: plugins/service/wick_mesh; policy GET endpoint, preset/group override helpers.
- meshstate: local durable jobs (NOT distributed lease authority), peer invitations and revocation, signed envelope with process-local replay, pinned mTLS, causal revision merge, verified chunk storage, durable result feed and mTLS pull.
- Connector: plugins/connector/wick_mesh_connector; read-only policy operation.
- Tool: plugins/tool/wick_mesh_tool; admin read-only responsive policy view/filter.
- meshclient: authenticated bounded HTTPS policy client.
- CI: .github/workflows/mesh-ci.yml builds/tests core + three plugins; repeated REST/integration, race, 720 convergence permutations and coverage.
- No evidence of live integrated Mesh runtime, real host auth E2E, quorum or complete UI.

- Core datatable opt-in transactional metadata feed added in bb186d8b42c23444f03d37dfef690c28634ef868; captures actual row/schema writes and deletions, durable per-slug sequence, bounded reads. No payload export, global identity, authorized remote access, runtime activation or per-peer ACK yet. New tests not yet verified.

## Acceptance matrix — all 25 original sections
| Section | Current state | Unfinished evidence gate |
| --- | --- | --- |
| 1 Goals | Partial | Standalone complete nodes, distributed execution, HP offline |
| 2 Architecture | Partial | Core hooks, outbox, runtime, registry and adapters |
| 3 Roles/network | Partial | Runtime capability/quotas, metered policy, no HP transit |
| 4 Selective sync | Partial | Per-object scopes, ACL, preview/backup/identity |
| 5 Protocol | Partial | Metadata change feed implemented (pending CI); payload/field deltas, global IDs, durable dedup ACK, snapshots/retention remain |
| 6 Versions | Partial | Per-scope negotiation, quarantine, migrations, signed rollback |
| 7 Orchestration | Partial local only | Real consensus, fencing, DAG/recovery/retry/side effects |
| 8 SDK/pilot | Pending | Split/execute/reduce, House Hunt pilot, rate limits |
| 9 LLM/agents | Pending | Routing, budgets, scoped checkpoints |
| 10 Security | Partial | Durable replay, key lifecycle, resource ACL, threat model |
| 11 Dashboard | Partial read-only | Functional full dashboard, browser UX and accessibility |
| 12 Phases | Pending | Complete phased delivery evidence |
| 13 Acceptance | Pending | All original scenarios, isolated platform tests |
| 14 ADRs | Pending | Source audit and justified architecture decisions |
| 15 Plugin-first | Partial | Background lifecycle, bundle, signed update/rollback |
| 16 Setup UX | Partial | Pair wizard, groups, overrides, preview, audit |
| 17 Plugin config | Pending | Authorized APIs, desired/effective, OAuth/permissions |
| 18 Tables/files | Partial chunks only | Schema/rows, logical paths, lazy materialization, repair |
| 19 Mobile | Partial result transport | Actual job publication, offline ACK, byte measurement |
| 20 Revised gates | Pending | Each revised phase verified |
| 21 Topology | Pending | Full VPS replication + lightweight HP |
| 22 Gateway | Pending | Stable MCP ingress, remote registry, handoff/quorum |
| 23 HP checklist | Pending | Per-object selection, local eviction, unsynced protection |
| 24 Internal registry | Pending | Actual Wick audit, bot receipts, schedules, exclusions |
| 25 Rollout | Pending | 5 VPS + 1 mobile, partition/failover/overhead |

## Ordered work queue
- [ ] A. Audit real core data APIs, transactions, lifecycle, scheduler and MCP extension points; document ADR/threat model. Start with actual datatable mutation paths and supported host APIs, not direct live SQLite copying.
- [ ] B. Durable versioned resource mutation log/outbox and per-peer cursors; test duplicates, conflicts, crashes, ACLs. Integrate atomically with actual Wick writes through minimal generic core hooks.
- [ ] C. Integrate node identity storage, peer lifecycle, secure runtime listener, resource ACLs and sync APIs; independent plugin runtime startup/shutdown. Prove real two-node operation.
- [ ] D. Adopt established quorum implementation/backend, replicated job/occurrence/receipt state, monotonic fencing and checkpoint handoff; test 3 voters, partitions, stale owner rejection, retry/DLQ.
- [ ] E. SDK, authorized connector registry, remote calls and stable MCP gateway; pilot House Hunt split/reduce, rate limits and safe side effects; 2 then 5/8 workers.
- [ ] F. Datatable/session/plugin/workflow adapters, per-object scope, snapshot catchup, conflict handling, retention, bot coordination. No raw core DB overwrite.
- [ ] G. File manifests, logical IDs, ACL/lazy fetch, safe materialization, replication factor/repair, local eviction and offline state.
- [ ] H. Result-only actual job publication and mobile network policy, versioned configuration/group inheritance, mixed schemas and LLM routing.
- [ ] I. Full functional dashboard and connector parity: pairing, nodes, health, scope checklist, sync progress, jobs, conflicts, logs, versions, budgets and rollback. Desktop/mobile, dark/light, keyboard, safe confirmations, loading/error/offline, browser screenshots and E2E.
- [ ] J. Packaging/versioned updates, signed artifacts, compatibility, ARM64/Linux cross-build, isolated install/update/rollback, backup/restore.
- [ ] K. Full acceptance matrix, five-node + mobile tests, long-offline/snapshot, throughput/bytes/security/regressions; audit fixes, final CI and PR.

## Per-run log format
Date/time UTC | head before | code/test commit | paths | exact CI run/step results | remaining gates | next concrete task | blockers and attempts.
Never mark an entire section done based on a helper or test of a mock. Update this file only with evidence. Keep original plan intact.

## Run 2026-11-26
- Head before: 89f3b2e3f415ba582f1930fa193e5093cb84f08d; run 45795654415 baseline and mesh-plugin success confirmed.
- Code/test commit: bb186d8b42c23444f03d37dfef690c28634ef868; paths internal/agents/workflow/datatable/change_feed.go and change_feed_test.go. Explicit SQLite/PostgreSQL trigger installation, opt-in per resource, transactional sequence and event insert, metadata-only, rollback/restart tests. No remote ACL or replication readiness claim.
- Follow-up in this ledger's commit: durable snapshot_required boundary on every activation after a disabled interval; actual PostgreSQL service CI job and test; existing SQLite tests adjusted. Check exact branch head and workflow run before marking any test passed. Prior push may be cancelled by concurrency.
- Blocker: cloud git ls-remote cannot resolve github.com; local Go 1.23.2 versus repo Go 1.26.0. GitHub connector writes succeeded. Connector commit-workflow query filters to PR runs and returned empty; combined status empty. New CI not verified, no pass claim.
- Next: inspect actual CI for new head, fix failures, prove concurrent PostgreSQL sequence ordering and rebootstrap; implement peer cursor/ACK and authorized payload/snapshot adapters, integrate explicit lifecycle and ACL. Add source audit ADR and threat model. Queue A/B remain open.

## Continuation — peer cursor and ACK
- Verified d8eeb02534b056d99e0b77eb39392d8430511718 CI run 45824215016: mesh-plugin and real PostgreSQL change-feed tests passed; core race failed TestRestartRedeliversOutboxAndHeldReplies (in-memory completion observed before persisted task reply). 9b9d288 adds persisted-state wait to restart fixture; retest pending, no claim of crash atomicity.
- This commit adds trusted local per-peer/per-table durable cursor APIs, bounded page/ACK, duplicate handling, snapshot watermark boundary, disabled-scope rejection, peer isolation and restart tests for SQLite and real PostgreSQL. No network ACL, snapshot creation or verification, or remote apply yet. ACK requires trusted caller verification of durable apply. No retention or deletion enabled.
- Next: verify CI and investigate failures; implement consistent authorized snapshot and actual payload adapter, peer grant/lifecycle integration, and audit concurrent PostgreSQL behavior. Do not call cursor helpers full replication.
