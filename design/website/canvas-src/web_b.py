"""Website final page (direction B merged), third pass after comments.
Full-bleed hero and closing panel with a painterly, grainy sky (roughened ridges, haze, no cartoon details),
three step cards about the journey (till -> your server -> the app), a clean light feature showcase whose left list
cycles while the phone changes (CSS only), footer. Called from gen.py's main block: web_b.build(globals())."""

CSS = '''
.w .hb h1{font-size:88px;line-height:1;font-weight:400;letter-spacing:-.05em;text-align:center}
.w .hb .lead{font-size:21px;line-height:1.5;color:var(--ink2);text-align:center;max-width:620px;margin:22px auto 0}
.w .heroP .lead,.w .heroP p{color:var(--ink)!important;opacity:.82}
.w .h2.lt{font-weight:400;letter-spacing:-.045em}
.w .btn.alt{background:rgba(255,255,255,.6);color:var(--ink);backdrop-filter:blur(8px)}
.w .dk .btn.alt{background:rgba(255,255,255,.14)}
.w .burger{width:44px;height:44px;border-radius:50%;display:inline-flex;flex-direction:column;align-items:center;justify-content:center;gap:6px;background:rgba(255,255,255,.55);flex:none}
.w .burger i{display:block;width:18px;height:2px;border-radius:1px;background:#2B2D3F}
.w .badge{display:inline-flex;align-items:center;gap:8px;height:34px;padding:0 14px 0 16px;border-radius:17px;background:rgba(43,45,63,.82);color:#F4F4F1;font-size:13.5px;font-weight:500;backdrop-filter:blur(10px)}
.w .art{position:absolute;inset:0;width:100%;height:100%;display:block}
.w .heroP{position:relative;overflow:hidden;background:var(--sk1)}
.w .heroP .nav{position:relative;z-index:3}
.w .heroP .brand{color:#2B2D3F}
.w .fade{position:absolute;left:0;right:0;bottom:0;z-index:3;pointer-events:none;background:linear-gradient(180deg,transparent,var(--bg) 92%)}
.w .fadeT{position:absolute;left:0;right:0;top:0;z-index:1;pointer-events:none;background:linear-gradient(180deg,var(--bg),transparent)}
.w .fl{position:absolute;background:var(--bg);border-radius:20px;box-shadow:0 18px 40px -16px var(--shadow);padding:14px 18px;display:flex;align-items:center;gap:12px;font-size:15px;font-weight:500;white-space:nowrap;z-index:4}
.w .fl i{width:28px;height:28px;border-radius:50%;display:flex;align-items:center;justify-content:center;flex:none;font-style:normal;color:#fff;font-size:15px}
.w .fl small{display:block;font-size:12.5px;font-weight:400;color:var(--ink2)}
.w .tilt{transform:perspective(1800px) rotateY(-9deg) rotateX(3deg) rotate(1.5deg);transform-origin:50% 100%}
.w .sx{border-radius:34px;background:var(--sunk);overflow:hidden;position:relative;display:flex;flex-direction:column}
.w .sx .tx{padding:30px 30px 0}
.w .sx .num{width:34px;height:34px;border-radius:50%;background:var(--blueTint);color:var(--blueText);font-size:15px;font-weight:500;display:flex;align-items:center;justify-content:center;margin-bottom:18px}
.w .sx h3{font-size:28px;line-height:1.12;font-weight:500;letter-spacing:-.025em;margin:0 0 8px}
.w .sx p{font-size:16.5px;line-height:1.5;color:var(--ink2);margin:0;max-width:310px}
.w .sx .vis{margin-top:auto;height:240px;position:relative}
.w .vf{position:absolute;left:50%;bottom:-40px;width:230px;height:250px;margin-left:-115px;background:var(--bg);border-radius:20px;box-shadow:0 24px 50px -20px var(--shadow);padding:26px 22px;display:flex;flex-direction:column;gap:12px;transform:rotate(-4deg)}
.w .vf .b{height:9px;border-radius:5px;background:var(--line)}
.w .cb{position:absolute;width:26px;height:26px;border:3px solid var(--blue);z-index:2}
.w .sv{position:absolute;left:0;right:0;top:34px;display:flex;align-items:center;justify-content:center;gap:0}
.w .sv .ph1{width:62px;height:112px;border-radius:16px;border:3px solid var(--ink3);background:var(--bg);position:relative;flex:none}
.w .sv .ph1:after{content:"";position:absolute;left:12px;right:12px;top:16px;height:8px;border-radius:4px;background:var(--line);box-shadow:0 16px 0 var(--line),0 32px 0 var(--line)}
.w .sv .ln{width:70px;border-top:3px dashed var(--ink3);flex:none}
.w .sv .mini{width:150px;height:84px;border-radius:22px;background:var(--ink2);position:relative;flex:none}
.w .sv .mini:after{content:"";position:absolute;right:16px;bottom:16px;width:9px;height:9px;border-radius:50%;background:var(--green)}
.w .lk{position:absolute;left:50%;margin-left:-98px;top:170px;width:196px;height:40px;border-radius:20px;background:var(--bg);box-shadow:0 14px 30px -14px var(--shadow);display:flex;align-items:center;justify-content:center;gap:8px;font-size:14px;font-weight:500}
.w .nc{position:absolute;left:34px;right:34px;background:var(--bg);border-radius:20px;box-shadow:0 24px 50px -22px var(--shadow);padding:16px 18px;display:flex;align-items:center;gap:14px;font-size:15px;font-weight:500}
.w .nc i{width:36px;height:36px;border-radius:11px;display:flex;align-items:center;justify-content:center;flex:none;color:#fff;font-style:normal}
.w .nc small{display:block;font-size:13px;font-weight:400;color:var(--ink2)}
.w .fx{display:flex;flex-direction:column}
.w .fi{padding:22px 0 20px;position:relative}
.w .fi h3{font-size:26px;font-weight:500;letter-spacing:-.02em;margin:0;opacity:.4}
.w .fi .dd{display:grid;grid-template-rows:0fr;opacity:0}
.w .fi .dd>div{overflow:hidden}
.w .fi .dd p{font-size:17px;line-height:1.55;color:var(--ink2);margin:10px 0 0;max-width:400px}
.w .fi .pr{position:absolute;left:0;top:-1px;height:2px;width:0;background:var(--blue)}
.w .fi:first-child h3{opacity:1}.w .fi:first-child .dd{grid-template-rows:1fr;opacity:1}.w .fi:first-child .pr{width:100%}
.w .stage{position:relative;flex:1;border-radius:36px;background:var(--panel);overflow:hidden}
.w .ph{position:absolute;left:0;right:0;top:70px;display:flex;justify-content:center;opacity:0}
.w .ph:first-of-type{opacity:1}
@keyframes tOn{0%,24%{opacity:1}26%,100%{opacity:.4}}
@keyframes dOn{0%,24%{grid-template-rows:1fr;opacity:1}26%,100%{grid-template-rows:0fr;opacity:0}}
@keyframes pOn{0%{width:0}25%{width:100%}25.01%,100%{width:0}}
@keyframes phOn{0%{opacity:0;transform:translateY(28px)}4%,23%{opacity:1;transform:none}27%,100%{opacity:0;transform:translateY(-14px)}}
@media (prefers-reduced-motion:no-preference){
.w .fi h3{animation:tOn 20s infinite}.w .fi .dd{animation:dOn 20s infinite}.w .fi .pr{animation:pOn 20s linear infinite}.w .ph{animation:phOn 20s infinite}
.w .fi:nth-child(1)>*,.w .ph:nth-of-type(1){animation-delay:0s}
.w .fi:nth-child(2)>*,.w .ph:nth-of-type(2){animation-delay:-15s}
.w .fi:nth-child(3)>*,.w .ph:nth-of-type(3){animation-delay:-10s}
.w .fi:nth-child(4)>*,.w .ph:nth-of-type(4){animation-delay:-5s}
.w .shw:hover *{animation-play-state:paused}
}
.w .gp{display:inline-flex;align-items:center;height:38px;padding:0 16px;border-radius:19px;background:rgba(255,255,255,.5);backdrop-filter:blur(8px);font-size:14.5px;font-weight:500;color:#2B2D3F}
'''

ICONS = {
    'check': '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6 9 17l-5-5"/></svg>',
    'tag': '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 12V4h8l10 10-8 8L3 12Z"/><circle cx="7.5" cy="8.5" r="1.2"/></svg>',
    'bars': '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M5 20V10M12 20V4M19 20v-7"/></svg>',
    'lock': '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/></svg>',
    'warn': '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><path d="M12 7v6M12 17v.1"/></svg>',
}

TOKS = {
    'light': dict(sk1='#C4D3F2', sk2='#E2DCF1', sk3='#F8DCCB', sk4='#F2C4A2', sun='#FFE6C7', cl1='#FFF4EA', cl2='#F7C9AB',
                  mt1='#D9C9E8', mt2='#B9A5D6', mt3='#8E80BE', gr1='#7F72B0', gr2='#6A5FA0', gr3='#574E8C'),
    'dark': dict(sk1='#080D24', sk2='#16204A', sk3='#3E3270', sk4='#8A5868', sun='#C98A86', cl1='#6872B8', cl2='#8C5E78',
                 mt1='#4A4380', mt2='#352F6A', mt3='#251F52', gr1='#1A1840', gr2='#141233', gr3='#0F0D28'),
}
SKYV = ['sk1', 'sk2', 'sk3', 'sk4', 'sun', 'cl1', 'cl2', 'mt1', 'mt2', 'mt3', 'gr1', 'gr2', 'gr3']


def art(uid, par='xMidYMax'):
    """Painterly dusk: gradient sky, blurred colour fields, haze, three ridges with roughened edges, soft foreground, film grain."""
    return f'''<svg class="art" viewBox="0 0 1232 1120" preserveAspectRatio="{par} slice" aria-hidden="true"><defs>
<linearGradient id="sk{uid}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="var(--sk1)"/><stop offset=".34" stop-color="var(--sk2)"/><stop offset=".56" stop-color="var(--sk3)"/><stop offset=".7" stop-color="var(--sk4)"/></linearGradient>
<linearGradient id="fg{uid}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="var(--gr1)" stop-opacity="0"/><stop offset=".35" stop-color="var(--gr1)" stop-opacity=".8"/><stop offset="1" stop-color="var(--gr3)"/></linearGradient>
<linearGradient id="r1{uid}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="var(--mt1)"/><stop offset="1" stop-color="var(--mt2)" stop-opacity=".6"/></linearGradient>
<linearGradient id="r2{uid}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="var(--mt2)"/><stop offset="1" stop-color="var(--mt3)" stop-opacity=".7"/></linearGradient>
<linearGradient id="r3{uid}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="var(--mt3)"/><stop offset="1" stop-color="var(--gr1)"/></linearGradient>
<filter id="bl{uid}" x="-30%" y="-30%" width="160%" height="160%"><feGaussianBlur stdDeviation="48"/></filter>
<filter id="hz{uid}" x="-10%" y="-100%" width="120%" height="300%"><feGaussianBlur stdDeviation="26"/></filter>
<filter id="rf{uid}" x="-5%" y="-10%" width="110%" height="120%"><feTurbulence type="fractalNoise" baseFrequency="0.009 0.022" numOctaves="3" seed="7" result="n"/><feDisplacementMap in="SourceGraphic" in2="n" scale="62" xChannelSelector="R" yChannelSelector="G"/><feGaussianBlur stdDeviation="1.4"/></filter>
<filter id="gn{uid}" x="0" y="0" width="100%" height="100%"><feTurbulence type="fractalNoise" baseFrequency=".8" numOctaves="2" seed="3" result="t"/><feColorMatrix in="t" type="matrix" values="0 0 0 0 .45  0 0 0 0 .4  0 0 0 0 .5  1.6 0 0 0 -.55"/></filter></defs>
<rect width="1232" height="1120" fill="url(#sk{uid})"/>
<g filter="url(#bl{uid})"><ellipse cx="170" cy="190" rx="340" ry="190" fill="var(--sk1)" opacity=".9"/><ellipse cx="980" cy="560" rx="420" ry="170" fill="var(--cl2)" opacity=".75"/><ellipse cx="700" cy="470" rx="380" ry="120" fill="var(--sun)" opacity=".85"/><ellipse cx="1020" cy="170" rx="300" ry="90" fill="var(--cl1)" opacity=".7"/><ellipse cx="260" cy="430" rx="280" ry="70" fill="var(--cl1)" opacity=".55"/></g>
<g filter="url(#rf{uid})"><path d="M-60 650 L120 600 C230 560 300 590 380 580 L470 540 C560 505 620 560 700 575 L810 520 C900 480 990 540 1080 570 L1300 590 V800 H-60Z" fill="url(#r1{uid})"/></g>
<rect x="0" y="600" width="1232" height="140" fill="var(--cl1)" opacity=".5" filter="url(#hz{uid})"/>
<g filter="url(#rf{uid})"><path d="M-60 720 C100 650 230 665 360 705 C470 665 560 640 650 660 C780 615 920 660 1030 705 C1120 675 1200 685 1300 700 V860 H-60Z" fill="url(#r2{uid})"/></g>
<rect x="0" y="690" width="1232" height="120" fill="var(--cl1)" opacity=".35" filter="url(#hz{uid})"/>
<g filter="url(#rf{uid})"><path d="M-60 790 C130 735 270 760 380 790 C520 745 650 760 780 792 C920 750 1060 770 1300 786 V940 H-60Z" fill="url(#r3{uid})"/></g>
<rect y="800" width="1232" height="320" fill="url(#fg{uid})"/>
<rect width="1232" height="1120" filter="url(#gn{uid})" opacity=".5" style="mix-blend-mode:soft-light"/></svg>'''


def build(g):
    import re
    g['CSS'] += CSS
    for t in ('light', 'dark'):
        g['TOK'][t].update(TOKS[t])
    g['TOK']['light']['band'] = '#141413'; g['TOK']['dark']['band'] = '#1C1C1A'
    g['VARS'] += SKYV + ['band']
    GH, dev, footer, doc, write, dark_wrapper, brand = g['GH'], g['dev'], g['footer'], g['doc'], g['write'], g['dark_wrapper'], g['brand']

    def header(m):
        pad = '0 20px' if m else '0 48px'
        return f'<header class="nav" style="padding:{pad};{"height:68px" if m else ""}">{brand(False)}<nav class="links" style="gap:10px"><a class="btn sm" href="#">GitHub</a><span class="burger" aria-label="Menu"><i></i><i></i></span></nav></header>'

    def hero(m):
        h = 1010 if m else 1130
        ph = 250 if m else 340
        h1 = 'font-size:46px;line-height:1.03' if m else ''
        paper = f'<div class="paper a-settle" style="width:{150 if m else 230}px;height:{240 if m else 340}px;left:{"-40px" if m else "190px"};top:{690 if m else 720}px;transform:rotate(-9deg);z-index:1"><div class="b" style="width:50%;background:var(--ink)"></div><div class="b"></div><div class="b" style="width:72%"></div><div class="b"></div><div style="height:36px;border-radius:12px;background:var(--yellow);margin-top:8px"></div></div>'
        chips = '' if m else f'<div class="fl a-rise" style="right:170px;top:760px;animation-delay:.3s"><i style="background:var(--green)">{ICONS["check"]}</i><div>Adds up<small>€38,13 matches the receipt</small></div></div><div class="fl a-rise" style="right:120px;top:900px;animation-delay:.4s"><i style="background:var(--blue)">{ICONS["tag"]}</i><div>Cheapest this month<small>Oat milk, Corner Market</small></div></div><div class="fl a-rise" style="left:130px;top:980px;animation-delay:.5s"><i style="background:var(--amber)">{ICONS["bars"]}</i><div>Groceries, October<small>€412 across 4 shops</small></div></div>'
        btns = f'<div style="display:flex;gap:12px;justify-content:center;margin-top:30px;flex-direction:{"column" if m else "row"};padding:{"0 20px" if m else "0"}"><a class="btn" href="#" style="{"flex:1" if m else "min-width:220px"}">View on GitHub</a><a class="btn alt" href="#" style="{"flex:1" if m else "min-width:220px"}">Read the guide</a></div>'
        badge = '<span class="badge a-rise">MIT licence · open source <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m9 6 6 6-6 6"/></svg></span>'
        txt = f'<div class="hb" style="position:relative;z-index:2;padding:{"20px 0 0" if m else "40px 0 0"}"><div style="text-align:center">{badge}</div><h1 class="a-rise" style="margin-top:24px;{h1}">Photograph the receipt.{"" if m else "<br>"} Keep the data.</h1><p class="lead a-rise" style="{"font-size:17px;padding:0 20px" if m else ""}">Reads your supermarket receipts on your own server, checks every line adds up to the printed total, and shows what your groceries really cost.</p>{btns}</div>'
        phone = f'<div class="tilt" style="position:absolute;left:50%;margin-left:-{ph//2}px;top:{640 if m else 690}px;z-index:2">{dev("receipt",ph)}</div>'
        return f'<section><div class="heroP" style="height:{h}px">{art("h")}{header(m)}{txt}{paper}{phone}<div class="fade" style="height:{220 if m else 300}px"></div>{chips}</div></section>'

    def steps(m):
        cb = lambda pos: f'<i class="cb" style="{pos}"></i>'
        v1 = f'<div class="vis">{cb("left:78px;top:20px;border-right:0;border-bottom:0;border-radius:8px 0 0 0")}{cb("right:78px;top:20px;border-left:0;border-bottom:0;border-radius:0 8px 0 0")}<div class="vf"><div class="b" style="width:46%;background:var(--ink)"></div><div class="b"></div><div class="b" style="width:70%"></div><div class="b"></div><div class="b" style="width:55%"></div><div style="height:30px;border-radius:10px;background:var(--yellow);margin-top:6px"></div></div></div>'
        v2 = f'<div class="vis"><div class="sv"><div class="ph1"></div><div class="ln"></div><div class="mini"></div></div><div class="lk">{ICONS["lock"]}Stays on your network</div></div>'
        v3 = f'<div class="vis"><div class="nc" style="top:26px"><i style="background:var(--green)">{ICONS["check"].replace("22","20")}</i><div>Receipt ready<small>Corner Market · 15 lines</small></div></div><div class="nc" style="top:108px;transform:translateX(10px)"><i style="background:var(--amber)">{ICONS["warn"]}</i><div>One row needs a look<small>Tap to fix it in the app</small></div></div></div>'
        data = [('1', 'Snap it', 'Photograph the receipt at the till, or import a photo later. No signal? It waits and sends when you are back.', v1), ('2', 'Your server reads it', 'The photo goes to a machine you own. Text recognition runs there and nothing leaves your network.', v2), ('3', 'Open the app', 'A few seconds later the receipt is in your list, with anything doubtful marked for a quick look.', v3)]
        cards = ''.join(f'<article class="sx a-scroll" style="height:{500 if m else 520}px"><div class="tx"><div class="num">{n}</div><h3>{t}</h3><p>{d}</p></div>{v}</article>' for n, t, d, v in data)
        return f'<section style="padding:{"64px 20px 0" if m else "120px 80px 0"}"><h2 class="h2 lt" style="font-size:{34 if m else 56}px;line-height:1.06;margin-bottom:{28 if m else 48}px;max-width:760px">From the till to your phone.</h2><div style="display:grid;grid-template-columns:repeat({1 if m else 3},minmax(0,1fr));gap:{14 if m else 18}px">{cards}</div></section>'

    def showcase(m):
        F = [('Checks every receipt', 'The lines have to add up to the printed total. If they do not, you see exactly which row to fix.', 'receipt'),
             ('Finds the cheapest shop', 'Prices per kilo, litre or piece, per shop, so the comparison is fair.', 'product'),
             ('Shows where the money goes', 'Spend by month, shop and category, without a spreadsheet.', 'analytics'),
             ('Keeps products tidy', 'Names cleaned up, categories filled in, barcodes linked.', 'products')]
        items = ''.join(f'<div class="fi"><i class="pr"></i><h3>{t}</h3><div class="dd"><div><p>{d}</p></div></div></div>' for t, d, s in F)
        pw = 250 if m else 290
        phs = ''.join(f'<div class="ph" style="{"top:50px" if m else ""}">{dev(s, pw)}</div>' for t, d, s in F)
        left = f'<div style="{"" if m else "width:440px;flex:none"}"><h2 class="h2 lt" style="font-size:{34 if m else 56}px;line-height:1.05;margin-bottom:{24 if m else 36}px">Everything a receipt knows.</h2><div class="fx">{items}</div></div>'
        right = f'<div class="stage" style="{"height:460px;margin-top:28px" if m else "height:620px"}">{phs}</div>'
        return f'<section class="shw" style="padding:{"64px 20px 0" if m else "120px 80px 0"}"><div style="display:flex;flex-direction:{"column" if m else "row"};gap:{0 if m else 64}px;align-items:{"stretch" if m else "flex-start"}">{left}{right}</div></section>'

    def closing(m):
        h = 600
        pills = ''.join(f'<span class="gp">{t}</span>' for t in ['Your server, your data', 'No account, no analytics', 'MIT licensed'])
        return f'<section style="padding:{"64px 0 0" if m else "120px 0 0"}"><div class="heroP" style="height:{h}px;display:flex;flex-direction:column;align-items:center;justify-content:flex-start;text-align:center;padding:{"56px 24px 0" if m else "80px 0 0"}">{art("c", "xMidYMid")}<div class="fadeT" style="height:{160 if m else 220}px"></div><div class="fade" style="height:{140 if m else 180}px"></div><h2 class="h2 lt" style="position:relative;z-index:2;font-size:{40 if m else 72}px;line-height:1.02">Run it at home.</h2><p style="position:relative;z-index:2;font-size:{17 if m else 20}px;line-height:1.5;color:var(--ink2);margin-top:16px;max-width:520px">Yours, on your own server. Install it with Docker on a machine you already have.</p><div style="position:relative;z-index:2;display:flex;gap:10px;margin-top:28px;flex-wrap:wrap;justify-content:center"><a class="btn" href="#" style="min-width:220px">View on GitHub</a></div><div style="position:relative;z-index:2;display:flex;gap:10px;flex-wrap:wrap;justify-content:center;margin-top:{20 if m else 28}px">{pills}</div></div></section>'

    def body(m):
        return hero(m) + steps(m) + showcase(m) + closing(m) + footer(m, False)

    boards = [('HB-Page-Desktop', False, 1280, 3800, 'Website · final page, desktop, light'), ('HB-Page-Mobile', True, 390, 4900, 'Website · final page, mobile, light')]
    for name, m, w, h, title in boards:
        write(f'{name}.dc.html', doc('x', title, body(m), w, h, 'light'))
        dark_wrapper(f'D-{name}.dc.html', name, title.replace('light', 'dark'), w, h)
        flat = re.sub(r'<dc-import name="Brand"[^>]*></dc-import>', brand(True), body(m))
        for th in ('light', 'dark'):
            write(f'{name.lower()}-{th}.html', doc('x', title, flat, w, h, th, preview=True), preview=True)
