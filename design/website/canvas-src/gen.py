#!/usr/bin/env python3
"""Generates the website design boards for the canvas (.dc.html) and flat HTML previews to check them in a browser.
   python3 gen.py <out-dir>   (writes <out>/project/*.dc.html and <out>/preview/*.html)"""
import json, os, re, sys
HERE=os.path.dirname(os.path.abspath(__file__))
A=json.load(open(f'{HERE}/assets.json'))
OUT=sys.argv[1] if len(sys.argv)>1 else 'out'
os.makedirs(f'{OUT}/project',exist_ok=True); os.makedirs(f'{OUT}/preview',exist_ok=True)

TOK={'light':dict(bg='#FFFFFF',sunk='#F3F4F8',line='#E4E6EE',ink='#2B2D3F',ink2='#686B7E',ink3='#B2B5C4',btn='#1D1D1B',btnInk='#FFFFFF',blue='#0A84FF',blueText='#0071E3',blueTint='#E5F1FF',panel='#DCEBFF',yellow='#FFCC1A',green='#18B663',greenText='#0B8A47',amber='#FF9500',amberText='#B25E00',paper='#F7F9FF',shadow='rgba(29,29,27,.16)',bezel='#1D1D1B'),
     'dark':dict(bg='#0C0C0B',sunk='#1A1A18',line='#2A2A27',ink='#F4F4F1',ink2='#9B9B95',ink3='#5C5C58',btn='#F4F4F1',btnInk='#0C0C0B',blue='#4DA3FF',blueText='#4DA3FF',blueTint='#0E2236',panel='#12304F',yellow='#FFCC1A',green='#3DDC84',greenText='#3DDC84',amber='#FFB340',amberText='#FFB340',paper='#E9ECF4',shadow='rgba(0,0,0,.55)',bezel='#2A2A27')}
VARS=['bg','sunk','line','ink','ink2','ink3','btn','btnInk','blue','blueText','blueTint','panel','yellow','green','greenText','amber','amberText','paper','shadow','bezel']
SHOTS=['home','receipt','analytics','products','product']

FONTFACE=''.join(f"@font-face{{font-family:'GT America';src:url(/_blob/{b}) format('woff2');font-weight:{w};font-display:swap}}" for b,w in [('80ac2bab8376b033b47e3bbbcce44096',400),('ab67710a758abf97af8b2b7aacbd5359',500),('f70bb7bbdd676014d8cf05f61d2c134f',700)])+"@font-face{font-family:'GT America Mono';src:url(/_blob/1c5f938b08f8e5864d7263e601c301f3) format('woff2');font-weight:400;font-display:swap}"
FONTFACE_PREVIEW=''.join(f"@font-face{{font-family:'GT America';src:url(../fonts/{f}.woff2) format('woff2');font-weight:{w}}}" for f,w in [('GTAmerica-Regular',400),('GTAmerica-Medium',500),('GTAmerica-Bold',700)])+"@font-face{font-family:'GT America Mono';src:url(../fonts/GTAmericaMono-Regular.woff2) format('woff2');font-weight:400}"

GH='<svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M12 .5C5.65.5.5 5.65.5 12c0 5.08 3.29 9.39 7.86 10.91.58.1.79-.25.79-.56v-2c-3.2.7-3.87-1.37-3.87-1.37-.52-1.33-1.28-1.69-1.28-1.69-1.04-.71.08-.7.08-.7 1.15.08 1.76 1.18 1.76 1.18 1.03 1.76 2.69 1.25 3.35.96.1-.75.4-1.25.73-1.54-2.55-.29-5.24-1.28-5.24-5.69 0-1.26.45-2.28 1.18-3.09-.12-.29-.51-1.46.11-3.05 0 0 .97-.31 3.17 1.18a11 11 0 0 1 5.77 0c2.2-1.49 3.17-1.18 3.17-1.18.63 1.59.23 2.76.11 3.05.74.81 1.18 1.83 1.18 3.09 0 4.42-2.69 5.39-5.25 5.67.41.36.78 1.06.78 2.14v3.17c0 .31.21.67.8.56A11.5 11.5 0 0 0 23.5 12C23.5 5.65 18.35.5 12 .5Z"/></svg>'
BADGE=open(f'{HERE}/../logo-badge.svg').read().replace('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" width="512" height="512" role="img" aria-label="Logo badge">','<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" style="width:100%;height:100%;display:block" aria-hidden="true">').replace('id="g"','id="bg1"').replace('url(#g)','url(#bg1)')

CSS='''
body{margin:0}
.w{font-family:'GT America',system-ui,-apple-system,sans-serif;background:var(--bg);color:var(--ink);-webkit-font-smoothing:antialiased;line-height:1.5;overflow:hidden}
.w *{box-sizing:border-box}
.w h1,.w h2,.w h3,.w p{margin:0}
.w a{color:inherit;text-decoration:none}
.w .mono{font-family:'GT America Mono',ui-monospace,monospace}
.w .brand{display:inline-flex;align-items:center;gap:10px;font-weight:500;letter-spacing:-.02em;font-size:21px;line-height:1}
.w .brand .mk{width:32px;height:32px;display:block;flex:none}
.w .nav{display:flex;align-items:center;justify-content:space-between;height:88px}
.w .nav .links{display:flex;align-items:center;gap:8px}
.w .nav .nl{height:40px;padding:0 14px;display:inline-flex;align-items:center;border-radius:20px;font-size:15px;font-weight:500;color:var(--ink2);transition:color .15s,background .15s}
.w .nav .nl:hover,.w .nav .nl.hover{color:var(--ink);background:var(--sunk)}
.w .nav .nl:focus-visible{outline:2px solid var(--blue);outline-offset:2px}
.w .btn{display:inline-flex;align-items:center;justify-content:center;gap:8px;height:56px;padding:0 28px;border-radius:28px;font-weight:500;font-size:16.5px;background:var(--btn);color:var(--btnInk);transition:transform .16s cubic-bezier(.22,1,.36,1),opacity .2s}
.w .btn.sm{height:36px;padding:0 18px;font-size:14px;border-radius:18px}
.w .btn:hover,.w .btn.hover{opacity:.9}
.w .btn:active,.w .btn.press{transform:scale(.97)}
.w .btn:focus-visible,.w .btn.focus,.w .link:focus-visible{outline:2px solid var(--blue);outline-offset:3px}
.w .link{color:var(--blueText);font-weight:500;border-radius:6px}
.w .link:hover{text-decoration:underline;text-underline-offset:3px}
.w .eyebrow{display:inline-flex;align-items:center;gap:8px;height:32px;padding:0 14px;border-radius:16px;background:var(--sunk);font-size:13px;font-weight:500;color:var(--ink2)}
.w .eyebrow i{width:8px;height:8px;border-radius:50%;background:var(--green);display:block}
.w .h1{font-weight:500;letter-spacing:-.04em}
.w .h2{font-weight:500;letter-spacing:-.03em}
.w .dev{position:relative;border-radius:46px;background:var(--bezel);padding:7px;box-shadow:0 30px 60px -20px var(--shadow),0 0 0 1px color-mix(in srgb,var(--ink) 10%,transparent);flex:none}
.w .dev img{display:block;width:100%;height:auto;border-radius:39px}
.w .paper{position:absolute;background:var(--paper);border-radius:18px;box-shadow:0 24px 50px -18px var(--shadow);display:flex;flex-direction:column;gap:14px;padding:26px 22px}
.w .paper .b{height:9px;border-radius:5px;background:#CBD3E0}
.w .card{position:relative;background:var(--sunk);border-radius:28px;overflow:hidden;border:1px solid var(--line)}
.w .card .ct{font-weight:500;letter-spacing:-.02em}
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
    links='' if m else '<a class="nl" href="#features">Features</a><a class="nl" href="#credits">Credits</a>'
    return f'<header class="nav" style="padding:{pad};{"height:68px" if m else ""}">{brand(pre)}<nav class="links">{links}<a class="btn sm" href="https://github.com/sunday-studio/shop">{GH.replace("20","18")}GitHub</a></nav></header>'

def dev(shot,w,extra=''):
    return f'<div class="dev" style="width:{w}px;{extra}"><img src="{{{{shots.{shot}}}}}" alt="" width="600" height="1304"></div>'

def hero(m):
    h=lambda: ''
    left=f'''<div style="display:flex;flex-direction:column;gap:{20 if m else 28}px;align-items:flex-start;flex:1;min-width:0"><span class="eyebrow a-rise"><i></i>Open source · self-hosted · iPhone</span><h1 class="h1 a-rise" style="font-size:{44 if m else 72}px;line-height:{1.03 if m else 1.02};animation-delay:.05s">Photograph the receipt. Keep the data.</h1><p class="a-rise" style="font-size:{17 if m else 20}px;line-height:1.5;color:var(--ink2);max-width:500px;animation-delay:.1s">Reads your supermarket receipts on your own server, checks every line adds up to the printed total, and shows what your groceries really cost.</p><a class="btn a-rise" href="https://github.com/sunday-studio/shop" style="animation-delay:.15s;{"width:100%;justify-content:center" if m else ""}">{GH}View on GitHub</a></div>'''
    pw=250 if m else 330
    panel_h=520 if m else 640
    right=f'''<div class="a-rise" style="position:relative;flex:none;width:{ "100%" if m else "560px"};height:{panel_h}px;border-radius:{32 if m else 40}px;background:var(--panel);overflow:hidden;animation-delay:.1s"><div class="paper a-settle" style="width:{170 if m else 230}px;height:{270 if m else 350}px;left:{ "-30px" if m else "34px"};top:{70 if m else 90}px;transform:rotate(-9deg);animation-delay:.2s"><div class="b" style="width:50%;background:var(--ink)"></div><div class="b"></div><div class="b" style="width:72%"></div><div class="b"></div><div style="height:40px;border-radius:12px;background:var(--yellow);margin-top:8px"></div><div style="margin-top:auto;display:flex;gap:3px;height:36px">{"".join(f'<i style="width:{w}px;background:var(--ink);display:block"></i>' for w in [3,7,3,5,3,9,3,5,3,7,3,5,3,9,3])}</div></div><div style="position:absolute;left:50%;margin-left:{ "-90px" if m else "-100px"};top:{54 if m else 70}px">{dev("receipt",pw)}</div></div>'''
    return f'<section style="padding:{"24px 20px 24px" if m else "24px 80px 72px"}"><div style="display:flex;flex-direction:{"column" if m else "row"};gap:{28 if m else 40}px;align-items:{"stretch" if m else "center"};justify-content:space-between">{left}{right}</div></section>'

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
            span=[7,5,5,7][i]; wide=span==7
            ph=f'<div style="position:absolute;right:{40 if wide else 0}px;{"left:auto" if wide else "left:50%;margin-left:-118px;right:auto"};top:{130 if wide else 250}px">{dev(s,236)}</div>'
            cards+=f'<article class="card a-scroll" style="grid-column:span {span};height:560px;padding:40px 40px 0"><h3 class="ct" style="font-size:30px;line-height:1.12;max-width:{260 if wide else 380}px">{t}</h3><p style="font-size:17px;line-height:1.5;color:var(--ink2);margin-top:12px;max-width:{250 if wide else 380}px">{d}</p>{ph}</article>'
    return f'<section id="features" style="padding:{"40px 20px 56px" if m else "72px 80px 96px"}"><h2 class="h2" style="font-size:{30 if m else 44}px;line-height:1.1;margin-bottom:{24 if m else 40}px">What it does</h2><div style="display:grid;grid-template-columns:repeat({1 if m else 12},minmax(0,1fr));gap:{16 if m else 24}px">{cards}</div></section>'

def footer(m,pre):
    return f'''<footer class="foot" id="credits" style="padding:{"36px 20px 40px" if m else "56px 80px 64px"}"><div style="display:flex;flex-direction:{"column" if m else "row"};justify-content:space-between;gap:{24 if m else 40}px;align-items:{"flex-start" if m else "center"}">{brand(pre)}<a class="link" href="https://github.com/sunday-studio/shop" style="display:inline-flex;align-items:center;gap:8px;font-size:16px">{GH.replace("20","18")}View the source and README on GitHub →</a></div><p style="margin-top:{28 if m else 36}px;font-size:13.5px;line-height:1.6;color:var(--ink2);max-width:640px">MIT licence. Product data from Open Food Facts (ODbL). Full credits and licences in the repository. Not affiliated with any supermarket.</p><p style="margin-top:14px;font-size:13.5px;line-height:1.6;color:var(--ink2)">A product from <a class="link" href="https://www.thesunday.studio/">Sunday Studio</a>.</p></footer>'''

def body(m,pre): return nav(m,pre)+hero(m)+features(m)+footer(m,pre)

def doc(name,title,inner,w,h,theme,preview=False,fixed=True):
    t=TOK[theme]
    rootvars=';'.join(f'--{k}:{t[k]}' if preview else f'--{k}:{{{{t.{k}}}}}' for k in VARS)
    shots={s:A['shots'][f'{s}-{theme}'] for s in SHOTS}
    if preview:
        inner=re.sub(r'\{\{shots\.(\w+)\}\}',lambda mm:f'../screens/web/{mm.group(1)}-{theme}.jpg',inner)
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
<span style="display:inline-flex;align-items:center;gap:10px;font-family:'GT America',system-ui,sans-serif;font-weight:500;letter-spacing:-.03em;font-size:{{{{px}}}}px;line-height:1;color:inherit"><span style="width:32px;height:32px;display:block;flex:none">{BADGE}</span>{{{{name}}}}</span>
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

def lum(h):
    h=h.lstrip('#'); r,g,b=[int(h[i:i+2],16)/255 for i in (0,2,4)]
    f=lambda c:c/12.92 if c<=.03928 else ((c+.055)/1.055)**2.4
    return .2126*f(r)+.7152*f(g)+.0722*f(b)
def cr(a,b):
    la,lb=lum(a),lum(b); hi,lo=max(la,lb),min(la,lb); return (hi+.05)/(lo+.05)
def contrast_rows():
    rows=''
    pairs=[('Body text','ink','bg'),('Secondary text','ink2','bg'),('Secondary on card','ink2','sunk'),('Link / blue text','blueText','bg'),('Button label','btnInk','btn'),('Green text','greenText','bg'),('Amber text','amberText','bg'),('Placeholder, decor only','ink3','bg')]
    for n,f,b in pairs:
        cells=''
        for th in ['light','dark']:
            v=cr(TOK[th][f],TOK[th][b]); ok='AAA' if v>=7 else ('AA' if v>=4.5 else ('AA large' if v>=3 else 'fails: decor only'))
            cells+=f'<td style="padding:10px 0;color:var(--ink2)">{v:.1f}:1 · {ok}</td>'
        rows+=f'<tr style="border-bottom:1px solid var(--line)"><td style="padding:10px 0">{n}</td>{cells}</tr>'
    return rows
def notes_board():
    sw=lambda k:f'<div style="display:flex;flex-direction:column;gap:6px"><div style="width:76px;height:52px;border-radius:12px;background:var(--{k});border:1px solid var(--line)"></div><span class="mono" style="font-size:11.5px;color:var(--ink2)">{k}<br>{TOK["light"][k]}</span></div>'
    scale=[('Display','76 / 77','500 · -0.04em','Photograph the receipt'),('Display mobile','44 / 45','500 · -0.04em','Keep the data'),('H2','44 / 48','500 · -0.03em','What it does'),('H3 card','28 / 32','500 · -0.02em','Checks every receipt'),('Lead','20 / 30','400','Reads your supermarket receipts'),('Body','16-17 / 24','400','Prices per kilo, litre or piece'),('Small','13.5 / 22','400','MIT licence. Product data from…'),('Mono','13','Geist Mono 400','€38,13 · 1,79')]
    sc=''.join(f'<div style="display:grid;grid-template-columns:150px 110px 170px 1fr;gap:16px;padding:12px 0;border-bottom:1px solid var(--line);align-items:baseline;font-size:14px"><b style="font-weight:500">{a}</b><span class="mono" style="font-size:12px;color:var(--ink2)">{b}</span><span style="color:var(--ink2)">{c}</span><span style="font-size:{min(int(float(b.split(" / ")[0].split("-")[0])),30)}px;font-weight:{600 if "600" in c else 400};letter-spacing:-.02em;white-space:nowrap;overflow:hidden">{d}</span></div>' for a,b,c,d in scale)
    sp=''.join(f'<div style="display:flex;flex-direction:column;gap:6px;align-items:flex-start"><div style="width:{n}px;height:24px;background:var(--blue);border-radius:4px"></div><span class="mono" style="font-size:11.5px;color:var(--ink2)">{n}</span></div>' for n in [4,8,12,16,24,32,48,64,96])
    comps=[('Nav','Plain header, 88 px (68 mobile), no bar, border or blur, scrolls away. Logo left; grey text links Features and Credits, then a dark GitHub pill. Mobile: logo and pill only.'),('Button','52 px pill (40 px in nav). States: rest, hover (lift 1 px + shadow), press (scale .97), focus (2 px blue ring, 3 px offset).'),('Eyebrow pill','32 px, sunk fill, green dot.'),('Device frame','Black bezel 7 px, radius 46, island comes from the capture. Light and dark capture swap with the theme.'),('Tilted receipt','Paper card rotated -9°, yellow total, barcode. Settles in 0.9 s on load.'),('Feature card','Radius 28, sunk fill, 1 px line. Title 28, line 17, phone cropped at the bottom.'),('Footer','Logo, one GitHub link, one credits line linking to the repo.')]
    cp=''.join(f'<div class="card" style="padding:18px 20px;border-radius:18px"><b style="font-weight:600">{a}</b><p style="font-size:14px;color:var(--ink2);margin-top:4px">{b}</p></div>' for a,b in comps)
    build=['One static HTML file and one CSS file (about 6 kB of CSS here). No framework, no JavaScript needed; the page animates with CSS only.','Fonts self-hosted: GT America 400/500/700 and GT America Mono 400 as woff2. The files in the repo are a TRIAL licence: buy the web licence from Grilli Type before the site goes public. No Google CDN.','Screenshots: five captures in light and dark, exported as AVIF/WebP at 600 and 1200 px; use <picture> with prefers-color-scheme so the right pair loads.','Theme: follows the system (prefers-color-scheme), CSS variables from the token table. Optional footer toggle is not needed for this page.','Logo: logo-badge.svg inline, name in one variable. design/website/make-wordmark.py outlines the wordmark for the favicon, social image and any place text cannot be live.','No analytics, cookies or third-party requests. GitHub star count is optional and would be fetched at build time, not in the browser.','Social card (1200×630) and favicon set made from the badge. Deploy: GitHub Pages or Cloudflare Pages from a site/ folder; domain after the name decision.']
    bl=''.join(f'<li style="margin:6px 0">{b}</li>' for b in build)
    return f'''<section style="padding:56px 80px;border-bottom:1px solid var(--line)"><div style="font-size:13px;color:var(--blue);font-weight:500;margin-bottom:8px">Design notes · website</div><h1 class="h1" style="font-size:52px;line-height:1.05;max-width:900px">A short page: one promise, four features, one link.</h1><p style="color:var(--ink2);font-size:18px;max-width:760px;margin-top:14px">Hero with the GitHub button and one strong app screen; four feature cards with one real screen each; footer with the GitHub link, licence and one credits line. Everything else (pipeline, architecture, MCP, quick start, roadmap, FAQ, limits, full credits) moves to the repository README; the earlier drawings are kept on the Cut boards.</p></section>
<section style="padding:48px 80px;border-bottom:1px solid var(--line)"><h2 class="h2" style="font-size:26px;margin-bottom:20px">Colour tokens (from the app)</h2><div style="display:flex;gap:14px;flex-wrap:wrap">{"".join(sw(k) for k in ["bg","sunk","line","ink","ink2","btn","blue","blueText","green","amber","yellow"])}</div><p style="font-size:14px;color:var(--ink2);margin-top:16px;max-width:760px">Dark values are in the board's theme switch. Green and amber appear only inside the app screens (adds up / needs review), never as page decoration. Yellow is the logo's total bar and the paper in the hero.</p><table style="width:100%;max-width:820px;margin-top:24px;font-size:14px;border-collapse:collapse"><tr style="text-align:left;color:var(--ink2)"><th style="padding:8px 0;font-weight:500">Contrast (WCAG)</th><th style="font-weight:500">Light</th><th style="font-weight:500">Dark</th></tr>{contrast_rows()}</table></section>
<section style="padding:48px 80px;border-bottom:1px solid var(--line)"><h2 class="h2" style="font-size:26px;margin-bottom:8px">Type scale</h2><p style="font-size:14px;color:var(--ink2);margin-bottom:12px">GT America (Regular, Medium, Bold) with GT America Mono for code and figures. Headings use Medium, matching the app. The font files are a trial licence: fine for this design, but a web licence is needed before the site is published. GT America has no tabular-figure feature, so figures that must align use the Mono digits.</p>{sc}</section>
<section style="padding:48px 80px;border-bottom:1px solid var(--line)"><div style="display:flex;gap:64px"><div style="flex:1"><h2 class="h2" style="font-size:26px;margin-bottom:16px">Spacing scale</h2><div style="display:flex;gap:18px;align-items:flex-end;flex-wrap:wrap">{sp}</div></div><div style="flex:1"><h2 class="h2" style="font-size:26px;margin-bottom:16px">Grid</h2><p style="font-size:15px;color:var(--ink2)">Desktop 1280: 80 px margins, 1120 px content, two feature columns with a 24 px gap. Mobile 390: 20 px gutters, one column, 16 px gaps. Radii 16 (pill chips), 28 (cards), 46 (device), 26/20 (buttons).</p></div></div></section>
<section style="padding:48px 80px;border-bottom:1px solid var(--line)"><h2 class="h2" style="font-size:26px;margin-bottom:16px">Components</h2><div style="display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px">{cp}</div></section>
<section style="padding:48px 80px"><h2 class="h2" style="font-size:26px;margin-bottom:8px">What the build needs (no code here)</h2><ul style="margin:0;padding-left:20px;font-size:15.5px;color:var(--ink2);max-width:860px">{bl}</ul></section>'''
def details_board():
    btn=lambda c,l:f'<div style="display:flex;flex-direction:column;gap:10px;align-items:flex-start"><a class="btn {c}" href="https://github.com/sunday-studio/shop">{GH}View on GitHub</a><span class="mono" style="font-size:12px;color:var(--ink2)">{l}</span></div>'
    states=''.join(btn(c,l) for c,l in [('','rest'),('hover','hover'),('press','pressed'),('focus','keyboard focus')])
    badge=lambda w:f'<div style="display:flex;flex-direction:column;gap:8px;align-items:flex-start"><div style="width:{w}px;height:{w}px">{BADGE}</div><span class="mono" style="font-size:11.5px;color:var(--ink2)">{w} px</span></div>'
    badges=''.join(badge(w) for w in [128,64,32,16])
    motion=[('0 ms','Eyebrow, headline, line and button rise 22 px and fade in, 60 ms apart (700 ms, ease-out)'),('100 ms','Tilted receipt settles from -17° to -9° (900 ms, ease-out)'),('200 ms','Phone rises (700 ms)'),('on scroll','Feature cards rise as they enter the viewport (CSS scroll timeline where supported, otherwise visible at once)'),('hover','Button lifts 1 px with a soft shadow, 160 ms')]
    mo=''.join(f'<div style="display:flex;gap:20px;padding:12px 0;border-bottom:1px solid var(--line);font-size:15px"><span class="mono" style="width:90px;color:var(--blue);font-size:13px">{a}</span><span style="color:var(--ink2)">{b}</span></div>' for a,b in motion)
    return f'''<section style="padding:56px 80px;border-bottom:1px solid var(--line)"><div style="font-size:13px;color:var(--blue);font-weight:500;margin-bottom:8px">Details · states, logo, motion</div><h2 class="h2" style="font-size:36px;margin-bottom:24px">Button states</h2><div style="display:flex;gap:40px;flex-wrap:wrap">{states}</div><p style="font-size:14px;color:var(--ink2);margin-top:20px;max-width:700px">Links use blue text with an underline on hover and the same focus ring. No copy button, mobile menu or quick start on this page: the nav is a logo, two text links and one pill on desktop, a logo and one pill on mobile.</p></section>
<section style="padding:48px 80px;border-bottom:1px solid var(--line)"><h2 class="h2" style="font-size:36px;margin-bottom:24px">Logo</h2><div style="display:flex;gap:56px;align-items:flex-end;flex-wrap:wrap">{badges}<div style="display:flex;flex-direction:column;gap:8px">{brand(False)}<span class="mono" style="font-size:11.5px;color:var(--ink2)">lockup, live text</span></div></div><p style="font-size:14px;color:var(--ink2);margin-top:20px;max-width:760px">Badge is a vector (design/website/logo-badge.svg): blue squircle, tilted receipt, green dot, yellow total, barcode. The product name is one value: on the canvas it is the "Product name" tweak of the Brand component; in the files it is the argument of make-wordmark.py, which outlines the name for the favicon, social card and any place live text cannot be used. "Shop" is a placeholder until the name is chosen.</p></section>
<section style="padding:48px 80px"><h2 class="h2" style="font-size:36px;margin-bottom:8px">Motion</h2><p style="font-size:14px;color:var(--ink2);margin-bottom:16px">Cheap and short: CSS transforms and opacity only, nothing runs after load except scroll-linked fades. With prefers-reduced-motion everything is visible at once, the receipt is simply rotated, and hover does not move.</p>{mo}</section>'''

if __name__=='__main__':
    open(f'{OUT}/project/notes.tmp','w').write('')
    write('WD-DesignNotes.dc.html',doc('x','Website design notes',notes_board(),1280,2500,'light'))
    write('WD-Details.dc.html',doc('x','Website states, logo, motion',details_board(),1280,1700,'light'))
    write('notes-light.html',doc('x','notes',notes_board(),1280,2500,'light',preview=True),preview=True)
    write('details-light.html',doc('x','details',details_board(),1280,1700,'light',preview=True),preview=True)
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
    import docs_boards, web_b; docs_boards.build(globals()); web_b.build(globals())
    print(sorted(os.listdir(f'{OUT}/project')),sorted(os.listdir(f'{OUT}/preview')))
