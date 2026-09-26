#!/bin/sh
# Resolve routine rebase conflicts: go.mod (union), go.sum (union), .golangci.yml (keep upstream/HEAD). Others are reported.
S=$(cd "$(dirname "$0")" && pwd)
cd /Users/santi/Projects/xtandard-image
set -e
for f in $(git diff --name-only --diff-filter=U); do
  case "$f" in
    go.mod)
      # union of require lines from both sides, then tidy
      { git show :2:go.mod; git show :3:go.mod; } | awk '/^\t[a-z]/{print $1" "$2}' | sort -u > /tmp/reqs
      { printf 'module github.com/xantiagoma/xtandard-image\n\ngo 1.27.0\n\ntoolchain go1.27.1\n\nrequire (\n'; awk '{print "\t"$0}' /tmp/reqs; printf ')\n'; } > go.mod
      git add go.mod ;;
    go.sum)
      { git show :2:go.sum 2>/dev/null; git show :3:go.sum 2>/dev/null; } | sort -u > go.sum; git add go.sum ;;
    .golangci.yml)
      git checkout --ours -- .golangci.yml; git add .golangci.yml ;;
    compose.test.yml|compose.acceptance.yml) git checkout --theirs -- "$f"; git add "$f" ;;
    docs/ROADMAP.md) python3 $S/roadmap-merge.py; git add docs/ROADMAP.md ;;
    NOTICE|docs/adr/0006-license-policy.md|internal/security/sign/doc.go|docs/URL-DIALECTS.md)
      # keep both sides of every conflict hunk (additive files)
      python3 - "$f" <<'PY'
import re,sys
p=sys.argv[1]; s=open(p).read()
s=re.sub(r'<<<<<<< [^\n]*\n(.*?)=======\n(.*?)>>>>>>> [^\n]*\n', lambda m: m.group(1)+m.group(2), s, flags=re.S)
open(p,'w').write(s)
PY
      git add "$f" ;;
    *) echo "MANUAL: $f" ;;
  esac
done
if ! mise exec -- go mod tidy >/tmp/tidy.log 2>&1; then echo 'TIDY FAILED'; tail -5 /tmp/tidy.log; fi; git add go.mod go.sum 2>/dev/null; mise exec -- go build ./... >/tmp/build.log 2>&1 || { echo 'BUILD FAILED after resolve'; head -5 /tmp/build.log; }
git diff --name-only --diff-filter=U

# Generated artefacts: take the branch's copy; the adopter regenerates on the stack top
# (`bun run generate` for packages/image-url/src/generated/*, `task` targets for the schema/openapi).
for f in $(git diff --name-only --diff-filter=U | grep -E '^(packages/image-url/src/generated/|internal/config/config\.schema\.json$|internal/apidoc/openapi\.json$)'); do
  git checkout --theirs "$f" && git add "$f"
done
