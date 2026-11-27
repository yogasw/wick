# Wick Mesh — execution checklist and acceptance ledger

Source of truth: [approved original 25-section plan](implementation-plan.md).
Branch: feat/wick-mesh. Target PR: develop. Do not merge or deploy to production without authorization.
Last verified CI checkpoint: 4cbd40f9dc1ed4fd438154b9bd8f986a815a3780, baseline, change-feed-postgres and mesh-plugin success [45832007588](https://github.com/yogasw/wick/actions/runs/45832007588). Plan copied in 7dec1d9. This is NOT completion of Mesh.

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

## Continuation — bounded consistent datatable export
- CI 45829112950 at ffa6590: mesh-plugin passed, baseline and real PostgreSQL failed; logs showed test IDs containing underscores rejected by existing parse.ValidateID, and restart fixture comparing raw A2A enum against normalized state. Both corrected at babddfd7ad381f8921b53b0397324920af27cca0. Recheck CI, do not mark passed yet.
- This change adds trusted local bounded snapshot export using SQLite read transaction / PostgreSQL repeatable-read, consistent watermark/schema/rows, deletion marker, unknown JSON retention, limit rejection and snapshot-to-delta catchup tests on both engines. Does NOT expose remote endpoint, apply data, enforce caller/row ACL, or support larger paginated exports. Source IDs/ACL are not portable authority. Next: verify CI, then authorized paginated snapshot and payload/apply path with global IDs, conflict policy and lifecycle; continue independent quorum and runtime tasks.

## Superpowers adoption and verified RED/GREEN
- Upstream obra/superpowers pinned at 22f0b463e7a5e579904dd6211a520321035526f7; adapted workflow and rulings: [superpowers-workflow.md](superpowers-workflow.md).
- Base source jobs.go blob 895c16fd9c7320f3546b923f5c69974321350928 matched cloud copy exactly (6108 bytes). TestPoisonedJobsRejectStaleState: RED on original code (exit 1, five failing cases; Cancel replay incorrectly returns success). GREEN after adding poison check before Claim, mutateLease and Cancel state access: GO111MODULE=off go test -race -count=10 -run '^TestPoisonedJobsRejectStaleState$' . passed (1.091s) on isolated Go 1.23.2; fixed source blob 5b8eb0f296cc935f0610f067e195f82e4e0311ee, test blob 5e5b9384094364cf0766d071fedf0495238c13fd. This is fault-state injection, not an actual filesystem fsync-failure test. Full Go 1.26 CI still required. LocalJobs is not quorum coordination.
- CI 45829980430 at dc0307b: real PostgreSQL and Mesh plugin jobs success; baseline was still running when checked. Next: verify final status, continue work queue using Superpowers method.

## Continuation — persistent node identity (Superpowers)
- Base: c2cac48049f93b2a1d692db722d23ef95bbcf829. Task brief: OpenNodeIdentity(dir string) returns the same Ed25519 key across restart and concurrent opens, creates private seed atomically, refuses unsafe or corrupt state instead of changing identity. Files: meshstate/identity.go and identity_test.go. RED: isolated cloud standard-library-only Go 1.23.2, stub with explicit unimplemented error, GO111MODULE=off go test -count=1 -run '^TestNodeIdentity' . failed on concurrent/restart behavior as expected. GREEN: GO111MODULE=off go test -race -count=10 -run '^TestNodeIdentity' . passed (1.140s), including 16 simultaneous opens and rejection of corrupt/public/symlink/directory seed. Expected Git blobs identity.go ed658660d83197ad40821aa5d1c3626b61f54d4f, identity_test.go f7b91fd575cf55cbd768bfd07707954a3a5ccc38. Full Go 1.26 CI pending. Ruling: dedicated private directory under pre-existing trusted parent, hard-link publication + fsync, fail closed on unsupported filesystem; Windows and ARM64/runtime validation remain pending. Key rotation, lifecycle and real listener remain unfinished. No key material is replicated or exposed by this API.
- CI 45830432143 at c2cac48: Mesh plugin and real PostgreSQL success, baseline still running at last check. Next inspect final CI, connect identity to plugin-owned runtime with explicit lifecycle; continue remaining queue. No completion claim for C or overall goal.

## Mandatory final acceptance gates (user, 2026-11-26)
All 12 must be satisfied with current-commit evidence; no partial completion counts.
- [ ] Seluruh 25 bagian plan terpenuhi
- [ ] Core dan integrasi Mesh selesai
- [ ] Connector selesai
- [ ] Service plugin selesai
- [ ] Tools dan dashboard HTML selesai
- [ ] SDK dan distributed orchestration selesai
- [ ] Sinkronisasi data, file, dan mobile selesai
- [ ] Failover, quorum, dan MCP gateway selesai
- [ ] UI/UX desktop dan mobile teruji
- [ ] Unit, integration, REST, E2E, race dan security test lulus
- [ ] Seluruh temuan audit diperbaiki
- [ ] PR ke develop siap

## Continuation — dedicated peer listener
- Base: 121fefa6d8c9c569588dacf2d08646f1ee48a090. CI 45830939566 at 121fefa: mesh-plugin and change-feed-postgres success; baseline was still in progress. Task: explicit TLS listener lifecycle, bounded HTTP server, rejection of unsafe TLS configuration, actual socket test with client certificate and cancellation.
- RED: isolated cloud Go 1.23.2, stub returns not implemented; GO111MODULE=off go test -count=1 -run '^TestPeerListener' . failed as expected at TestPeerListenerLifecycle. GREEN: GO111MODULE=off go test -race -count=10 -run '^TestPeerListener' . passed (1.308s). Git blobs peer_listener.go 46ab3de09bd851db304ffb167223a43de7d74ffc and peer_listener_test.go c8327ac5863f25bd3a0fec23bb102d071ffef6bc. Fixture verifies certificate requirement, not pinning itself; actual pinning is in ServerTLS. Added StartResultPeer wiring to existing pinned TLS and ResultHandler; full integration verification pending. Next: exercise this wiring with actual paired nodes and restart, certificate rotation, service lifecycle. No host activation or deployment. No gate marked complete.

## Continuation — rotating result-peer leaf certificate (2026-11-27)
- Base 4cbd40f9dc1ed4fd438154b9bd8f986a815a3780; CI 45832007588 all three jobs success verified. Work queue C/H, plan sections 10/19. Task: dedicated listener must survive 24-hour leaf expiry without changing pinned node identity or relaxing mTLS.
- RED: cloud Go 1.23.2, original peer_listener.go Git blob 46ab3de09bd851db304ffb167223a43de7d74ffc matched exactly; callback-only test on actual loopback TLS socket failed with "invalid peer listener configuration". GREEN: use callback-only certificate configuration to avoid IP/no-SNI static certificate bypass; renew within one hour, serialize renewal, fail closed on mint failure or changed public key. Wire StartResultPeer to this callback with owned private-key copy.
- Local narrow verification: GO111MODULE=off go test -race -count=20 . (2.488s), go vet . and go build . passed in isolated cloud with peer_listener.go, peer_rotation_test.go and original peer_listener_test.go (blob c8327ac5863f25bd3a0fec23bb102d071ffef6bc matched). Expected changed blobs: listener 4d20976d2de880d8a1d88f7ec54efbc648f2a99d, rotation test 2b8a337e128ddf71bf4408cac82c68cc2769cd69. TLS client fixture checks key and expiry; server fixture only checks certificate requirement, not actual peer ACL. ARM64 cross-build timed out; not a pass.
- New result_peer_test.go tests actual pairing, durable publication/pull, 25-hour clock advance, restart, cursor catch-up and revocation using production transport. Full integration test is not yet executed locally or confirmed by CI. Core host activation, key rotation/re-pairing, client certificate lifecycle, per-user/object ACL and real mobile job publication still remain. No gate completed.
- Next: verify exact commit blobs and full CI, fix any failures, exercise complete paired runtime and authorized host lifecycle, continue datatable payload apply and quorum backend. No production changes.

## Continuation — durable signed-envelope replay admission (2026-11-27)
- Base 9bd258034776bc333b5e42144ae7362a806dba01. Latest push CI could not be enumerated (combined status and PR-only run query both empty); last known full CI remains 4cbd40f9. Local clone again failed DNS. No claim about latest full CI.
- Task: work queue C, section 10: protect signed messages against nonce reuse after restart, widened allowed skew, rollback of trusted clock. RED on exact original wire.go blob 954c4b98c915762884ec78b61e85015744e34c74, TestReplayChangingSkew and TestReplayClockRollback both accepted replay (exit 1). RED for new OpenReplayWindow contract used a process-local stub and observed restart replay acceptance. GREEN: max five-minute retention, clock high-water, private bounded fsynced nonce ledger, fail closed on write failure and corrupt state. Existing NewReplayWindow remains in-memory, callers must explicitly use durable variant.
- Local Go 1.23.2 standard-library subset: GO111MODULE=off go test -race -count=20 . passed (1.551s), go vet ., go build ., GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c passed (ELF aarch64 cross-build only). Original wire_test.go matched fb4520d14d46b6753e6d3ae89862f7223fb6a058. Tested wire.go 5d7930637bc5c3fa520ae41b3f5f6a4d8f419e4c, replay_store.go aff951ab0a872e137e3cdc581619b11b5bd55389, replay_regression_test.go 12481c1a4e5c014a7e20e2509a47b23a214c9be0. Initial GREEN fixture needed private directory chmod; corrected, reran all tests. No full repo verification.
- Ruling: single process owns a trusted local ledger, file loss/old backup invalidates replay guarantee. Durable admission may reject retries after crash; does not promise exactly-once or replace command/side-effect receipts. Explicit runtime ownership, ACL, live signed-command integration, storage limits/benchmark and multi-node consensus still required. No acceptance gate closed.
- Next: full CI verification (including prior TestResultPeerLifecycle), integrate explicit background service lifecycle. Actual pkg/service/serve.go only invokes Register before initial Configure, with no background lifecycle hook; design minimal configure/close hooks or supported plugin-owned lifecycle without booting on manifest dump. Do not expose management writes before host caller authorization audit.
