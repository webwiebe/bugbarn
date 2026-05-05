#!/bin/bash
# PreToolUse hook: block git commit if go vet or tests fail.
# Outputs JSON with decision:"block" to prevent the commit.

INPUT=$(cat)
CWD=$(echo "$INPUT" | jq -r '.cwd')

cd "$CWD" || exit 0

# go vet
VET_OUTPUT=$(go vet ./... 2>&1)
if [ $? -ne 0 ]; then
  jq -n --arg reason "go vet failed:
$VET_OUTPUT" '{
    "decision": "block",
    "reason": $reason
  }'
  exit 0
fi

# go build (must compile)
BUILD_OUTPUT=$(go build ./... 2>&1)
if [ $? -ne 0 ]; then
  jq -n --arg reason "Build failed:
$BUILD_OUTPUT" '{
    "decision": "block",
    "reason": $reason
  }'
  exit 0
fi

# go test (fast — 60s timeout)
TEST_OUTPUT=$(go test ./... -timeout 60s -count=1 2>&1)
if [ $? -ne 0 ]; then
  jq -n --arg reason "Tests failed:
$TEST_OUTPUT" '{
    "decision": "block",
    "reason": $reason
  }'
  exit 0
fi

exit 0
