import re,sys
p='docs/ROADMAP.md'; s=open(p).read()
def fix(m):
    a,b=m.group(1),m.group(2)
    ids={l.split('|')[1].strip() for l in a.splitlines() if l.startswith('| WP-')}
    extra=[]
    for l in b.splitlines():
        if l.startswith('| WP-'):
            cells=[c.strip() for c in l.strip().strip('|').split('|')]
            if cells[0] not in ids:
                # insert a status cell if HEAD's table has the Status column and this row lacks it
                head_cols=a.splitlines()[0].count('|')-1 if a.splitlines() else 0
                if head_cols==6 and len(cells)==5: cells.insert(1,'in PR')
                extra.append('| '+' | '.join(cells)+' |')
    return a.rstrip('\n')+'\n'+('\n'.join(extra)+'\n' if extra else '')
s=re.sub(r'<<<<<<< [^\n]*\n(.*?)=======\n(.*?)>>>>>>> [^\n]*\n', fix, s, flags=re.S)
open(p,'w').write(s)
