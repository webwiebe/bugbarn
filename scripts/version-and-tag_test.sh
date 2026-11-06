#!/usr/bin/env bash
# Tests for scripts/version-and-tag.sh. Two local bare repos stand in for Gitea
# (origin) and the GitHub mirror; each run works in a fresh clone of origin, as
# a pipeline step does.
#
# Run it with: bash scripts/version-and-tag_test.sh   (or `make test-gates`)
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="$REPO/scripts/version-and-tag.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.invalid
export GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.invalid
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

fail=0
check() { # name, want, got
  if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: want [$2], got [$3]"; fail=1; fi
}

git init -q --bare "$WORK/origin.git"
git init -q --bare "$WORK/github.git"
git clone -q "$WORK/origin.git" "$WORK/seed" 2>/dev/null
(
  cd "$WORK/seed"
  git commit -q --allow-empty -m one
  git tag v0.236.10
  git commit -q --allow-empty -m two
  git push -q origin HEAD:main v0.236.10
)
export GITHUB_REMOTE="$WORK/github.git"

# run <dir>: a fresh clone of origin at main, with its tags, running the script.
run() {
  rm -rf "$WORK/$1"
  git clone -q --branch main "$WORK/origin.git" "$WORK/$1" 2>/dev/null
  (cd "$WORK/$1" && git fetch -q --tags origin && bash "$SCRIPT" >"$WORK/$1.log" 2>&1)
}
tags_at() { git -C "$1" tag --points-at "$(git -C "$WORK/seed" rev-parse HEAD)" | tr '\n' ' ' | sed 's/ $//'; }
remote_has() { git ls-remote --tags "$1" "refs/tags/$2" | wc -l | tr -d ' '; }

# 1. An untagged HEAD gets the next patch version on Gitea and GitHub.
run first
check "untagged HEAD: .relver" "VERSION=v0.236.11" "$(head -n 1 "$WORK/first/.relver")"
check "untagged HEAD: VERSION_NUM" "VERSION_NUM=0.236.11" "$(sed -n 2p "$WORK/first/.relver")"
check "untagged HEAD: Gitea tags" "v0.236.11" "$(tags_at "$WORK/origin.git")"
check "untagged HEAD: GitHub has it" "1" "$(remote_has "$WORK/github.git" v0.236.11)"

# 2. Pipeline 853: the tag reached Gitea and the GitHub push failed. A rerun on
#    the tagged HEAD cuts no new tag and pushes the existing one to GitHub.
git -C "$WORK/github.git" tag -d v0.236.11 >/dev/null
run rerun
check "rerun: .relver keeps the tag" "VERSION=v0.236.11" "$(head -n 1 "$WORK/rerun/.relver")"
check "rerun: no new tag on Gitea" "v0.236.11" "$(tags_at "$WORK/origin.git")"
check "rerun: GitHub gets the tag" "1" "$(remote_has "$WORK/github.git" v0.236.11)"
check "rerun: log says reused" "HEAD already carries v0.236.11; reusing it" "$(head -n 1 "$WORK/rerun.log")"

# 3. A rerun when GitHub has the tag too pushes nothing.
run again
check "rerun, GitHub complete: no new tag" "v0.236.11" "$(tags_at "$WORK/origin.git")"
check "rerun, GitHub complete: no push" "GitHub already has v0.236.11" "$(tail -n 1 "$WORK/again.log")"

# 4. The next commit bumps from the nearest tag and skips a version that is
#    already taken (here v0.236.12, on an older commit).
(
  cd "$WORK/seed"
  git tag v0.236.12 HEAD~1
  git commit -q --allow-empty -m three
  git push -q origin HEAD:main v0.236.12
)
run next
check "next commit: skips a taken version" "VERSION=v0.236.13" "$(head -n 1 "$WORK/next/.relver")"

# 5. Pipeline 875: Gitea's push mirror put the tag on GitHub between the check
#    and the push, so GitHub rejected the push. A pre-receive hook plays the
#    mirror: it sets the tag to the pushed commit itself and rejects the push.
#    The commit is pushed as a branch first so the hook's object is present.
(
  cd "$WORK/seed"
  git commit -q --allow-empty -m four
  git push -q origin HEAD:main
  git push -q "$WORK/github.git" HEAD:refs/heads/main
)
cat >"$WORK/github.git/hooks/pre-receive" <<'HOOK'
#!/usr/bin/env bash
while read -r _ new ref; do
  env -u GIT_QUARANTINE_PATH -u GIT_OBJECT_DIRECTORY -u GIT_ALTERNATE_OBJECT_DIRECTORIES \
    git update-ref "$ref" "$new"
done
echo "cannot lock ref: reference already exists" >&2
exit 1
HOOK
chmod +x "$WORK/github.git/hooks/pre-receive"
status=0
run race || status=$?
rm "$WORK/github.git/hooks/pre-receive"
check "mirror race: step passes" "0" "$status"
check "mirror race: .relver" "VERSION=v0.236.14" "$(head -n 1 "$WORK/race/.relver")"
check "mirror race: push was rejected" "1" "$(grep -c 'reference already exists' "$WORK/race.log")"
check "mirror race: tag accepted" "GitHub already has v0.236.14" "$(tail -n 1 "$WORK/race.log")"

# 6. GitHub holds the next version at another commit. The step fails and says so.
(
  cd "$WORK/seed"
  git push -q "$WORK/github.git" HEAD~1:refs/tags/v0.236.15
  git commit -q --allow-empty -m five
  git push -q origin HEAD:main
)
status=0
run elsewhere || status=$?
check "tag elsewhere on GitHub: step fails" "1" "$status"
check "tag elsewhere on GitHub: says where" "1" "$(grep -c '^GitHub has v0.236.15 at .*, but HEAD is ' "$WORK/elsewhere.log")"

if [ "$fail" -ne 0 ]; then
  cat "$WORK"/*.log
  exit 1
fi
echo "version-and-tag tests passed"
