"""Website direction B ("story"): centred hero with a wide panel, a 3-step how-it-works, a dark band about owning your data,
a compact "also" grid. Called from gen.py's main block: web_b.build(globals())."""

CSS = '''
.w .hb h1{font-size:88px;line-height:1;font-weight:500;letter-spacing:-.045em;text-align:center}
.w .hb .lead{font-size:21px;line-height:1.5;color:var(--ink2);text-align:center;max-width:620px;margin:22px auto 0}
.w .btn.alt{background:var(--sunk);color:var(--ink)}
.w .pnl{position:relative;border-radius:44px;background:var(--panel);overflow:hidden}
.w .fl{position:absolute;background:var(--bg);border-radius:20px;box-shadow:0 18px 40px -16px var(--shadow);padding:14px 18px;display:flex;align-items:center;gap:12px;font-size:15px;font-weight:500;white-space:nowrap}
.w .fl i{width:28px;height:28px;border-radius:50%;display:flex;align-items:center;justify-content:center;flex:none;font-style:normal;color:#fff;font-size:15px}
.w .fl small{display:block;font-size:12.5px;font-weight:400;color:var(--ink2)}
.w .hw{display:grid;gap:20px}
.w .hw .st{border-top:2px solid var(--ink);padding-top:20px}
.w .hw .st b{font-family:'GT America Mono',ui-monospace,monospace;font-size:13px;font-weight:400;color:var(--ink2);display:block;margin-bottom:12px}
.w .hw .st h3{font-size:28px;line-height:1.15;font-weight:500;letter-spacing:-.025em;margin:0 0 10px}
.w .hw .st p{font-size:17px;line-height:1.55;color:var(--ink2)}
.w .band{background:var(--band);color:#F4F4F1;border-radius:44px;overflow:hidden;position:relative}
.w .band p{color:#B4B4AE}
.w .alsoc{border:1px solid var(--line);border-radius:26px;padding:26px 28px}
.w .alsoc h3{font-size:22px;font-weight:500;letter-spacing:-.02em;margin:14px 0 6px}
.w .alsoc p{font-size:16px;line-height:1.5;color:var(--ink2)}
.w .ico{width:44px;height:44px;border-radius:14px;background:var(--blueTint);color:var(--blueText);display:flex;align-items:center;justify-content:center}
'''

ICONS = {
    'check': '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6 9 17l-5-5"/></svg>',
    'tag': '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 12V4h8l10 10-8 8L3 12Z"/><circle cx="7.5" cy="8.5" r="1.2"/></svg>',
    'bars': '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M5 20V10M12 20V4M19 20v-7"/></svg>',
    'box': '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 8l9-5 9 5v8l-9 5-9-5V8Z"/><path d="M3 8l9 5 9-5M12 13v8"/></svg>',
}


def build(g):
    g['CSS'] += CSS
    for t, v in (('light', dict(band='#141413')), ('dark', dict(band='#1C1C1A'))):
        g['TOK'][t].update(v)
    g['VARS'] += ['band']
    GH, nav, dev, footer, doc, write, dark_wrapper = g['GH'], g['nav'], g['dev'], g['footer'], g['doc'], g['write'], g['dark_wrapper']

    def hero(m):
        pad = '20px 20px 0' if m else '40px 80px 0'
        h1 = 'font-size:46px;line-height:1.03' if m else ''
        btns = f'<div style="display:flex;gap:12px;justify-content:center;margin-top:30px;flex-direction:{"column" if m else "row"}"><a class="btn" href="#" style="{"justify-content:center" if m else ""}">{GH}View on GitHub</a><a class="btn alt" href="#" style="{"justify-content:center" if m else ""}">Read the guide</a></div>'
        ph = 250 if m else 340
        paper = f'<div class="paper a-settle" style="width:{150 if m else 230}px;height:{240 if m else 340}px;left:{"-40px" if m else "120px"};top:{120 if m else 150}px;transform:rotate(-9deg)"><div class="b" style="width:50%;background:var(--ink)"></div><div class="b"></div><div class="b" style="width:72%"></div><div class="b"></div><div style="height:36px;border-radius:12px;background:var(--yellow);margin-top:8px"></div></div>'
        chips = '' if m else f'<div class="fl a-rise" style="right:110px;top:150px;animation-delay:.3s"><i style="background:var(--green)">{ICONS["check"]}</i><div>Adds up<small>€38,13 matches the receipt</small></div></div><div class="fl a-rise" style="right:70px;top:300px;animation-delay:.4s"><i style="background:var(--blue)">{ICONS["tag"]}</i><div>Cheapest this month<small>Oat milk, Corner Market</small></div></div><div class="fl a-rise" style="left:70px;bottom:120px;animation-delay:.5s"><i style="background:var(--amber)">{ICONS["bars"]}</i><div>Groceries, October<small>€412 across 4 shops</small></div></div>'
        panel = f'<div class="pnl" style="height:{520 if m else 640}px;margin-top:{40 if m else 56}px;border-radius:{32 if m else 44}px">{paper}<div style="position:absolute;left:50%;margin-left:-{ph//2}px;top:{50 if m else 60}px">{dev("receipt",ph)}</div>{chips}</div>'
        return f'<section class="hb" style="padding:{pad}"><div style="text-align:center"><span class="eyebrow a-rise"><i></i>Open source · self-hosted · iPhone</span></div><h1 class="a-rise" style="margin-top:22px;{h1}">Photograph the receipt.{'' if m else '<br>'} Keep the data.</h1><p class="lead a-rise" style="{"font-size:17px" if m else ""}">Reads your supermarket receipts on your own server, checks every line adds up to the printed total, and shows what your groceries really cost.</p>{btns}{panel}</section>'

    def how(m):
        steps = [('01', 'Photograph', 'Scan a paper receipt or import a photo. It uploads in the background, even offline.'),
                 ('02', 'Check', 'Every line has to add up to the printed total. If not, you see exactly which row to fix.'),
                 ('03', 'Understand', 'Spend by month, shop and category, and the cheapest place for each product.')]
        cols = f'grid-template-columns:repeat({1 if m else 3},minmax(0,1fr));gap:{28 if m else 40}px'
        body = ''.join(f'<div class="st a-scroll"><b>{a}</b><h3>{t}</h3><p>{d}</p></div>' for a, t, d in steps)
        return f'<section style="padding:{"56px 20px 0" if m else "112px 80px 0"}"><h2 class="h2" style="font-size:{32 if m else 52}px;line-height:1.08;margin-bottom:{28 if m else 48}px;max-width:640px">From paper to numbers in three steps.</h2><div class="hw" style="{cols}">{body}</div></section>'

    def bandsec(m):
        pts = [('Your server, your data', 'Receipts and the database stay on a machine you own. Text recognition runs there too.'),
               ('No account, no analytics', 'The app talks only to your server. Nothing is sold, nothing is sent away.'),
               ('Open source', 'MIT licensed. Read it, change it, run it.')]
        lis = ''.join(f'<div style="padding:18px 0;border-top:1px solid rgba(255,255,255,.14)"><div style="font-size:19px;font-weight:500">{a}</div><p style="font-size:16px;line-height:1.5;margin-top:4px">{b}</p></div>' for a, b in pts)
        left = f'<div style="flex:1;min-width:0"><div style="font-size:13px;color:#8FC1FF;font-weight:500;margin-bottom:14px">Self-hosted</div><h2 class="h2" style="font-size:{32 if m else 52}px;line-height:1.08;margin-bottom:{24 if m else 32}px;color:#F4F4F1">Yours, on your own server.</h2>{lis}</div>'
        right = f'<div style="flex:none;{"align-self:center;margin-top:36px" if m else "margin-bottom:-120px;margin-top:20px"}">{dev("analytics",250 if m else 300)}</div>'
        return f'<section style="padding:{"56px 20px 0" if m else "112px 80px 0"}"><div class="band" style="padding:{"36px 24px 0" if m else "72px 72px 0"};display:flex;flex-direction:{"column" if m else "row"};gap:{0 if m else 64}px;height:{"auto" if m else "640px"};{"max-height:none" if m else ""}">{left}{right}</div></section>'

    def also(m):
        items = [('check', 'Checks every receipt', 'Lines must add up to the printed total.'), ('tag', 'Finds the cheapest shop', 'Prices per kilo, litre or piece, per shop.'),
                 ('bars', 'Shows where the money goes', 'By month, shop and category.'), ('box', 'Keeps products tidy', 'Clean names, categories, barcodes linked.')]
        cards = ''.join(f'<div class="alsoc a-scroll"><div class="ico">{ICONS[i]}</div><h3>{t}</h3><p>{d}</p></div>' for i, t, d in items)
        return f'<section style="padding:{"56px 20px 56px" if m else "112px 80px 96px"}"><h2 class="h2" style="font-size:{32 if m else 52}px;line-height:1.08;margin-bottom:{24 if m else 40}px">What it does.</h2><div style="display:grid;grid-template-columns:repeat({1 if m else 4},minmax(0,1fr));gap:16px">{cards}</div></section>'

    def body(m):
        return nav(m, False) + hero(m) + how(m) + bandsec(m) + also(m) + footer(m, False)

    boards = [('HB-Page-Desktop', False, 1280, 3400, 'Website · direction B, desktop, light'), ('HB-Page-Mobile', True, 390, 4300, 'Website · direction B, mobile, light')]
    import re
    for name, m, w, h, title in boards:
        write(f'{name}.dc.html', doc('x', title, body(m), w, h, 'light'))
        dark_wrapper(f'D-{name}.dc.html', name, title.replace('light', 'dark'), w, h)
        flat = re.sub(r'<dc-import name="Brand"[^>]*></dc-import>', g['brand'](True), body(m))
        for th in ('light', 'dark'):
            write(f'{name.lower()}-{th}.html', doc('x', title, flat, w, h, th, preview=True), preview=True)
