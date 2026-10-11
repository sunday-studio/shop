#!/usr/bin/env python3
"""Generates the website design boards for the canvas (.dc.html) and flat HTML previews to check them in a browser.
   python3 gen.py <out-dir>   (writes <out>/project/*.dc.html and <out>/preview/*.html)"""
import json, os, re, sys
HERE=os.path.dirname(os.path.abspath(__file__))
A=json.load(open(f'{HERE}/assets.json'))
OUT=sys.argv[1] if len(sys.argv)>1 else 'out'
os.makedirs(f'{OUT}/project',exist_ok=True); os.makedirs(f'{OUT}/preview',exist_ok=True)

TOK={'light':dict(bg='#FFFFFF',sunk='#F4F4F3',line='#E6E6E2',ink='#1D1D1B',ink2='#6B6B66',ink3='#B4B4AF',btn='#1D1D1B',btnInk='#FFFFFF',blue='#0A84FF',blueText='#0071E3',blueTint='#E5F1FF',yellow='#FFCC1A',green='#18B663',greenText='#0B8A47',amber='#FF9500',amberText='#B25E00',paper='#F7F9FF',shadow='rgba(29,29,27,.16)',bezel='#1D1D1B'),
     'dark':dict(bg='#0C0C0B',sunk='#1A1A18',line='#2A2A27',ink='#F4F4F1',ink2='#9B9B95',ink3='#5C5C58',btn='#F4F4F1',btnInk='#0C0C0B',blue='#4DA3FF',blueText='#4DA3FF',blueTint='#0E2236',yellow='#FFCC1A',green='#3DDC84',greenText='#3DDC84',amber='#FFB340',amberText='#FFB340',paper='#E9ECF4',shadow='rgba(0,0,0,.55)',bezel='#2A2A27')}
VARS=['bg','sunk','line','ink','ink2','ink3','btn','btnInk','blue','blueText','blueTint','yellow','green','greenText','amber','amberText','paper','shadow','bezel']
SHOTS=['home','receipt','analytics','products','product']

FONTFACE=''.join(f"@font-face{{font-family:'Inter';src:url({A['fonts'][f]}) format('woff2');font-weight:{w};font-display:swap}}" for f,w in [('Inter-Regular',400),('Inter-Medium',500),('Inter-SemiBold',600)])+f"@font-face{{font-family:'Geist Mono';src:url({A['fonts']['GeistMono-Regular']}) format('woff2');font-weight:400;font-display:swap}}"
FONTFACE_PREVIEW=''.join(f"@font-face{{font-family:'Inter';src:url(../../fonts/{f}.woff2) format('woff2');font-weight:{w}}}" for f,w in [('Inter-Regular',400),('Inter-Medium',500),('Inter-SemiBold',600)])+"@font-face{font-family:'Geist Mono';src:url(../../fonts/GeistMono-Regular.woff2) format('woff2');font-weight:400}"

GH='<svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M12 .5C5.65.5.5 5.65.5 12c0 5.08 3.29 9.39 7.86 10.91.58.1.79-.25.79-.56v-2c-3.2.7-3.87-1.37-3.87-1.37-.52-1.33-1.28-1.69-1.28-1.69-1.04-.71.08-.7.08-.7 1.15.08 1.76 1.18 1.76 1.18 1.03 1.76 2.69 1.25 3.35.96.1-.75.4-1.25.73-1.54-2.55-.29-5.24-1.28-5.24-5.69 0-1.26.45-2.28 1.18-3.09-.12-.29-.51-1.46.11-3.05 0 0 .97-.31 3.17 1.18a11 11 0 0 1 5.77 0c2.2-1.49 3.17-1.18 3.17-1.18.63 1.59.23 2.76.11 3.05.74.81 1.18 1.83 1.18 3.09 0 4.42-2.69 5.39-5.25 5.67.41.36.78 1.06.78 2.14v3.17c0 .31.21.67.8.56A11.5 11.5 0 0 0 23.5 12C23.5 5.65 18.35.5 12 .5Z"/></svg>'
BADGE=open(f'{HERE}/../logo-badge.svg').read().replace('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" width="512" height="512" role="img" aria-label="Logo badge">','<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" style="width:100%;height:100%;display:block" aria-hidden="true">').replace('id="g"','id="bg1"').replace('url(#g)','url(#bg1)')

CSS='''
body{margin:0}
.w{font-family:'Inter',system-ui,-apple-system,sans-serif;font-feature-settings:'tnum' 1,'cv11' 1;background:var(--bg);color:var(--ink);-webkit-font-smoothing:antialiased;line-height:1.5;overflow:hidden}
.w *{box-sizing:border-box}
.w h1,.w h2,.w h3,.w p{margin:0}
.w a{color:inherit;text-decoration:none}
.w .mono{font-family:'Geist Mono',ui-monospace,monospace}
.w .brand{display:inline-flex;align-items:center;gap:10px;font-weight:600;letter-spacing:-.03em;font-size:21px;line-height:1}
.w .brand .mk{width:32px;height:32px;display:block;flex:none}
.w .nav{position:sticky;top:0;z-index:5;display:flex;align-items:center;justify-content:space-between;height:68px;background:color-mix(in srgb,var(--bg) 88%,transparent);backdrop-filter:saturate(1.4) blur(14px);border-bottom:1px solid var(--line)}
.w .btn{display:inline-flex;align-items:center;gap:10px;height:52px;padding:0 24px;border-radius:26px;font-weight:500;font-size:16px;background:var(--btn);color:var(--btnInk);transition:transform .16s cubic-bezier(.22,1,.36,1),box-shadow .2s,opacity .2s}
.w .btn.sm{height:40px;padding:0 16px;font-size:14px;gap:8px;border-radius:20px}
.w .btn:hover,.w .btn.hover{transform:translateY(-1px);box-shadow:0 8px 20px var(--shadow)}
.w .btn:active,.w .btn.press{transform:scale(.97);box-shadow:none}
.w .btn:focus-visible,.w .btn.focus,.w .link:focus-visible{outline:2px solid var(--blue);outline-offset:3px}
.w .link{color:var(--blueText);font-weight:500;border-radius:6px}
.w .link:hover{text-decoration:underline;text-underline-offset:3px}
.w .eyebrow{display:inline-flex;align-items:center;gap:8px;height:32px;padding:0 14px;border-radius:16px;background:var(--sunk);font-size:13px;font-weight:500;color:var(--ink2)}
.w .eyebrow i{width:8px;height:8px;border-radius:50%;background:var(--green);display:block}
.w .h1{font-weight:600;letter-spacing:-.045em}
.w .h2{font-weight:600;letter-spacing:-.035em}
.w .dev{position:relative;border-radius:46px;background:var(--bezel);padding:7px;box-shadow:0 30px 60px -20px var(--shadow),0 0 0 1px color-mix(in srgb,var(--ink) 10%,transparent);flex:none}
.w .dev img{display:block;width:100%;height:auto;border-radius:39px}
.w .paper{position:absolute;background:var(--paper);border-radius:18px;box-shadow:0 24px 50px -18px var(--shadow);display:flex;flex-direction:column;gap:14px;padding:26px 22px}
.w .paper .b{height:9px;border-radius:5px;background:#CBD3E0}
.w .card{position:relative;background:var(--sunk);border-radius:28px;overflow:hidden;border:1px solid var(--line)}
.w .card .ct{font-weight:600;letter-spacing:-.025em}
.w .foot{border-top:1px solid var(--line)}
@keyframes settle{from{opacity:0;transform:rotate(-17deg) translateY(28px)}to{opacity:1;transform:rotate(-9deg) translateY(0)}}
@keyframes rise{from{opacity:0;transform:translateY(22px)}to{opacity:1;transform:none}}
.w .a-settle{animation:settle .9s cubic-bezier(.22,1,.36,1) both}
.w .a-rise{animation:rise .7s cubic-bezier(.22,1,.36,1) both}
@supports (animation-timeline:view()){.w .a-scroll{animation:rise linear both;animation-timeline:view();animation-range:entry 0% entry 45%}}
@media (prefers-reduced-motion:reduce){.w .a-settle,.w .a-rise,.w .a-scroll{animation:none!important;opacity:1!important}.w .a-settle{transform:rotate(-9deg)}.w .btn{transition:none}.w .btn:hover{transform:none}}
'''

def brand(preview,name='Shop',size=21,mk=32):
    if preview:
        return f'<span class="brand" style="font-size:{size}px"><span class="mk" style="width:{mk}px;height:{mk}px">{BADGE}</span>{name}</span>'
    return f'<dc-import name="Brand" product-name="{name}" size="{size}" hint-size="{int(len(name)*size*.62+mk+14)}px,{mk}px"></dc-import>'

def nav(m,pre):
    pad='0 20px' if m else '0 80px'
    return f'<header class="nav" style="padding:{pad}">{brand(pre)}<a class="btn sm" href="#">{GH.replace("20","18")}GitHub{"" if m else " · [n]"}</a></header>'

def dev(shot,w,extra=''):
    return f'<div class="dev" style="width:{w}px;{extra}"><img src="{{{{shots.{shot}}}}}" alt="" width="600" height="1304"></div>'

def hero(m):
    h=lambda: ''
    left=f'''<div style="display:flex;flex-direction:column;gap:{20 if m else 28}px;align-items:flex-start;flex:1;min-width:0"><span class="eyebrow a-rise"><i></i>Open source · self-hosted · iPhone</span><h1 class="h1 a-rise" style="font-size:{44 if m else 76}px;line-height:{1.03 if m else 1.01};animation-delay:.05s">Photograph the receipt. Keep the data.</h1><p class="a-rise" style="font-size:{17 if m else 20}px;line-height:1.5;color:var(--ink2);max-width:500px;animation-delay:.1s">Reads your supermarket receipts on your own server, checks every line adds up to the printed total, and shows what your groceries really cost.</p><a class="btn a-rise" href="#" style="animation-delay:.15s;{"width:100%;justify-content:center" if m else ""}">{GH}View on GitHub</a></div>'''
    pw=250 if m else 300
    right=f'''<div style="position:relative;flex:none;width:{ "100%" if m else "520px"};height:{470 if m else 660}px;display:flex;justify-content:center"><div class="paper a-settle" style="width:{190 if m else 250}px;height:{300 if m else 390}px;left:{ "calc(50% - 160px)" if m else "20px"};top:{90 if m else 110}px;transform:rotate(-9deg);animation-delay:.1s"><div class="b" style="width:50%;background:var(--ink)"></div><div class="b"></div><div class="b" style="width:72%"></div><div class="b"></div><div style="height:46px;border-radius:12px;background:var(--yellow);margin-top:8px"></div><div style="margin-top:auto;display:flex;gap:3px;height:40px">{"".join(f'<i style="width:{w}px;background:var(--ink);display:block"></i>' for w in [3,7,3,5,3,9,3,5,3,7,3,5,3,9,3])}</div></div><div class="a-rise" style="position:absolute;left:{ "calc(50% - 60px)" if m else "190px"};top:{10 if m else 0}px;animation-delay:.2s">{dev("receipt",pw)}</div></div>'''
    return f'<section style="padding:{"36px 20px 24px" if m else "88px 80px 72px"}"><div style="display:flex;flex-direction:{"column" if m else "row"};gap:{28 if m else 40}px;align-items:{"stretch" if m else "center"};justify-content:space-between">{left}{right}</div></section>'

FEAT=[('Checks every receipt','The lines have to add up to the printed total. If they do not, you see exactly which row to fix.','receipt'),
      ('Finds the cheapest shop','Prices per kilo, litre or piece, per shop, so the comparison is fair.','product'),
      ('Shows where the money goes','Spend by month, shop and category, without a spreadsheet.','analytics'),
      ('Keeps products tidy','Names cleaned up, categories filled in, barcodes linked.','products')]
def features(m):
    cards=''
    for i,(t,d,s) in enumerate(FEAT):
        if m:
            cards+=f'<article class="card a-scroll" style="padding:28px 24px 0"><h3 class="ct" style="font-size:24px;line-height:1.2">{t}</h3><p style="font-size:16px;color:var(--ink2);margin-top:8px">{d}</p><div style="height:330px;overflow:hidden;margin-top:24px;display:flex;justify-content:center"><div style="margin-top:0">{dev(s,230)}</div></div></article>'
        else:
            cards+=f'<article class="card a-scroll" style="height:520px;padding:40px 40px 0"><h3 class="ct" style="font-size:30px;line-height:1.15;max-width:300px">{t}</h3><p style="font-size:17px;line-height:1.5;color:var(--ink2);margin-top:12px;max-width:290px">{d}</p><div style="position:absolute;right:36px;top:{150 if i%2==0 else 170}px">{dev(s,250)}</div></article>'
    return f'<section style="padding:{"40px 20px 56px" if m else "72px 80px 96px"}"><h2 class="h2" style="font-size:{30 if m else 44}px;line-height:1.1;margin-bottom:{24 if m else 40}px">What it does</h2><div style="display:grid;grid-template-columns:repeat({1 if m else 2},minmax(0,1fr));gap:{16 if m else 24}px">{cards}</div></section>'

def footer(m,pre):
    return f'''<footer class="foot" style="padding:{"36px 20px 40px" if m else "56px 80px 64px"}"><div style="display:flex;flex-direction:{"column" if m else "row"};justify-content:space-between;gap:{24 if m else 40}px;align-items:{"flex-start" if m else "center"}">{brand(pre)}<a class="link" href="#" style="display:inline-flex;align-items:center;gap:8px;font-size:16px">{GH.replace("20","18")}View the source and README on GitHub →</a></div><p style="margin-top:{28 if m else 36}px;font-size:13.5px;line-height:1.6;color:var(--ink2);max-width:640px">MIT licence. Product data from Open Food Facts (ODbL). Full credits and licences in the repository. Not affiliated with any supermarket.</p></footer>'''

def body(m,pre): return nav(m,pre)+hero(m)+features(m)+footer(m,pre)

def doc(name,title,inner,w,h,theme,preview=False,fixed=True):
    t=TOK[theme]
    rootvars=';'.join(f'--{k}:{t[k]}' if preview else f'--{k}:{{{{t.{k}}}}}' for k in VARS)
    shots={s:A['shots'][f'{s}-{theme}'] for s in SHOTS}
    if preview:
        inner=re.sub(r'\{\{shots\.(\w+)\}\}',lambda mm:f'../../screens/web/{mm.group(1)}-{theme}.jpg',inner)
        return f'<!doctype html><html lang="en"><head><meta charset="utf-8"><title>{title}</title><meta name="viewport" content="width=device-width,initial-scale=1"><style>{FONTFACE_PREVIEW}{CSS}</style></head><body><div class="w" style="{rootvars};width:{w}px">{inner}</div></body></html>'
    sj=json.dumps(shots).replace("'","&#39;")
    tj=json.dumps(t)
    return f'''<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{title}</title>
<script src="./support.js"></script>
</head>
<body>
<x-dc>
<helmet>
<style>{FONTFACE}{CSS}</style>
</helmet>
<div class="w" style="{rootvars};width:{w}px">
{inner}
</div>
</x-dc>
<script type="text/x-dc" data-dc-script data-props='{{"theme":{{"editor":"enum","options":["light","dark"],"default":"{theme}"}},"$preview":{{"width":{w},"height":{h}}}}}'>
class Component extends DCLogic {{
  renderVals() {{
    var T = {tj};
    var L = T;
    var D = {json.dumps(TOK['dark'])};
    var S = {{ light: {json.dumps({s:A['shots'][s+'-light'] for s in SHOTS})}, dark: {json.dumps({s:A['shots'][s+'-dark'] for s in SHOTS})} }};
    var dark = this.props.theme === 'dark';
    return {{ t: dark ? D : L, shots: dark ? S.dark : S.light }};
  }}
}}
</script>
</body>
</html>
'''

def write(fn,html,preview=False):
    open(f'{OUT}/{"preview" if preview else "project"}/{fn}','w').write(html)

def dark_wrapper(fn,src,title,w,h):
    write(fn,f'''<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{title}</title>
<script src="./support.js"></script>
</head>
<body>
<x-dc>
<helmet>
<style>body{{margin:0}}</style>
</helmet>
<dc-import name="{src}" theme="dark" hint-size="{w}px,{h}px"></dc-import>
</x-dc>
<script type="text/x-dc" data-dc-script data-props='{{"$preview":{{"width":{w},"height":{h}}}}}'>
class Component extends DCLogic {{
  renderVals() {{ return {{}}; }}
}}
</script>
</body>
</html>
''')

# Brand child component (the one place the product name lives)
def brand_component():
    write('Brand.dc.html',f'''<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Brand lockup</title>
<script src="./support.js"></script>
</head>
<body>
<x-dc>
<helmet>
<style>body{{margin:0}}</style>
</helmet>
<span style="display:inline-flex;align-items:center;gap:10px;font-family:'Inter',system-ui,sans-serif;font-weight:600;letter-spacing:-.03em;font-size:{{{{px}}}}px;line-height:1;color:inherit"><span style="width:32px;height:32px;display:block;flex:none">{BADGE}</span>{{{{name}}}}</span>
</x-dc>
<script type="text/x-dc" data-dc-script data-props='{{"productName":{{"editor":"text","default":"Shop"}},"size":{{"editor":"int","default":21}},"$preview":{{"width":120,"height":32}}}}'>
class Component extends DCLogic {{
  renderVals() {{
    return {{ name: this.props.productName || 'Shop', px: this.props.size || 21 }};
  }}
}}
</script>
</body>
</html>
''')

if __name__=='__main__':
    brand_component()
    for theme in ['light','dark']:
        for m,tag,w,h in [(False,'Desktop',1280,2420),(True,'Mobile',390,2900)]:
            # preview (flat)
            write(f'page-{tag.lower()}-{theme}.html',doc('x','Website page',body(m,True),w,h,theme,preview=True),preview=True)
    doc_d=doc('x','Website page, desktop',body(False,False),1280,2420,'light')
    write('HF-Page-Desktop.dc.html',doc_d)
    write('HF-Page-Mobile.dc.html',doc('x','Website page, mobile',body(True,False),390,2900,'light'))
    dark_wrapper('D-HF-Page-Desktop.dc.html','HF-Page-Desktop','Website page, desktop, dark',1280,2420)
    dark_wrapper('D-HF-Page-Mobile.dc.html','HF-Page-Mobile','Website page, mobile, dark',390,2900)
    print(sorted(os.listdir(f'{OUT}/project')),sorted(os.listdir(f'{OUT}/preview')))
