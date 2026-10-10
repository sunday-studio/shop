import ctranslate2,sentencepiece as spm,time,re,sys,resource
m="model"
t=ctranslate2.Translator(m,device="cpu",compute_type="int8",inter_threads=1,intra_threads=4)
sp=spm.SentencePieceProcessor(model_file=m+"/source.spm");tp=spm.SentencePieceProcessor(model_file=m+"/target.spm")
def tr(xs):
    toks=[sp.encode(x,out_type=str)+["</s>"] for x in xs]
    out=t.translate_batch(toks,beam_size=2,max_decoding_length=40)
    return [tp.decode(o.hypotheses[0]) for o in out]
g=[l.rstrip("\n").split("\t") for l in open("../../api/internal/translate/glossary.tsv") if l.strip() and not l.startswith("#") and "\t" in l]
g=[(a,b) for a,b in g]
t0=time.time();tr(["melk"]);print("warm",round(time.time()-t0,2))
t0=time.time();res=tr([a for a,_ in g]);dt=time.time()-t0
norm=lambda s:re.sub(r"[^a-z ]","",s.lower()).strip()
ex=sum(norm(r)==norm(b) for r,(a,b) in zip(res,g));ct=sum(norm(b) in norm(r) or norm(r) in norm(b) for r,(a,b) in zip(res,g))
print(f"glossary n={len(g)} exact={ex/len(g):.2f} contains={ct/len(g):.2f} ms/term={dt/len(g)*1000:.1f} rssMB={resource.getrusage(resource.RUSAGE_SELF).ru_maxrss/1e6:.0f}")
bad=[(a,b,r) for r,(a,b) in zip(res,g) if norm(r)!=norm(b)][:25]
for x in bad:print(" ",x)
# receipt items
items=[]
for l in open("../out/parse.txt"):
    mm=re.match(r"\s+(item|discount|deposit|bag)\s+-?\d+\s+(.*)",l)
    if mm and mm.group(1)=="item":
        s=re.sub(r"[\d,.]+|^\W+|\W+$","",mm.group(2)).strip()
        if len(s)>3:items.append(s)
items=list(dict.fromkeys(items))
t0=time.time();r2=tr([i.lower() for i in items]);print("receipt items",len(items),"ms/item",round((time.time()-t0)/len(items)*1000,1))
for i,r in zip(items,r2):print(f"  {i} -> {r}")
