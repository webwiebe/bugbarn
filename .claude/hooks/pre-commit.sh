#!/bin/bash
# PreToolUse hook: block git commit if go vet or tests fail.
# Outputs JSON with decision:"block" to prevent the commit.

INPUT=$(cat)
CWD=$(echo "$INPUT" | jq -r '.cwd')
COMMAND=$(echo "$INPUT" | jq -r '.tool_input.command // empty')

# Settings filter can fire on other commands, and this hook runs the whole suite
# on a host that also runs CI: go on only for a git commit in command position.
echo "$COMMAND" | grep -Eq '(^|[;&|(])[[:space:]]*([A-Za-z_][A-Za-z0-9_]*=[^[:space:]]*[[:space:]]+)*git([[:space:]]+-[Cc][[:space:]]+[^[:space:]]+)*[[:space:]]+commit([[:space:]]|$)' || exit 0

cd "$CWD" || exit 0

export GOMAXPROCS=3

# go vet
VET_OUTPUT=$(go vet -p 2 ./... 2>&1)
if [ $? -ne 0 ]; then
  jq -n --arg reason "go vet failed:
$VET_OUTPUT" '{
    "decision": "block",
    "reason": $reason
  }'
  exit 0
fi

# go build (must compile)
BUILD_OUTPUT=$(go build -p 2 ./... 2>&1)
if [ $? -ne 0 ]; then
  jq -n --arg reason "Build failed:
$BUILD_OUTPUT" '{
    "decision": "block",
    "reason": $reason
  }'
  exit 0
fi

# go test (fast — 60s timeout)
TEST_OUTPUT=$(go test -p 2 ./... -timeout 60s -count=1 2>&1)
if [ $? -ne 0 ]; then
  jq -n --arg reason "Tests failed:
$TEST_OUTPUT" '{
    "decision": "block",
    "reason": $reason
  }'
  exit 0
fi

exit 0
