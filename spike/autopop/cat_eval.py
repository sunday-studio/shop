import re,sys
kw={}
for l in open('category_keywords.tsv'):
    if l.startswith('#') or '\t' not in l: continue
    k,c=l.rstrip('\n').split('\t');kw[k]=c
def guess(name):
    toks=[t for t in re.split(r'[^a-z]+',name.lower()) if t]
    best=None  # (matchlen, tokenindex, tablepos)
    for ti,t in enumerate(toks):
        for pos,(k,c) in enumerate(kw.items()):
            if t==k: m=len(k)+100       # whole token
            elif len(k)>=4 and t.endswith(k): m=len(k)   # compound head
            else: continue
            cand=(m,ti,-pos,c)
            if best is None or cand>best: best=cand
    return best[3] if best else None
truth=eval(open('truth.py').read())
ok=wrong=none=0;bad=[]
for n,v in truth.items():
    g=guess(n)
    if g is None: none+=1
    elif g==v: ok+=1
    else: wrong+=1;bad.append((n,v,g))
print(len(truth),'correct',ok,'wrong',wrong,'nohit',none);print(bad)
