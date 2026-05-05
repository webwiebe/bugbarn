#!/bin/bash
# PostToolUse hook: run go build after Write/Edit to catch compile errors early.
# Non-blocking — shows feedback to Claude but doesn't prevent further edits.

INPUT=$(cat)
CWD=$(echo "$INPUT" | jq -r '.cwd')
TOOL=$(echo "$INPUT" | jq -r '.tool_name')
FILE=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty')

# Only check .go files
if [[ -n "$FILE" && "$FILE" != *.go ]]; then
  exit 0
fi

cd "$CWD" || exit 0

OUTPUT=$(go build ./... 2>&1)
if [ $? -ne 0 ]; then
  echo "$OUTPUT" >&2
  echo "Build failed after editing ${FILE##*/}" >&2
  exit 1
fi

exit 0
