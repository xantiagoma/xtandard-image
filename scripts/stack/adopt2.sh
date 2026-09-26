#!/bin/zsh
# adopt2.sh <agent-id> <branch> <n-own-commits> <title> <bodyfile>
# Rebases the agent's last N commits onto the current stack top, pushes, adds to stack, opens PR.
S=$(cd "$(dirname "$0")" && pwd)
cd /Users/santi/Projects/xtandard-image
AGENT=$1; BRANCH=$2; N=$3; TITLE=$4; BODY=$5
[ -d ".claude/worktrees/agent-$AGENT" ] && git worktree remove --force ".claude/worktrees/agent-$AGENT"
git branch -D "worktree-agent-$AGENT" -q 2>/dev/null; git worktree prune
git fetch -q origin; git checkout -q main; rm -f .git/index.lock
TOP=$(gh stack view --json | python3 -c "import json,sys; print(json.load(sys.stdin)['branches'][-1]['name'])")
git branch -f "$BRANCH" "origin/$BRANCH" >/dev/null
OLD=$(git rev-parse "$BRANCH~$N")
if ! git rebase -q --onto "$TOP" "$OLD" "$BRANCH" >/tmp/rb.log 2>&1; then
  while :; do
    if git diff --name-only --diff-filter=U | grep -q .; then $S/resolve.sh >/dev/null 2>&1; if git diff --name-only --diff-filter=U | grep -q .; then echo "MANUAL: $(git diff --name-only --diff-filter=U | tr '\n' ' ')"; exit 1; fi; fi
    GIT_EDITOR=true git rebase --continue >/tmp/rb.log 2>&1 && break
    grep -q 'index.lock' /tmp/rb.log && { rm -f .git/index.lock; continue; }
    grep -q 'would be overwritten' /tmp/rb.log && { sed -n '/would be overwritten/,/Please move/p' /tmp/rb.log | grep -E '^\s' | xargs rm -f; continue; }
    grep -q 'CONFLICT' /tmp/rb.log && continue
    echo "rebase error:"; tail -3 /tmp/rb.log; exit 1
  done
fi
git checkout -q main
mise exec -- go build ./... >/tmp/build.log 2>&1 || { echo "BUILD FAILED on $BRANCH"; head -5 /tmp/build.log; exit 1; }
git push -q --force-with-lease origin "$BRANCH"
gh stack top >/dev/null 2>&1; gh stack add "$BRANCH" 2>&1 | tail -1
gh stack sync 2>&1 | tail -1
gh stack submit --auto --open 2>&1 | grep -E 'Created|up to date' | tail -1
gh pr edit "$BRANCH" --title "$TITLE" --body-file "$BODY" >/dev/null
git checkout -q main
gh stack view --json | python3 -c "import json,sys; d=json.load(sys.stdin); print(len(d['branches']),'branches; top:', d['branches'][-1]['name'], '#', d['branches'][-1].get('pr',{}).get('number'), 'needsRebase:', [b['name'] for b in d['branches'] if b['needsRebase']])"
