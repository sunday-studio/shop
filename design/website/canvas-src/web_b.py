"""Website direction B ("story"), second pass after comments.
Full-bleed painted-sky hero (Duna), three visual step cards, a dark feature showcase whose left list cycles
through the four features while the phone on the right changes (CSS only), a closing sky panel, footer.
Called from gen.py's main block: web_b.build(globals())."""

CSS = '''
.w .hb h1{font-size:88px;line-height:1;font-weight:400;letter-spacing:-.05em;text-align:center}
.w .hb .lead{font-size:21px;line-height:1.5;color:var(--ink2);text-align:center;max-width:620px;margin:22px auto 0}
.w .h2.lt{font-weight:400;letter-spacing:-.045em}
.w .btn.alt{background:rgba(255,255,255,.55);color:#1D1D1B;backdrop-filter:blur(8px)}
.w .mcta{font-family:'GT America Mono',ui-monospace,monospace;text-transform:uppercase;letter-spacing:.07em;font-size:12.5px;height:44px;padding:0 20px;border-radius:22px;gap:10px}
.w .burger{width:44px;height:44px;border-radius:50%;display:inline-flex;flex-direction:column;align-items:center;justify-content:center;gap:6px;background:rgba(255,255,255,.55);flex:none}
.w .burger i{display:block;width:18px;height:2px;border-radius:1px;background:#1D1D1B}
.w .badge{display:inline-flex;align-items:center;gap:8px;height:34px;padding:0 14px 0 16px;border-radius:17px;background:rgba(20,20,19,.82);color:#F4F4F1;font-size:13.5px;font-weight:500;backdrop-filter:blur(10px)}
.w .art{position:absolute;inset:0;width:100%;height:100%;display:block}
.w .heroP{position:relative;overflow:hidden;background:var(--sk1)}
.w .heroP .nav{position:relative;z-index:3}
.w .heroP .brand{color:#1D1D1B}
.w .fl{position:absolute;background:var(--bg);border-radius:20px;box-shadow:0 18px 40px -16px var(--shadow);padding:14px 18px;display:flex;align-items:center;gap:12px;font-size:15px;font-weight:500;white-space:nowrap;z-index:2}
.w .fl i{width:28px;height:28px;border-radius:50%;display:flex;align-items:center;justify-content:center;flex:none;font-style:normal;color:#fff;font-size:15px}
.w .fl small{display:block;font-size:12.5px;font-weight:400;color:var(--ink2)}
.w .tilt{transform:perspective(1800px) rotateY(-9deg) rotateX(3deg) rotate(1.5deg);transform-origin:50% 100%}
.w .sx{border-radius:34px;background:var(--sunk);border:1px solid var(--line);overflow:hidden;position:relative;display:flex;flex-direction:column}
.w .sx .tx{padding:30px 30px 0}
.w .sx .num{width:34px;height:34px;border-radius:50%;background:var(--ink);color:var(--bg);font-size:15px;font-weight:500;display:flex;align-items:center;justify-content:center;margin-bottom:18px}
.w .sx h3{font-size:28px;line-height:1.12;font-weight:500;letter-spacing:-.025em;margin:0 0 8px}
.w .sx p{font-size:16.5px;line-height:1.5;color:var(--ink2);margin:0;max-width:300px}
.w .sx .vis{margin-top:auto;height:230px;position:relative}
.w .vf{position:absolute;left:50%;bottom:-40px;width:230px;height:250px;margin-left:-115px;background:var(--bg);border-radius:20px;box-shadow:0 24px 50px -20px var(--shadow);padding:26px 22px;display:flex;flex-direction:column;gap:12px;transform:rotate(-4deg)}
.w .vf .b{height:9px;border-radius:5px;background:var(--line)}
.w .cb{position:absolute;width:26px;height:26px;border:3px solid var(--blue);z-index:2}
.w .vl{position:absolute;left:30px;right:30px;bottom:-30px;background:var(--bg);border-radius:20px 20px 0 0;box-shadow:0 24px 50px -20px var(--shadow);padding:20px 22px 40px;font-size:15px}
.w .vl .r{display:flex;justify-content:space-between;padding:7px 0;color:var(--ink2);border-bottom:1px solid var(--line)}
.w .vl .r b{font-weight:500;color:var(--ink)}
.w .vl .tot{display:flex;justify-content:space-between;align-items:center;padding-top:12px;font-weight:500;color:var(--ink)}
.w .okp{display:inline-flex;align-items:center;gap:6px;height:26px;padding:0 10px;border-radius:13px;background:var(--greenTint);color:var(--greenText);font-size:12.5px;font-weight:500}
.w .vb{position:absolute;left:30px;right:30px;bottom:0;display:flex;gap:12px;align-items:flex-end;height:200px}
.w .vb i{flex:1;border-radius:10px 10px 0 0;overflow:hidden;display:flex;flex-direction:column;justify-content:flex-end}
.w .vb i s{display:block;width:100%}
.w .band{background:var(--band);color:#F4F4F1;border-radius:44px;overflow:hidden;position:relative}
.w .fx{display:flex;flex-direction:column}
.w .fi{padding:22px 0 20px;border-top:1px solid rgba(255,255,255,.14);position:relative}
.w .fi h3{font-size:26px;font-weight:500;letter-spacing:-.02em;margin:0;opacity:.45}
.w .fi .dd{display:grid;grid-template-rows:0fr;opacity:0}
.w .fi .dd>div{overflow:hidden}
.w .fi .dd p{font-size:17px;line-height:1.55;color:#B4B4AE;margin:10px 0 0;max-width:400px}
.w .fi .pr{position:absolute;left:0;top:-1px;height:2px;width:0;background:#8FC1FF}
.w .fi:first-child h3{opacity:1}.w .fi:first-child .dd{grid-template-rows:1fr;opacity:1}.w .fi:first-child .pr{width:100%}
.w .ph{position:absolute;left:0;right:0;top:0;display:flex;justify-content:center;opacity:0}
.w .ph:first-of-type{opacity:1}
.w .glow{position:absolute;border-radius:50%;background:radial-gradient(closest-side,rgba(95,160,255,.55),transparent);filter:blur(10px)}
@keyframes tOn{0%,24%{opacity:1}26%,100%{opacity:.45}}
@keyframes dOn{0%,24%{grid-template-rows:1fr;opacity:1}26%,100%{grid-template-rows:0fr;opacity:0}}
@keyframes pOn{0%{width:0}25%{width:100%}25.01%,100%{width:0}}
@keyframes phOn{0%{opacity:0;transform:translateY(28px)}4%,23%{opacity:1;transform:none}27%,100%{opacity:0;transform:translateY(-14px)}}
@media (prefers-reduced-motion:no-preference){
.w .fi h3{animation:tOn 20s infinite}.w .fi .dd{animation:dOn 20s infinite}.w .fi .pr{animation:pOn 20s linear infinite}.w .ph{animation:phOn 20s infinite}
.w .fi:nth-child(1)>*,.w .ph:nth-of-type(1){animation-delay:0s}
.w .fi:nth-child(2)>*,.w .ph:nth-of-type(2){animation-delay:-15s}
.w .fi:nth-child(3)>*,.w .ph:nth-of-type(3){animation-delay:-10s}
.w .fi:nth-child(4)>*,.w .ph:nth-of-type(4){animation-delay:-5s}
.w .band:hover *{animation-play-state:paused}
}
.w .gp{display:inline-flex;align-items:center;height:38px;padding:0 16px;border-radius:19px;background:rgba(255,255,255,.5);backdrop-filter:blur(8px);font-size:14.5px;font-weight:500;color:#1D1D1B}
'''

ICONS = {
    'check': '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6 9 17l-5-5"/></svg>',
    'tag': '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 12V4h8l10 10-8 8L3 12Z"/><circle cx="7.5" cy="8.5" r="1.2"/></svg>',
    'bars': '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M5 20V10M12 20V4M19 20v-7"/></svg>',
}

TOKS = {
    'light': dict(band='#141413', sk1='#B9CDF5', sk2='#DCD6F3', sk3='#FFD9C2', sk4='#FFBE8C', sun='#FFE7BE', cl1='#FFF3E6', cl2='#FFC7A3',
                  mt1='#CDB5DE', mt2='#A98FC8', mt3='#8C77B6', gr1='#C5CE82', gr2='#86B26C', gr3='#6D9F58', wt1='#F6BDBB', wt2='#FCD8C6'),
    'dark': dict(band='#1C1C1A', sk1='#0A1030', sk2='#1B2658', sk3='#4B3B7A', sk4='#C27560', sun='#F0A582', cl1='#7B86C8', cl2='#D08C7C',
                 mt1='#3A3470', mt2='#2C2760', mt3='#221E4E', gr1='#2D4A48', gr2='#1D3A38', gr3='#142C2A', wt1='#7A4F78', wt2='#9E6A78'),
}
SKYV = ['band', 'sk1', 'sk2', 'sk3', 'sk4', 'sun', 'cl1', 'cl2', 'mt1', 'mt2', 'mt3', 'gr1', 'gr2', 'gr3', 'wt1', 'wt2']


def art(uid, par='xMidYMax'):
    """Painted-sky landscape: sky, sun glow, clouds, three ridges, water, meadow. Scales to any size."""
    return f'''<svg class="art" viewBox="0 0 1232 1120" preserveAspectRatio="{par} slice" aria-hidden="true"><defs>
<linearGradient id="sk{uid}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="var(--sk1)"/><stop offset=".3" stop-color="var(--sk2)"/><stop offset=".52" stop-color="var(--sk3)"/><stop offset=".68" stop-color="var(--sk4)"/></linearGradient>
<radialGradient id="sn{uid}" cx=".62" cy=".6" r=".5"><stop offset="0" stop-color="var(--sun)" stop-opacity=".95"/><stop offset="1" stop-color="var(--sun)" stop-opacity="0"/></radialGradient>
<linearGradient id="gr{uid}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="var(--gr1)"/><stop offset=".5" stop-color="var(--gr2)"/><stop offset="1" stop-color="var(--gr3)"/></linearGradient>
<linearGradient id="wt{uid}" x1="0" y1="0" x2="1" y2="0"><stop offset="0" stop-color="var(--wt2)"/><stop offset=".5" stop-color="var(--wt1)"/><stop offset="1" stop-color="var(--wt2)"/></linearGradient>
<filter id="bl{uid}" x="-20%" y="-50%" width="140%" height="200%"><feGaussianBlur stdDeviation="22"/></filter></defs>
<rect width="1232" height="1120" fill="url(#sk{uid})"/><rect width="1232" height="1120" fill="url(#sn{uid})"/>
<g filter="url(#bl{uid})"><ellipse cx="200" cy="230" rx="260" ry="70" fill="var(--cl1)" opacity=".9"/><ellipse cx="330" cy="300" rx="200" ry="50" fill="var(--cl2)" opacity=".7"/><ellipse cx="980" cy="190" rx="300" ry="64" fill="var(--cl1)" opacity=".85"/><ellipse cx="900" cy="270" rx="220" ry="46" fill="var(--cl2)" opacity=".65"/><ellipse cx="620" cy="410" rx="340" ry="40" fill="var(--cl1)" opacity=".6"/></g>
<path d="M0 640 L120 600 C200 580 260 560 330 590 L420 560 C520 520 600 540 680 580 L800 540 C900 500 980 540 1060 580 L1232 600 V760 H0Z" fill="var(--mt1)"/>
<path d="M0 700 C150 640 250 650 380 690 C470 660 560 620 660 640 C780 600 900 640 1010 690 C1100 660 1170 670 1232 690 V800 H0Z" fill="var(--mt2)"/>
<path d="M0 760 C140 720 260 740 360 770 C500 730 640 740 760 770 C900 730 1040 750 1232 760 V860 H0Z" fill="var(--mt3)"/>
<path d="M0 800 C200 780 400 830 640 810 C860 790 1040 820 1232 800 V900 H0Z" fill="url(#wt{uid})"/>
<path d="M0 870 C220 840 420 880 640 862 C880 842 1050 880 1232 860 V1120 H0Z" fill="url(#gr{uid})"/>
<g fill="#F7A8C4" opacity=".8"><circle cx="90" cy="980" r="4"/><circle cx="140" cy="1010" r="3"/><circle cx="220" cy="960" r="4"/><circle cx="1010" cy="990" r="4"/><circle cx="1090" cy="960" r="3"/><circle cx="1160" cy="1010" r="4"/><circle cx="60" cy="1060" r="3"/></g></svg>'''


def build(g):
    import re
    g['CSS'] += CSS
    for t in ('light', 'dark'):
        g['TOK'][t].update(TOKS[t])
    g['VARS'] += SKYV
    GH, dev, footer, doc, write, dark_wrapper, brand = g['GH'], g['dev'], g['footer'], g['doc'], g['write'], g['dark_wrapper'], g['brand']

    def header(m):
        pad = '0 20px' if m else '0 48px'
        return f'<header class="nav" style="padding:{pad};{"height:68px" if m else ""}">{brand(False)}<nav class="links" style="gap:10px"><a class="btn mcta" href="#">{GH.replace("20","16")}GitHub</a><span class="burger" aria-label="Menu"><i></i><i></i></span></nav></header>'

    def hero(m):
        h = 1010 if m else 1130
        ph = 250 if m else 340
        h1 = 'font-size:46px;line-height:1.03' if m else ''
        paper = f'<div class="paper a-settle" style="width:{150 if m else 230}px;height:{240 if m else 340}px;left:{"-40px" if m else "190px"};top:{690 if m else 720}px;transform:rotate(-9deg);z-index:1"><div class="b" style="width:50%;background:var(--ink)"></div><div class="b"></div><div class="b" style="width:72%"></div><div class="b"></div><div style="height:36px;border-radius:12px;background:var(--yellow);margin-top:8px"></div></div>'
        chips = '' if m else f'<div class="fl a-rise" style="right:170px;top:760px;animation-delay:.3s"><i style="background:var(--green)">{ICONS["check"]}</i><div>Adds up<small>€38,13 matches the receipt</small></div></div><div class="fl a-rise" style="right:120px;top:900px;animation-delay:.4s"><i style="background:var(--blue)">{ICONS["tag"]}</i><div>Cheapest this month<small>Oat milk, Corner Market</small></div></div><div class="fl a-rise" style="left:130px;top:980px;animation-delay:.5s"><i style="background:var(--amber)">{ICONS["bars"]}</i><div>Groceries, October<small>€412 across 4 shops</small></div></div>'
        btns = f'<div style="display:flex;gap:12px;justify-content:center;margin-top:30px;flex-direction:{"column" if m else "row"};padding:{"0 20px" if m else "0"}"><a class="btn mcta" href="#" style="height:52px;{"justify-content:center" if m else ""}">{GH.replace("20","16")}View on GitHub</a><a class="btn mcta alt" href="#" style="height:52px;{"justify-content:center" if m else ""}">Read the guide</a></div>'
        badge = '<span class="badge a-rise">MIT licence · open source <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m9 6 6 6-6 6"/></svg></span>'
        txt = f'<div class="hb" style="position:relative;z-index:2;padding:{"20px 0 0" if m else "40px 0 0"}"><div style="text-align:center">{badge}</div><h1 class="a-rise" style="margin-top:24px;{h1}">Photograph the receipt.{"" if m else "<br>"} Keep the data.</h1><p class="lead a-rise" style="{"font-size:17px;padding:0 20px" if m else ""}">Reads your supermarket receipts on your own server, checks every line adds up to the printed total, and shows what your groceries really cost.</p>{btns}</div>'
        phone = f'<div class="tilt" style="position:absolute;left:50%;margin-left:-{ph//2}px;top:{640 if m else 690}px;z-index:2">{dev("receipt",ph)}</div>'
        return f'<section style="padding:16px"><div class="heroP" style="height:{h}px;border-radius:{32 if m else 44}px">{art("h")}{header(m)}{txt}{paper}{phone}{chips}</div></section>'

    def steps(m):
        cb = lambda pos: f'<i class="cb" style="{pos}"></i>'
        v1 = f'<div class="vis">{cb("left:78px;top:20px;border-right:0;border-bottom:0;border-radius:8px 0 0 0")}{cb("right:78px;top:20px;border-left:0;border-bottom:0;border-radius:0 8px 0 0")}<div class="vf"><div class="b" style="width:46%;background:var(--ink)"></div><div class="b"></div><div class="b" style="width:70%"></div><div class="b"></div><div class="b" style="width:55%"></div><div style="height:30px;border-radius:10px;background:var(--yellow);margin-top:6px"></div></div></div>'
        v2 = f'<div class="vis"><div class="vl"><div class="r"><span>Halfvolle melk</span><b>1,79</b></div><div class="r"><span>Bananen</span><b>1,68</b></div><div class="r"><span>Hummus naturel</span><b>1,49</b></div><div class="tot"><span>Total €4,96</span><span class="okp">{ICONS["check"].replace("22","13")}Adds up</span></div></div></div>'
        bars = [(120, ['var(--blue)', 'var(--green)', 'var(--yellow)']), (160, ['var(--blue)', 'var(--green)', 'var(--amber)']), (100, ['var(--blue)', 'var(--yellow)']), (190, ['var(--blue)', 'var(--green)', 'var(--yellow)', 'var(--amber)']), (140, ['var(--blue)', 'var(--green)', 'var(--amber)']), (170, ['var(--blue)', 'var(--green)', 'var(--yellow)'])]
        bi = ''.join(f'<i style="height:{hh}px">' + ''.join(f'<s style="height:{hh//len(c)}px;background:{col}"></s>' for col in c) + '</i>' for hh, c in bars)
        v3 = f'<div class="vis"><div class="vb">{bi}</div></div>'
        data = [('1', 'Photograph', 'Scan a paper receipt or import a photo. It uploads in the background, even offline.', v1), ('2', 'Check', 'Every line has to add up to the printed total. If not, you see which row to fix.', v2), ('3', 'Understand', 'Spend by month, shop and category, and the cheapest place for each product.', v3)]
        cards = ''.join(f'<article class="sx a-scroll" style="height:{500 if m else 520}px"><div class="tx"><div class="num">{n}</div><h3>{t}</h3><p>{d}</p></div>{v}</article>' for n, t, d, v in data)
        return f'<section style="padding:{"64px 20px 0" if m else "120px 80px 0"}"><h2 class="h2 lt" style="font-size:{34 if m else 56}px;line-height:1.06;margin-bottom:{28 if m else 48}px;max-width:720px">From paper to numbers in three steps.</h2><div style="display:grid;grid-template-columns:repeat({1 if m else 3},minmax(0,1fr));gap:{14 if m else 18}px">{cards}</div></section>'

    def showcase(m):
        F = [('Checks every receipt', 'The lines have to add up to the printed total. If they do not, you see exactly which row to fix.', 'receipt'),
             ('Finds the cheapest shop', 'Prices per kilo, litre or piece, per shop, so the comparison is fair.', 'product'),
             ('Shows where the money goes', 'Spend by month, shop and category, without a spreadsheet.', 'analytics'),
             ('Keeps products tidy', 'Names cleaned up, categories filled in, barcodes linked.', 'products')]
        items = ''.join(f'<div class="fi"><i class="pr"></i><h3>{t}</h3><div class="dd"><div><p>{d}</p></div></div></div>' for t, d, s in F)
        pw = 250 if m else 300
        phs = ''.join(f'<div class="ph">{dev(s, pw)}</div>' for t, d, s in F)
        left = f'<div style="flex:1;min-width:0"><div style="font-size:13px;color:#8FC1FF;font-weight:500;margin-bottom:14px">What it does</div><h2 class="h2 lt" style="font-size:{34 if m else 56}px;line-height:1.05;margin-bottom:{24 if m else 36}px;color:#F4F4F1">Everything a receipt knows.</h2><div class="fx">{items}</div></div>'
        right = f'<div style="flex:none;position:relative;width:{pw+40}px;height:{440 if m else 560}px;{"align-self:center;margin-top:28px" if m else "margin-top:20px"}"><i class="glow" style="width:{pw+120}px;height:{pw+120}px;left:-40px;top:40px"></i>{phs}</div>'
        return f'<section style="padding:{"64px 20px 0" if m else "120px 80px 0"}"><div class="band" style="padding:{"36px 24px 0" if m else "72px 72px 0"};display:flex;flex-direction:{"column" if m else "row"};gap:{0 if m else 56}px;height:{"auto" if m else "640px"}">{left}{right}</div></section>'

    def closing(m):
        h = 600 if m else 600
        pills = ''.join(f'<span class="gp">{t}</span>' for t in ['Your server, your data', 'No account, no analytics', 'MIT licensed'])
        return f'<section style="padding:{"64px 16px 16px" if m else "120px 16px 16px"}"><div class="heroP" style="height:{h}px;border-radius:{32 if m else 44}px;display:flex;flex-direction:column;align-items:center;justify-content:flex-start;text-align:center;padding:{"56px 24px 0" if m else "72px 0 0"}">{art("c", "xMidYMid")}<h2 class="h2 lt" style="position:relative;z-index:2;font-size:{40 if m else 72}px;line-height:1.02">Run it at home.</h2><p style="position:relative;z-index:2;font-size:{17 if m else 20}px;line-height:1.5;color:var(--ink2);margin-top:16px;max-width:520px">Yours, on your own server. Install it with Docker in an evening.</p><div style="position:relative;z-index:2;display:flex;gap:10px;margin-top:28px;flex-wrap:wrap;justify-content:center"><a class="btn mcta" href="#" style="height:52px">{GH.replace("20","16")}View on GitHub</a></div><div style="position:relative;z-index:2;display:flex;gap:10px;flex-wrap:wrap;justify-content:center;margin-top:{20 if m else 28}px">{pills}</div></div></section>'

    def body(m):
        return hero(m) + steps(m) + showcase(m) + closing(m) + footer(m, False)

    boards = [('HB-Page-Desktop', False, 1280, 4300, 'Website · direction B, desktop, light'), ('HB-Page-Mobile', True, 390, 5200, 'Website · direction B, mobile, light')]
    for name, m, w, h, title in boards:
        write(f'{name}.dc.html', doc('x', title, body(m), w, h, 'light'))
        dark_wrapper(f'D-{name}.dc.html', name, title.replace('light', 'dark'), w, h)
        flat = re.sub(r'<dc-import name="Brand"[^>]*></dc-import>', brand(True), body(m))
        for th in ('light', 'dark'):
            write(f'{name.lower()}-{th}.html', doc('x', title, flat, w, h, th, preview=True), preview=True)
