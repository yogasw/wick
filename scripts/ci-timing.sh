#!/usr/bin/env bash
# ci-timing.sh — dump step-level timing + failed step logs for recent workflow runs
# Usage: bash scripts/ci-timing.sh
# Output is written directly to ci-timing.txt in the repo root.

REPO="yogasw/wick"
OUT="$(dirname "$0")/../ci-timing.txt"

{
for workflow in release-artifacts.yml pr-tests.yml release.yml docs-and-sync.yml; do
  echo "========================================"
  echo "WORKFLOW: $workflow"
  echo "========================================"

  run_ids=$(gh run list \
    --repo "$REPO" \
    --workflow "$workflow" \
    --limit 5 \
    --json databaseId,conclusion,createdAt,displayTitle \
    --jq '.[] | [.databaseId, .conclusion, .createdAt, .displayTitle] | @tsv')

  if [ -z "$run_ids" ]; then
    echo "  (no runs found)"
    echo ""
    continue
  fi

  while IFS=$'\t' read -r run_id conclusion created title; do
    echo ""
    echo "RUN $run_id | $conclusion | $created | $title"

    # Step-level timing
    gh run view "$run_id" \
      --repo "$REPO" \
      --json jobs \
      --jq '
        .jobs[] |
        "  JOB: " + .name + " [" + (.conclusion // "running") + "]",
        (
          .steps[] |
          "    STEP " + (.number|tostring) + ": " + .name +
          " [" + (.conclusion // "?") + "]" +
          " | start=" + (.startedAt // "?") +
          " | end=" + (.completedAt // "?")
        )
      '

    # Full logs for failed/cancelled runs
    if [ "$conclusion" = "failure" ] || [ "$conclusion" = "cancelled" ]; then
      echo ""
      echo "  --- FAILED LOGS ---"
      gh run view "$run_id" \
        --repo "$REPO" \
        --log-failed 2>&1 | sed 's/^/  /'
      echo "  --- END FAILED LOGS ---"
    fi

  done <<< "$run_ids"

  echo ""
done
} | tee "$OUT"

echo ""
echo "Written to $OUT"
