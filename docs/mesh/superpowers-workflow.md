# Wick Mesh — Superpowers execution protocol

Upstream: https://github.com/obra/superpowers
Pinned upstream revision: 22f0b463e7a5e579904dd6211a520321035526f7.
Use official skills at this revision: executing-plans, writing-plans, test-driven-development, systematic-debugging, verification-before-completion, requesting-code-review. Read their SKILL.md files directly from GitHub; do not execute unreviewed downloaded scripts.

## Integration and rulings
- User approved the existing 25-section specification and inline autonomous execution. No routine checkpoints requiring user responses.
- Ruling: use existing feature branch and isolated cloud/GitHub Actions as workspace — GitHub connector works but local clone has failed; cost is lack of native worktree/scripts. The repository execution ledger replaces ephemeral upstream workspace files. Never edit production or Termux source.
- Ruling: use inline execution, no fabricated subagents — the current harness does not expose independent coding reviewer agents. Self-review diffs, and use a fresh scheduled context for final branch review; do not call that an independent human review.
- Ruling: where Go 1.26 full checkout is unavailable locally, isolated GitHub Actions is the full-suite authority. Standard-library-only tests can run against byte-for-byte verified GitHub source in cloud Go 1.23 for a narrow RED/GREEN; label that limitation. If CI is needed to prove RED, keep the expected-failure experiment isolated and record output before implementing GREEN; do not mark a test written but unrun as verified.
- Source spec remains docs/mesh/implementation-plan.md; work queue and ledger remain docs/mesh/execution-checklist.md. No merging or production deployment without permission.

## Each task
1. Record base SHA, exact files, interface contract, expected test failure, test command, and expected output in a small task brief. Check producer/consumer compatibility and security boundaries.
2. Write a regression/feature test first. Execute it on the unmodified behavior and inspect actual failure. Fix a broken fixture before implementing.
3. Implement minimal correct behavior. Run the same test, then relevant suites/race/integration and full CI. Do not claim success without actual output.
4. Debug by reproducing, tracing the cause, comparing known working paths, testing one hypothesis, fixing and rerunning. Do not patch around failures or weaken assertions.
5. Review the diff against specification, ACL/security, lifecycle, error handling and UI/UX. Record rulings and outstanding issues in ledger.
6. Commit code and test; record RED/GREEN command/results, exact hashes, and final CI evidence. Mark task complete only after all its gates pass. Resume next unfinished task automatically, not just status checks.
7. At final gate, review entire branch against all 25 sections; fix critical/important issues and verify fresh full suite and real multi-node/mobile/desktop acceptance. Prepare PR to develop only when complete. Keep schedule enabled while incomplete.

## Next concrete briefs
- Verify latest full CI and snapshot/peer cursor tests. Investigate failures using systematic debugging.
- Datatable: design authorized versioned snapshot/delta export and apply adapter, explicit resource/row ACL, global IDs and tombstones; test unauthorized scope, disabled scope, snapshot boundaries, conflict and rollback before implementing. Large snapshot pagination must maintain a consistent watermark.
- Runtime: durable private node identity and explicit key lifecycle; test restart, concurrent creation, invalid file and permissions. Integrate paired listener and authenticated management API; test real two-node host lifecycle.
- Coordination: choose established consensus implementation with documented ADR; test quorum, partition, stale fence, durable job acceptance and checkpoint handoff. Never promote LocalJobs to distributed lease authority.
- Connector/dashboard: real controls, not mock state; test host authorization and desktop/mobile interactions, error/offline and accessibility. Continue all remaining checklist gates.
