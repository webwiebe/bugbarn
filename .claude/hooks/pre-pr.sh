#!/bin/bash
# PreToolUse hook: block gh pr create if branch is not up to date with main.
# Prevents PRs that will have merge conflicts and won't trigger CI.

INPUT=$(cat)
CWD=$(echo "$INPUT" | jq -r '.cwd')

cd "$CWD" || exit 0

# Fetch latest main
git fetch origin main --quiet 2>/dev/null

# Check if current branch contains all commits from origin/main
if ! git merge-base --is-ancestor origin/main HEAD 2>/dev/null; then
  BEHIND=$(git rev-list --count HEAD..origin/main 2>/dev/null)
  jq -n --arg reason "Branch is ${BEHIND} commit(s) behind origin/main. Rebase or merge main first to avoid merge conflicts that block CI." '{
    "decision": "block",
    "reason": $reason
  }'
  exit 0
fi

exit 0
