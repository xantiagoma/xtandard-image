#!/usr/bin/env python3
"""Resolve docs/HANDOFF.md conflicts during a stack rebase: inside "Known gaps" the branch
(theirs) wins; everywhere else the base (ours, which carries main's current state) wins."""
import re
p = 'docs/HANDOFF.md'
s = open(p).read()
def merge(m):
    ours, theirs = m.group(1), m.group(2)
    if 'Known gaps' in s[:m.start()].rsplit('## ', 1)[-1]:
        return theirs
    return ours
s = re.sub(r'<<<<<<< [^\n]*\n(.*?)=======\n(.*?)>>>>>>> [^\n]*\n', merge, s, flags=re.S)
assert '<<<<<<<' not in s
open(p, 'w').write(s)
