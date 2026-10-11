"""Guide (docs) boards for the website. Called from gen.py's main block: docs_boards.build(globals()).
Content comes from notes/website-guide.md (research agent). Layout: sticky left sidebar with small group labels, one centred reading column, numbered steps."""

CSS = '''
.w .dnav a.act{color:var(--ink)}
.w .gd{display:flex;gap:48px;align-items:flex-start}
.w .side{width:256px;flex:none;position:sticky;top:24px}
.w .side .grp{font-size:12px;font-weight:500;letter-spacing:.06em;text-transform:uppercase;color:var(--ink2);margin:26px 0 6px;padding:0 12px}
.w .side .grp:first-child{margin-top:0}
.w .side a{display:flex;align-items:center;height:36px;padding:0 12px;border-radius:10px;font-size:15px;color:var(--ink2);transition:background .15s,color .15s}
.w .side a:hover{background:var(--sunk);color:var(--ink)}
.w .side a.on{background:var(--sunk);color:var(--ink);font-weight:500}
.w .doc{flex:1;min-width:0;max-width:620px}
.w .doc h1{font-size:52px;line-height:1.05;font-weight:500;letter-spacing:-.04em}
.w .doc .lead{font-size:19px;line-height:1.5;color:var(--ink2);margin-top:14px}
.w .doc h2{font-size:26px;line-height:1.2;font-weight:500;letter-spacing:-.025em;margin:48px 0 14px}
.w .doc p.b{font-size:16.5px;line-height:1.65;color:var(--ink2);margin:0 0 14px}
.w .doc p.b b{color:var(--ink);font-weight:500}
.w .doc code{font-family:'GT America Mono',ui-monospace,monospace;font-size:.88em;background:var(--sunk);border-radius:6px;padding:2px 6px;color:var(--ink)}
.w .steps{list-style:none;margin:0;padding:0;display:flex;flex-direction:column;gap:22px}
.w .steps>li{display:grid;grid-template-columns:34px 1fr;gap:0;font-size:16.5px;line-height:1.6;color:var(--ink)}
.w .steps>li>i{font-style:normal;font-family:'GT America Mono',ui-monospace,monospace;font-size:14px;color:var(--ink3);padding-top:3px}
.w .steps .sub{color:var(--ink2);margin-top:6px}
.w .letters{list-style:none;margin:0;padding:0;display:flex;flex-direction:column;gap:8px;font-size:16.5px}
.w .letters li{display:grid;grid-template-columns:28px 1fr}
.w .letters i{font-style:normal;color:var(--ink3);font-family:'GT America Mono',ui-monospace,monospace;font-size:14px;padding-top:2px}
.w .code{position:relative;background:var(--sunk);border:1px solid var(--line);border-radius:14px;padding:16px 76px 16px 18px;margin-top:12px;font-family:'GT America Mono',ui-monospace,monospace;font-size:14px;line-height:1.7;color:var(--ink);overflow:hidden}
.w .code .cp{position:absolute;right:10px;top:10px;height:28px;padding:0 10px;border-radius:14px;background:var(--bg);border:1px solid var(--line);font-family:'GT America',system-ui,sans-serif;font-size:12.5px;font-weight:500;color:var(--ink2);display:flex;align-items:center}
.w .code .c{color:var(--ink3)}
.w .call{border-radius:16px;padding:16px 18px;margin:18px 0;font-size:15.5px;line-height:1.6;color:var(--ink)}
.w .call b.t{display:block;font-size:12px;letter-spacing:.06em;text-transform:uppercase;font-weight:500;margin-bottom:4px}
.w .call.note{background:var(--blueTint)}.w .call.note b.t{color:var(--blueText)}
.w .call.warn{background:var(--amberTint)}.w .call.warn b.t{color:var(--amberText)}
.w .call.tip{background:var(--greenTint)}.w .call.tip b.t{color:var(--greenText)}
.w .todo{display:inline-flex;align-items:center;height:22px;padding:0 8px;border-radius:6px;border:1px dashed var(--amberText);color:var(--amberText);font-size:11.5px;font-weight:500;letter-spacing:.06em;margin-right:6px;vertical-align:1px}
.w .call.draft{background:transparent;border:1px dashed var(--amberText)}
.w table.tb{width:100%;border-collapse:collapse;font-size:15px;margin-top:6px}
.w table.tb th{text-align:left;font-weight:500;color:var(--ink2);font-size:13px;padding:10px 12px 10px 0;border-bottom:1px solid var(--line)}
.w table.tb td{padding:12px 12px 12px 0;border-bottom:1px solid var(--line);color:var(--ink2);vertical-align:top}
.w table.tb td:first-child{color:var(--ink)}
.w .toc{width:180px;flex:none;position:sticky;top:24px;font-size:14px}
.w .toc .h{font-size:12px;font-weight:500;letter-spacing:.06em;text-transform:uppercase;color:var(--ink2);margin-bottom:10px}
.w .toc a{display:block;padding:6px 0 6px 14px;border-left:1px solid var(--line);color:var(--ink2)}
.w .toc a.on{border-left:2px solid var(--blue);padding-left:13px;color:var(--ink);font-weight:500}
.w .pn{display:grid;grid-template-columns:1fr 1fr;gap:12px;margin-top:56px}
.w .pn a{display:flex;flex-direction:column;gap:4px;padding:18px 20px;border-radius:18px;border:1px solid var(--line);transition:background .15s}
.w .pn a:hover{background:var(--sunk)}
.w .pn small{font-size:13px;color:var(--ink2)}.w .pn span{font-size:17px;font-weight:500}
.w .gc{border-radius:28px;padding:28px 28px 24px;display:flex;flex-direction:column;gap:14px}
.w .gc h3{font-size:26px;font-weight:500;letter-spacing:-.02em;margin:0}
.w .gc p{font-size:15.5px;line-height:1.5;color:var(--ink2);margin:0}
.w .gc ul{list-style:none;margin:6px 0 0;padding:0;display:flex;flex-direction:column;gap:2px}
.w .gc li a{display:flex;align-items:center;justify-content:space-between;height:38px;border-top:1px solid color-mix(in srgb,var(--ink) 9%,transparent);font-size:15.5px}
.w .gc li a span{color:var(--ink2)}
.w .term{background:#141413;color:#E9E9E4;border-radius:20px;padding:18px 20px 22px;font-family:'GT America Mono',ui-monospace,monospace;font-size:13.5px;line-height:1.75;margin-top:16px;box-shadow:0 0 0 1px color-mix(in srgb,var(--ink) 10%,transparent)}
.w .term .dots{display:flex;gap:7px;margin-bottom:12px}.w .term .dots i{width:11px;height:11px;border-radius:50%;background:#3a3a37;display:block}
.w .term .g{color:#6FE0A0}.w .term .m{color:#8C8C86}
.w .ill{border-radius:28px;background:var(--panel);overflow:hidden;position:relative;margin-top:20px}
.w .menubtn{display:inline-flex;align-items:center;gap:8px;height:40px;padding:0 16px;border-radius:20px;background:var(--sunk);font-size:14px;font-weight:500}
'''

GROUPS = [
    ('Getting started', [('What Shop is', 'what'), ('Install the server', 'install'), ('Connect the iPhone app', 'connect')]),
    ('Using Shop', [('Add a receipt', 'add'), ('Review and fix a receipt', 'review'), ('Products, categories and shops', 'products'), ('Analytics', 'analytics')]),
    ('Self-hosting', [('Update, back up and restore', 'backup'), ('Ask an assistant (MCP)', 'mcp')]),
    ('Help', [('Limits and privacy', 'limits'), ('Troubleshooting', 'trouble')]),
]
SUM = {'what': 'A grocery receipt tracker you run yourself.', 'install': 'Start the Docker stack and get the pairing password.', 'connect': 'Pair your phone with your server.',
       'add': 'Photograph a receipt or import a photo.', 'review': 'Fix what is wrong, then confirm.', 'products': 'Products are made for you; you decide the uncertain ones.',
       'analytics': 'What you spend, by time, shop and category.', 'backup': 'Keep the server current and your data safe.', 'mcp': 'Let an AI assistant read your spending.',
       'limits': 'What it does not do, and what leaves your machine.', 'trouble': 'Quick checks for the usual problems.'}
TINTS = ['var(--blueTint)', 'var(--greenTint)', 'var(--amberTint)', 'var(--purpleTint)']


def build(g):
    g['CSS'] += CSS
    for t, v in (('light', dict(amberTint='#FFF3DF', greenTint='#E3F6EC', purpleTint='#F0EAFE')), ('dark', dict(amberTint='#2B2112', greenTint='#12281C', purpleTint='#221A36'))):
        g['TOK'][t].update(v)
    g['VARS'] += ['amberTint', 'greenTint', 'purpleTint']
    GH, brand, footer, doc, write, dark_wrapper, dev = g['GH'], g['brand'], g['footer'], g['doc'], g['write'], g['dark_wrapper'], g['dev']

    def header(m, pre, menu=False):
        pad = '0 20px' if m else '0 80px'
        if m:
            right = '<span class="menubtn"><svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M4 8h16M4 16h16"/></svg>Menu</span>' if not menu else '<span class="menubtn"><svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>Close</span>'
        else:
            right = f'<a class="nl" href="#">Features</a><a class="nl hover" href="#" style="color:var(--blueText)">Guide</a><a class="btn sm" href="https://github.com/sunday-studio/shop">{GH.replace("20","18")}GitHub</a>'
        return f'<header class="nav" style="padding:{pad};{"height:68px" if m else ""}">{brand(pre)}<nav class="links">{right}</nav></header>'

    def sidebar(cur, m=False):
        out = ''
        for gname, items in GROUPS:
            out += f'<div class="grp">{gname}</div>' + ''.join(f'<a href="#" class="{"on" if k==cur else ""}">{n}</a>' for n, k in items)
        return f'<aside class="side" style="{"position:static;width:auto;padding:8px 8px 40px" if m else ""}">{out}</aside>'

    def code(lines, cp=True):
        body = '<br>'.join(lines)
        return f'<div class="code">{"<span class=cp>Copy</span>" if cp else ""}{body}</div>'

    def prevnext(p, n, m=False):
        return f'<div class="pn" style="{"grid-template-columns:1fr" if m else ""}"><a href="#"><small>← Previous</small><span>{p}</span></a><a href="#" style="text-align:right;{"" if not m else "text-align:left"}"><small>Next →</small><span>{n}</span></a></div>'

    def toc(items, on=0):
        return '<nav class="toc"><div class="h">On this page</div>' + ''.join(f'<a href="#" class="{"on" if i==on else ""}">{t}</a>' for i, t in enumerate(items)) + '</nav>'

    def terminal():
        return '''<div class="term"><div class="dots"><i></i><i></i><i></i></div><span class="m">$</span> make setup<br><span class="m">building images…</span><br><span class="g">✓</span> api healthy<br><span class="g">✓</span> ocr models ready<br><br>App pairing password (shown once):<br><span class="g">kestrel-lantern-4182</span></div>'''

    def install_doc(m):
        s = 15 if m else 16.5
        steps = f'''<ol class="steps">
<li><i>01</i><div>Get the code.{code(['git clone &lt;repo-url&gt; shop &amp;&amp; cd shop'])}<div class="call draft" style="margin:12px 0 0"><span class="todo">TODO</span>Public repository URL.</div></div></li>
<li><i>02</i><div>Optional: copy the settings file. Add a logo.dev key if you want real shop logos; without it the app shows initials.{code(['cp deploy/.env.example deploy/.env'])}</div></li>
<li><i>03</i><div>Build and start.{code(['make setup'])}<div class="sub">The first build downloads the OCR models and takes several minutes. When it ends it prints the <b style="color:var(--ink);font-weight:500">pairing password once</b>. Copy it.</div>{terminal()}</div></li>
<li><i>04</i><div>Check that everything is healthy.{code(['curl localhost:8080/healthz'])}<div class="sub">Every check should say <code>ok</code>.</div></div></li></ol>'''
        rows = [('SHOP_PORT', '8080', 'Port for the API and MCP'), ('SHOP_BACKUP_DIR', 'backups/', 'Where nightly backups go (point it at a NAS)'), ('BACKUP_AT', '03:30', 'Daily backup time'), ('BACKUP_KEEP_DAYS', '30', 'How long snapshots are kept'), ('LOGO_DEV_TOKEN', 'none', 'Shop logos'), ('AUTO_PRODUCTS', 'on', 'Create products when you confirm a receipt')]
        tb = '<table class="tb"><tr><th>Setting</th><th>Default</th><th>Meaning</th></tr>' + ''.join(f'<tr><td><code>{a}</code></td><td>{b}</td><td>{c}</td></tr>' for a, b, c in rows) + '</table>'
        if m:
            tb = '<div style="display:flex;flex-direction:column">' + ''.join(f'<div style="padding:14px 0;border-bottom:1px solid var(--line)"><code>{a}</code> <span style="color:var(--ink3)">· default {b}</span><div style="color:var(--ink2);font-size:15px;margin-top:4px">{c}</div></div>' for a, b, c in rows) + '</div>'
        h1 = 'font-size:38px' if m else ''
        return f'''<article class="doc" style="font-size:{s}px"><div style="font-size:13px;color:var(--blueText);font-weight:500;margin-bottom:12px">Getting started</div><h1 style="{h1}">Install the server</h1><p class="lead" style="{"font-size:17px" if m else ""}">Start the Docker stack and get the pairing password.</p>
<h2 id="need">You need</h2><ul class="letters"><li><i>A.</i><span>Docker, with about 4 GB of memory to spare</span></li><li><i>B.</i><span><code>git</code> and <code>make</code></span></li><li><i>C.</i><span>A Mac mini is what it was built on</span></li></ul>
<div class="call draft"><span class="todo">TODO</span>Confirm Linux and Intel/ARM support before saying so.</div>
<h2 id="steps">Steps</h2>{steps}
<div class="call warn"><b class="t">Warning</b>The server speaks plain HTTP. Do not expose it to the internet. Reach it over your home network or a VPN (see the next page).</div>
<h2 id="settings">Settings</h2><p class="b">Set these in <code>deploy/.env</code>.</p>{tb}
{prevnext('What Shop is','Connect the iPhone app',m)}</article>'''

    def home_doc(m):
        cards = ''
        for i, (gname, items) in enumerate(GROUPS):
            lis = ''.join(f'<li><a href="#">{n}<span>→</span></a></li>' for n, k in items)
            blurb = ['Install, pair, first receipt.', 'Scan, fix, organise, read your numbers.', 'Update, back up, restore, connect an assistant.', 'Limits, privacy and quick fixes.'][i]
            cards += f'<div class="gc" style="background:{TINTS[i]}"><h3>{gname}</h3><p>{blurb}</p><ul>{lis}</ul></div>'
        grid = f'display:grid;grid-template-columns:repeat({1 if m else 2},minmax(0,1fr));gap:{12 if m else 14}px;margin-top:32px'
        path = '<ol class="steps" style="margin-top:6px"><li><i>01</i><div>Install the server on a machine with Docker.</div></li><li><i>02</i><div>Pair the iPhone app with it.</div></li><li><i>03</i><div>Photograph receipts, review the ones that need it, read your numbers.</div></li></ol>'
        h1 = 'font-size:40px' if m else 'font-size:64px'
        return f'''<article class="doc" style="max-width:none"><h1 style="{h1}">Guide</h1><p class="lead" style="max-width:600px;{"font-size:17px" if m else ""}">Everything you need to run Shop yourself: install the server, pair the iPhone app, and read your numbers.</p><div style="{grid}">{cards}</div><h2>The path in short</h2>{path}</article>'''

    def page(kind, m, menu=False):
        pad = '0 20px' if m else '0 80px'
        if kind == 'home':
            inner = f'<div style="padding:{"16px 20px 48px" if m else "24px 80px 80px"};display:flex;gap:48px"><div style="{"" if m else "width:256px;flex:none"}">{"" if m else sidebar("home")}</div>{home_doc(m)}</div>'
            if m:
                inner = f'<div style="padding:16px 20px 48px">{home_doc(m)}</div>'
        elif kind == 'menu':
            inner = f'<div style="padding:8px 12px">{sidebar("install", True)}</div>'
            return header(m, False, True) + inner
        else:
            if m:
                inner = f'<div style="padding:12px 20px 56px">{install_doc(True)}</div>'
            else:
                inner = f'<div class="gd" style="padding:24px 80px 96px"><div style="width:256px;flex:none;align-self:stretch">{sidebar("install")}</div>{install_doc(False)}{toc(["You need","Steps","Settings"],1)}</div>'
        return header(m, False) + inner + footer(m, False)

    def pre(kind, m, menu=False):
        return page(kind, m, menu).replace('<dc-import name="Brand"', '<dc-import name="Brand"')

    def notes_board():
        ILL = [('What Shop is', 'home screen'), ('Install the server', 'terminal: make setup, healthz (new capture)'), ('Connect the iPhone app', 'Connect your server screen (new capture)'), ('Add a receipt', 'home + scan and corner-adjust (new capture)'),
               ('Review and fix a receipt', 'receipt screen'), ('Products, categories and shops', 'products and product screens'), ('Analytics', 'analytics screen'), ('Update, back up and restore', 'terminal + Settings backup status (new capture)'),
               ('Ask an assistant (MCP)', 'chat with demo data (new capture)'), ('Limits and privacy', 'data-flow diagram (new, not an app screen)'), ('Troubleshooting', 'healthz output (new capture)')]
        il = ''.join(f'<tr><td>{a}</td><td>{b}</td></tr>' for a, b in ILL)
        items = ['Layout: sticky sidebar (256), one reading column (620), sticky "On this page" (180). 80 px page margins, same plain header as the home page with "Guide" in blue. Below 1100 px the "On this page" column goes; below 800 px the sidebar becomes a Menu button that opens a full-screen list.',
                 'Sidebar: small caps group labels (ink2 for contrast), 36 px rows, current page on a sunk pill. Groups and 11 pages come from the research agent\'s list.',
                 'Page: blue "group" label, 52 px Medium title, 19 px lead, 26 px section titles. Body 16.5 / 1.65. Steps are numbered 01, 02 in mono grey (like Distill); requirements are lettered A, B, C.',
                 'Code: sunk block, 14 px GT America Mono, "Copy" pill top right, no syntax colours. Terminal captures are dark panels with a green tick.',
                 'Callouts: Note (blue tint), Warning (amber tint), Tip (green tint); small caps label, 15.5 px text.',
                 'TODO: dashed amber outline with a small TODO tag. Draft only, shown so nothing unverified looks finished. Strip them (or hide with one CSS rule) for the published site; about 20 exist in the draft.',
                 'Previous / next cards at the bottom. Home of the Guide shows the four groups as tinted cards with their pages listed, like Distill.',
                 'Optional later: search box above the sidebar and a "Was this useful?" row. Not drawn; the page is useful without them.']
        li = ''.join(f'<li style="margin-bottom:10px">{t}</li>' for t in items)
        return f'''<section style="padding:56px 80px;border-bottom:1px solid var(--line)"><div style="font-size:13px;color:var(--blue);font-weight:500;margin-bottom:8px">Guide · design notes</div><h1 class="h1" style="font-size:48px;line-height:1.05;max-width:900px">A docs page: sidebar, reading column, on this page.</h1><p style="color:var(--ink2);font-size:18px;max-width:760px;margin-top:14px">Content is the research agent's draft in notes/website-guide.md (11 pages, 4 groups). Boards: Guide home, one full page (Install the server) in light and dark on desktop and mobile, and the mobile menu.</p></section>
<section style="padding:48px 80px;border-bottom:1px solid var(--line)"><h2 class="h2" style="font-size:26px;margin-bottom:16px">Rules</h2><ul style="margin:0;padding-left:20px;font-size:15.5px;color:var(--ink2);max-width:900px">{li}</ul></section>
<section style="padding:48px 80px"><h2 class="h2" style="font-size:26px;margin-bottom:8px">Illustrations per page</h2><p style="font-size:14px;color:var(--ink2);margin-bottom:12px">Real app captures only with the mock server and invented shops (never real receipts). Five pages need captures that do not exist yet.</p><div class="w" style="font-size:15px"><table class="tb" style="max-width:820px"><tr><th>Page</th><th>Illustration</th></tr>{il}</table></div></section>'''

    boards = [('GD-Home-Desktop', 'home', False, 1280, 1360, 'Guide · home, desktop, light'), ('GD-Install-Desktop', 'install', False, 1280, 2540, 'Guide · Install the server, desktop, light'),
              ('GD-Install-Mobile', 'install', True, 390, 2960, 'Guide · Install the server, mobile, light'), ('GD-Menu-Mobile', 'menu', True, 390, 760, 'Guide · menu open, mobile, light'),
              ('GD-Home-Mobile', 'home', True, 390, 1910, 'Guide · home, mobile, light')]
    out = []
    for name, kind, m, w, h, title in boards:
        write(f'{name}.dc.html', doc('x', title, page(kind, m), w, h, 'light'))
        dark_wrapper(f'D-{name}.dc.html', name, title.replace('light', 'dark'), w, h)
        # flat previews for checking in a browser
        for th in ('light', 'dark'):
            write(f'{name.lower()}-{th}.html', doc('x', title, _flat(g, page, kind, m), w, h, th, preview=True), preview=True)
        out.append((name, title, w, h))
    write('GD-Notes.dc.html', doc('x', 'Guide design notes', notes_board(), 1280, 1700, 'light'))
    return out


def _flat(g, page, kind, m):
    # preview pages use the inline brand instead of the dc-import
    import re
    html = page(kind, m)
    html = re.sub(r'<dc-import name="Brand"[^>]*></dc-import>', g['brand'](True), html)
    return html
