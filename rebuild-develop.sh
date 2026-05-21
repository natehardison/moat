#!/bin/sh
# rebuild-develop.sh — run inside the dedicated `develop` worktree.
# develop is DERIVED: upstream/main + each in-flight feat/* branch from branches.txt.
#
# Conflict handling:
#   - CHANGELOG.md uses the `union` merge driver (see .git/info/attributes setup
#     below). Every feature branch adds a bullet under ## Unreleased; union keeps
#     both sides instead of conflicting.
#   - git rerere is enabled. The first time you resolve a conflict by hand and
#     commit, the resolution is recorded. Subsequent rebuilds replay it.
#   - For branches that genuinely contradict (e.g. one deletes a file the other
#     edits), keep a pre-merged "combined" branch on origin and list it in
#     branches.txt instead of the two source branches.
set -e

cd "$(dirname "$0")"
COMMON_DIR=$(git rev-parse --git-common-dir)

# --- One-time local setup (idempotent) -----------------------------------
mkdir -p "$COMMON_DIR/info"
ATTRS="$COMMON_DIR/info/attributes"
if ! grep -qsx 'CHANGELOG.md merge=union' "$ATTRS"; then
  echo 'CHANGELOG.md merge=union' >> "$ATTRS"
fi
[ "$(git config --get rerere.enabled)" = "true" ] || git config rerere.enabled true
[ "$(git config --get rerere.autoupdate)" = "true" ] || git config rerere.autoupdate true

# --- Guard 1: clean working tree (tooling files exempted) ----------------
# Dirty tooling files are expected and intentional — they get snapshotted and
# re-committed below. Dirt in any other file means uncommitted real work, bail.
dirty=$(git status --porcelain | awk '{print $2}' \
  | grep -vE '^(rebuild-develop\.sh|branches\.txt)$' || true)
if [ -n "$dirty" ]; then
  echo "working tree dirty — commit on a feat/* branch or stash first:" >&2
  echo "$dirty" | sed 's/^/  /' >&2
  exit 1
fi

# --- Fetch everything that branches.txt might reference ------------------
git fetch --quiet upstream
git fetch --quiet origin

BRANCHES=$(sed '/^[[:space:]]*$/d; /^#/d' branches.txt)

# --- Guard 2: refuse if develop has real direct (non-merge) commits -----
# Merge commits unique to develop are this script's own output from a previous
# rebuild — safe to discard. A non-merge commit unique to develop is suspect:
# either it's the tooling commit (only touches rebuild-develop.sh/branches.txt
# — recreated below, so safe) or a real direct commit we must protect.
# shellcheck disable=SC2086
direct=$(git rev-list --no-merges develop --not upstream/main $BRANCHES 2>/dev/null || true)
suspicious=""
for c in $direct; do
  if git show --pretty=format: --name-only "$c" \
       | grep -vE '^$|^(rebuild-develop\.sh|branches\.txt)$' >/dev/null; then
    suspicious="$suspicious $c"
  fi
done
if [ -n "$suspicious" ]; then
  echo "develop has direct (non-tooling) commits — inspect before rebuild:" >&2
  for c in $suspicious; do
    echo "  $(git log -1 --oneline "$c")" >&2
  done
  exit 1
fi

# --- Rebuild --------------------------------------------------------------
# Snapshot the develop-only tooling files so we can re-commit them on top of
# upstream/main. They are tracked on develop, so `reset --hard upstream/main`
# would otherwise delete them. The running shell has the script already
# mapped, so deleting+restoring rebuild-develop.sh mid-run is safe.
TOOLING_FILES="rebuild-develop.sh branches.txt"
SNAPSHOT=$(mktemp -d)
trap 'rm -rf "$SNAPSHOT"' EXIT
for f in $TOOLING_FILES; do
  [ -e "$f" ] && cp "$f" "$SNAPSHOT/"
done

git reset --hard upstream/main

for f in $TOOLING_FILES; do
  [ -e "$SNAPSHOT/$f" ] && cp "$SNAPSHOT/$f" "$f"
done
chmod +x rebuild-develop.sh
# shellcheck disable=SC2086
git add $TOOLING_FILES
if ! git diff --cached --quiet; then
  git commit -m "chore(tooling): develop rebuild script + branch list"
fi

echo "$BRANCHES" | while IFS= read -r b; do
  [ -z "$b" ] && continue
  printf '\n=== merging %s ===\n' "$b"
  if git merge --no-ff --no-edit "$b"; then
    continue
  fi
  # Merge failed. If rerere auto-resolved everything, finalize the commit.
  if [ -z "$(git diff --name-only --diff-filter=U)" ]; then
    echo "(rerere resolved all conflicts — committing)"
    git commit --no-edit
    continue
  fi
  # Real unresolved conflicts. Bail with instructions.
  cat >&2 <<EOF

unresolved conflicts while merging $b:
$(git diff --name-only --diff-filter=U | sed 's/^/  - /')

Resolve them in this worktree, then:
  git add <files>
  git commit --no-edit
  ./rebuild-develop.sh   # re-run; rerere will replay this resolution next time

If $b genuinely contradicts another branch in branches.txt (e.g. one deletes a
file the other edits), build a pre-merged combined branch on origin and list
that instead.
EOF
  exit 1
done

echo
echo "develop rebuilt. tip: $(git rev-parse --short HEAD)"
