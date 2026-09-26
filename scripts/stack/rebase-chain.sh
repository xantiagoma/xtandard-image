#!/bin/zsh
# rebase-chain.sh <branch>... : rebase each branch onto the previous one (using origin/<prev> as the old base).
# Conflicts from commits already squashed into main are resolved by taking HEAD; others via resolve.sh.
S=$(cd "$(dirname "$0")" && pwd)
cd /Users/santi/Projects/xtandard-image
already_upstream() { subj=$(git log -1 --format=%s REBASE_HEAD 2>/dev/null); [ -n "$subj" ] && git log main --format=%B | grep -qF -- "$subj"; }
fix() {
  if already_upstream; then
    for f in ${(f)"$(git diff --name-only --diff-filter=U)"}; do
      if git checkout --ours -- "$f" >/dev/null 2>&1; then git add "$f"; else git rm -q --cached "$f" >/dev/null 2>&1; fi
    done
  else $S/resolve.sh >/dev/null 2>&1; fi
  git diff --name-only --diff-filter=U | grep -q . && return 1; return 0
}
prev=""; for b in "$@"; do
  if [ -n "$prev" ]; then
    rm -f .git/index.lock 2>/dev/null
    if ! git rebase -q --onto $prev origin/$prev $b >/tmp/rb.log 2>&1; then
      while :; do
        if git diff --name-only --diff-filter=U | grep -q .; then fix || { echo "MANUAL in $b ($(git log -1 --format=%s REBASE_HEAD)): $(git diff --name-only --diff-filter=U | tr '\n' ' ')"; exit 1; }; fi
        GIT_EDITOR=true git rebase --continue >/tmp/rb.log 2>&1 && break
        if grep -q 'index.lock' /tmp/rb.log; then sleep 2; rm -f .git/index.lock; continue; fi
        if grep -q 'untracked working tree files would be overwritten' /tmp/rb.log; then sed -n '/would be overwritten/,/Please move/p' /tmp/rb.log | grep -E '^\s' | xargs rm -f; continue; fi
        if git diff --name-only --diff-filter=U | grep -q .; then continue; fi
        if grep -q 'No changes\|nothing to commit' /tmp/rb.log; then GIT_EDITOR=true git rebase --skip >/tmp/rb.log 2>&1 && break; continue; fi
        echo "rebase error in $b:"; tail -3 /tmp/rb.log; exit 1
      done
    fi
  fi
  echo "ok $b $(git rev-parse --short $b) +$(git log --oneline main..$b | wc -l | tr -d ' ')"; prev=$b
done; git checkout -q main
