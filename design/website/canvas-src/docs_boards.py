"""Guide (docs) boards for the website. Called from gen.py's main block: docs_boards.build(globals()).
Content: notes/website-guide.md (research agent). The design is task-first, not a sitemap:
the Guide home starts with the three-stage path and a "I want to..." grid; every page sits on a
path tracker; steps are cards with their commands inside; the next step is a big card."""

CSS = '''
.w .gd{display:flex;gap:56px;align-items:flex-start}
.w .side{width:232px;flex:none;position:sticky;top:24px}
.w .side .grp{font-size:12px;font-weight:500;letter-spacing:.06em;text-transform:uppercase;color:var(--ink2);margin:26px 0 6px;padding:0 12px}
.w .side .grp:first-child{margin-top:0}
.w .side a{display:flex;align-items:center;gap:10px;height:36px;padding:0 12px;border-radius:10px;font-size:15px;color:var(--ink2);transition:background .15s,color .15s}
.w .side a:hover{background:var(--sunk);color:var(--ink)}
.w .side a.on{background:var(--ink);color:var(--bg);font-weight:500}
.w .side a i{font-style:normal;width:20px;height:20px;border-radius:50%;border:1px solid var(--line);font-size:11px;display:inline-flex;align-items:center;justify-content:center;flex:none;font-family:'GT America Mono',ui-monospace,monospace}
.w .side a.on i{border-color:transparent;background:var(--blue);color:#fff}
.w .doc{flex:1;min-width:0;max-width:660px}
.w .doc h1{font-size:56px;line-height:1.04;font-weight:500;letter-spacing:-.04em}
.w .doc .lead{font-size:20px;line-height:1.5;color:var(--ink2);margin-top:14px}
.w .doc h2{font-size:28px;line-height:1.15;font-weight:500;letter-spacing:-.025em;margin:56px 0 16px}
.w .doc p.b{font-size:16.5px;line-height:1.65;color:var(--ink2);margin:0 0 14px}
.w .doc code,.w .code code{font-family:'GT America Mono',ui-monospace,monospace;font-size:.88em}
.w .doc p code,.w .doc li code,.w .chip code,.w .tbl code,.w .sc code{background:var(--bg);border-radius:6px;padding:2px 6px;color:var(--ink)}
.w .path{display:flex;align-items:center;gap:14px;margin-bottom:22px;font-size:13.5px;color:var(--ink2);font-weight:500}
.w .path .seg{display:flex;gap:4px}.w .path .seg i{width:28px;height:5px;border-radius:3px;background:var(--line);display:block}.w .path .seg i.d{background:var(--blue)}
.w .chips{display:flex;gap:10px;flex-wrap:wrap;margin-top:18px}
.w .chip{display:inline-flex;align-items:center;gap:8px;min-height:40px;padding:8px 16px;border-radius:20px;background:var(--sunk);font-size:15px;color:var(--ink)}
.w .chip svg{flex:none;color:var(--ink2)}
.w .sc{background:var(--sunk);border:1px solid var(--line);border-radius:26px;padding:24px 26px 26px;margin-bottom:14px}
.w .sc .hd{display:flex;align-items:center;gap:14px;margin-bottom:10px}
.w .sc .n{width:34px;height:34px;border-radius:50%;background:var(--ink);color:var(--bg);display:flex;align-items:center;justify-content:center;font-size:15px;font-weight:500;flex:none}
.w .sc h3{font-size:21px;font-weight:500;letter-spacing:-.015em;margin:0}
.w .sc p{font-size:16px;line-height:1.6;color:var(--ink2);margin:0 0 4px}
.w .cmd{display:flex;align-items:center;gap:12px;background:#141413;color:#EDEDE8;border-radius:14px;padding:0 8px 0 18px;min-height:52px;margin-top:12px;font-family:'GT America Mono',ui-monospace,monospace;font-size:14px}
.w .cmd .p{color:#6FE0A0}.w .cmd .t{flex:1;min-width:0;padding:14px 0;line-height:1.55;overflow-wrap:anywhere}
.w .cmd .cp{height:34px;padding:0 12px;border-radius:17px;background:#2A2A27;color:#CFCFC9;font-family:'GT America',system-ui,sans-serif;font-size:13px;font-weight:500;display:flex;align-items:center;flex:none}
.w .shot{border-radius:22px;background:var(--panel);margin-top:16px;padding:22px 22px 0;overflow:hidden}
.w .term{background:#141413;color:#E9E9E4;border-radius:16px 16px 0 0;padding:16px 20px 24px;font-family:'GT America Mono',ui-monospace,monospace;font-size:13.5px;line-height:1.75}
.w .term .dots{display:flex;gap:7px;margin-bottom:10px}.w .term .dots i{width:11px;height:11px;border-radius:50%;background:#3a3a37;display:block}
.w .term .g{color:#6FE0A0}.w .term .m{color:#8C8C86}
.w .call{display:grid;grid-template-columns:28px 1fr;gap:12px;border-radius:20px;padding:18px 20px;margin:18px 0;font-size:15.5px;line-height:1.6;color:var(--ink)}
.w .call svg{margin-top:2px}
.w .call b{font-weight:500}
.w .call.warn{background:var(--amberTint)}.w .call.warn svg{color:var(--amberText)}
.w .call.note{background:var(--blueTint)}.w .call.note svg{color:var(--blueText)}
.w .call.tip{background:var(--greenTint)}.w .call.tip svg{color:var(--greenText)}
.w .todo{display:inline-flex;align-items:center;height:22px;padding:0 8px;border-radius:6px;border:1px dashed var(--amberText);color:var(--amberText);font-size:11.5px;font-weight:500;letter-spacing:.06em;margin-right:8px;vertical-align:1px}
.w .draft{border:1px dashed var(--amberText);border-radius:14px;padding:12px 14px;margin-top:12px;font-size:14.5px;color:var(--ink2);line-height:1.5}
.w .tbl{border:1px solid var(--line);border-radius:22px;overflow:hidden}
.w .tbl .r{display:grid;grid-template-columns:200px 100px 1fr;gap:16px;padding:14px 20px;border-top:1px solid var(--line);font-size:15px;color:var(--ink2);align-items:baseline}
.w .tbl .r:first-child{border-top:0;background:var(--sunk);font-size:13px;font-weight:500}
.w .tbl .r span:first-child{color:var(--ink)}
.w .rail{width:196px;flex:none;position:sticky;top:24px;font-size:14px}
.w .rail .h{font-size:12px;font-weight:500;letter-spacing:.06em;text-transform:uppercase;color:var(--ink2);margin-bottom:12px}
.w .rail .trk{display:flex;flex-direction:column;gap:0;margin-bottom:30px}
.w .rail .trk a{display:flex;align-items:center;gap:12px;color:var(--ink2);height:34px;position:relative}
.w .rail .trk a:before{content:"";position:absolute;left:8px;top:-17px;height:34px;width:2px;background:var(--line)}
.w .rail .trk a:first-child:before{display:none}
.w .rail .trk i{width:18px;height:18px;border-radius:50%;border:2px solid var(--line);background:var(--bg);flex:none;position:relative;z-index:1}
.w .rail .trk a.d i{background:var(--blue);border-color:var(--blue)}
.w .rail .trk a.on{color:var(--ink);font-weight:500}.w .rail .trk a.on i{border-color:var(--blue)}
.w .rail .oth a{display:block;padding:5px 0;color:var(--ink2)}.w .rail .oth a.on{color:var(--ink);font-weight:500}
.w .next{display:flex;align-items:center;justify-content:space-between;gap:20px;border-radius:28px;background:var(--panel);padding:28px 30px;margin-top:48px}
.w .next small{font-size:13px;color:var(--ink2);display:block;margin-bottom:4px}.w .next b{font-size:24px;font-weight:500;letter-spacing:-.02em;display:block}
.w .stuck{display:flex;align-items:center;justify-content:space-between;gap:16px;margin-top:14px;padding:0 6px;font-size:15px;color:var(--ink2)}
.w .stuck a{color:var(--blueText);font-weight:500}
.w .stg{border-radius:30px;padding:28px 28px 0;display:flex;flex-direction:column;gap:8px;overflow:hidden;min-height:300px;position:relative}
.w .stg .n{font-family:'GT America Mono',ui-monospace,monospace;font-size:13px;color:var(--ink2)}
.w .stg h3{font-size:26px;font-weight:500;letter-spacing:-.02em;margin:0;line-height:1.15}
.w .stg p{font-size:15.5px;line-height:1.5;color:var(--ink2);margin:0;max-width:240px}
.w .task{display:flex;align-items:center;justify-content:space-between;gap:12px;min-height:64px;padding:12px 20px;border-radius:20px;border:1px solid var(--line);font-size:16px;font-weight:500;transition:background .15s}
.w .task:hover{background:var(--sunk)}.w .task span{color:var(--ink2);font-weight:400;font-size:14px;display:block}
.w .idx{display:grid;gap:28px}.w .idx h4{font-size:12px;font-weight:500;letter-spacing:.06em;text-transform:uppercase;color:var(--ink2);margin:0 0 8px}
.w .idx a{display:block;padding:5px 0;font-size:15px;color:var(--ink)}.w .idx a:hover{color:var(--blueText)}
.w .menubtn{display:inline-flex;align-items:center;gap:8px;height:40px;padding:0 16px;border-radius:20px;background:var(--sunk);font-size:14px;font-weight:500}
'''

GROUPS = [
    ('Getting started', [('What Shop is', 'what'), ('Install the server', 'install'), ('Connect the iPhone app', 'connect')]),
    ('Using Shop', [('Add a receipt', 'add'), ('Review and fix a receipt', 'review'), ('Products, categories and shops', 'products'), ('Analytics', 'analytics')]),
    ('Self-hosting', [('Update, back up and restore', 'backup'), ('Ask an assistant (MCP)', 'mcp')]),
    ('Help', [('Limits and privacy', 'limits'), ('Troubleshooting', 'trouble')]),
]
IC = {
    'warn': '<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 3 2.5 20h19L12 3Z"/><path d="M12 10v4M12 17.2v.1"/></svg>',
    'box': '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 8l9-5 9 5v8l-9 5-9-5V8Z"/><path d="M3 8l9 5 9-5M12 13v8"/></svg>',
    'term': '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="3"/><path d="m7 10 3 2-3 2M13 15h4"/></svg>',
    'mac': '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/></svg>',
    'arrow': '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 12h14M13 6l6 6-6 6"/></svg>',
}
TASKS = [('Fix a receipt that does not add up', 'Review and fix a receipt'), ('Use the app away from home', 'Connect the iPhone app'), ('Back up to my NAS', 'Update, back up and restore'),
         ('Ask an assistant about my spending', 'Ask an assistant (MCP)'), ('Restore after a failure', 'Update, back up and restore'), ('Something is not working', 'Troubleshooting')]


def build(g):
    import re
    g['CSS'] += CSS
    for t, v in (('light', dict(amberTint='#FFF3DF', greenTint='#E3F6EC', purpleTint='#F0EAFE')), ('dark', dict(amberTint='#2B2112', greenTint='#12281C', purpleTint='#221A36'))):
        g['TOK'][t].update(v)
    g['VARS'] += ['amberTint', 'greenTint', 'purpleTint']
    GH, brand, footer, doc, write, dark_wrapper, dev = g['GH'], g['brand'], g['footer'], g['doc'], g['write'], g['dark_wrapper'], g['dev']

    def header(m, menu=False):
        pad = '0 20px' if m else '0 80px'
        if m:
            ic = '<path d="M6 6l12 12M18 6L6 18"/>' if menu else '<path d="M4 8h16M4 16h16"/>'
            right = f'<span class="menubtn"><svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">{ic}</svg>{"Close" if menu else "Menu"}</span>'
        else:
            right = f'<a class="nl" href="#">Features</a><a class="nl" href="#" style="color:var(--blueText)">Guide</a><a class="btn sm" href="#">{GH.replace("20","18")}GitHub</a>'
        return f'<header class="nav" style="padding:{pad};{"height:68px" if m else ""}">{brand(False)}<nav class="links">{right}</nav></header>'

    def sidebar(cur, m=False):
        out = ''
        for gname, items in GROUPS:
            out += f'<div class="grp">{gname}</div>'
            for i, (n, k) in enumerate(items):
                badge = f'<i>{i+1}</i>' if gname == 'Getting started' else ''
                out += f'<a href="#" class="{"on" if k==cur else ""}">{badge}{n}</a>'
        return f'<aside class="side" style="{"position:static;width:auto;padding:8px 8px 40px" if m else ""}">{out}</aside>'

    def cmd(t):
        return f'<div class="cmd"><span class="p">$</span><span class="t">{t}</span><span class="cp">Copy</span></div>'

    def terminal():
        return '<div class="shot"><div class="term"><div class="dots"><i></i><i></i><i></i></div><span class="m">$</span> make setup<br><span class="m">building images…</span><br><span class="g">✓</span> api healthy<br><span class="g">✓</span> ocr models ready<br><br>App pairing password (shown once):<br><span class="g">kestrel-lantern-4182</span></div></div>'

    def stepcard(n, title, body, extra=''):
        return f'<div class="sc"><div class="hd"><span class="n">{n}</span><h3>{title}</h3></div>{body}{extra}</div>'

    def pathbar(done, total=3):
        segs = ''.join(f'<i class="{"d" if i < done else ""}"></i>' for i in range(total))
        return f'<div class="path"><span class="seg">{segs}</span>Getting started · step {done} of {total}</div>'

    def rail():
        trk = '<div class="h">Getting started</div><div class="trk"><a href="#" class="d"><i></i>What Shop is</a><a href="#" class="on"><i></i>Install the server</a><a href="#"><i></i>Connect the iPhone app</a></div>'
        oth = '<div class="h">On this page</div><div class="oth"><a href="#">You need</a><a href="#" class="on">Steps</a><a href="#">Settings</a></div>'
        return f'<nav class="rail">{trk}{oth}</nav>'

    def install_doc(m):
        chips = f'<div class="chips"><span class="chip">{IC["box"]}Docker, about 4 GB free</span><span class="chip">{IC["term"]}<span><code>git</code> and <code>make</code></span></span><span class="chip">{IC["mac"]}Built on a Mac mini</span></div>'
        todo1 = '<div class="draft"><span class="todo">TODO</span>Confirm Linux and Intel/ARM support before saying so.</div>'
        s1 = stepcard(1, 'Get the code', '<p>Clone the repository.</p>' + cmd('git clone &lt;repo-url&gt; shop &amp;&amp; cd shop'), '<div class="draft"><span class="todo">TODO</span>Public repository URL.</div>')
        s2 = stepcard(2, 'Choose your settings', '<p>Optional. Add a logo.dev key if you want real shop logos; without it the app shows initials.</p>' + cmd('cp deploy/.env.example deploy/.env'))
        s3 = stepcard(3, 'Build and start', '<p>The first build downloads the OCR models and takes several minutes. When it ends it prints the pairing password <b style="color:var(--ink);font-weight:500">once</b>. Copy it.</p>' + cmd('make setup'), terminal())
        s4 = stepcard(4, 'Check that it is healthy', '<p>Every line should say <code>ok</code>.</p>' + cmd('curl localhost:8080/healthz'))
        warn = f'<div class="call warn">{IC["warn"]}<div><b>Do not expose the server to the internet.</b> It speaks plain HTTP. Use your home network or a VPN; the next page shows how.</div></div>'
        rows = [('SHOP_PORT', '8080', 'Port for the API and MCP'), ('SHOP_BACKUP_DIR', 'backups/', 'Where nightly backups go (point it at a NAS)'), ('BACKUP_AT', '03:30', 'Daily backup time'), ('BACKUP_KEEP_DAYS', '30', 'How long snapshots are kept'), ('LOGO_DEV_TOKEN', 'none', 'Shop logos'), ('AUTO_PRODUCTS', 'on', 'Create products when you confirm a receipt')]
        if m:
            tb = '<div class="tbl">' + ''.join(f'<div style="padding:14px 18px;border-top:{"0" if i==0 else "1px solid var(--line)"}"><code>{a}</code> <span style="color:var(--ink3)">· {b}</span><div style="color:var(--ink2);font-size:15px;margin-top:4px">{c}</div></div>' for i, (a, b, c) in enumerate(rows)) + '</div>'
        else:
            tb = '<div class="tbl"><div class="r"><span>Setting</span><span>Default</span><span>Meaning</span></div>' + ''.join(f'<div class="r"><span><code>{a}</code></span><span>{b}</span><span>{c}</span></div>' for a, b, c in rows) + '</div>'
        nxt = f'<div class="next" style="{"flex-direction:column;align-items:flex-start" if m else ""}"><div><small>Next, step 3 of 3</small><b>Connect the iPhone app</b></div><a class="btn" href="#">Continue{IC["arrow"]}</a></div><div class="stuck"><span>Something went wrong?</span><a href="#">Troubleshooting</a></div>'
        h1 = 'font-size:40px' if m else ''
        return f'''<article class="doc">{pathbar(2)}<h1 style="{h1}">Install the server</h1><p class="lead" style="{"font-size:17px" if m else ""}">Start the Docker stack and get the pairing password.</p>
<h2 id="need" style="margin-top:36px;font-size:22px">You need</h2>{chips}{todo1}
<h2 id="steps">Steps</h2>{s1}{s2}{s3}{s4}{warn}
<h2 id="settings">Settings</h2><p class="b">Set these in <code>deploy/.env</code>.</p>{tb}{nxt}</article>'''

    def home_doc(m):
        stage = [('1', 'Install the server', 'Docker on a machine at home. The first build runs by itself.', 'var(--panel)', None),
                 ('2', 'Connect the app', 'Enter the address and pairing password in the iPhone app.', 'var(--greenTint)', None),
                 ('3', 'Add your first receipt', 'Photograph it, fix anything flagged, read your numbers.', 'var(--amberTint)', 'receipt')]
        cards = ''
        for n, t, d, bg, shot in stage:
            ph = f'<div style="position:absolute;right:20px;bottom:0;transform:translateY(38%)">{dev(shot,138)}</div>' if shot and not m else ''
            cards += f'<a class="stg" href="#" style="background:{bg}"><span class="n">0{n}</span><h3>{t}</h3><p{" style=max-width:165px" if shot and not m else ""}>{d}</p>{ph}</a>'
        grid = f'display:grid;grid-template-columns:repeat({1 if m else 3},minmax(0,1fr));gap:{12 if m else 14}px;margin-top:36px'
        tasks = ''.join(f'<a class="task" href="#"><div>{a}<span>{b}</span></div>{IC["arrow"]}</a>' for a, b in TASKS)
        tgrid = f'display:grid;grid-template-columns:repeat({1 if m else 2},minmax(0,1fr));gap:10px'
        idx = '<div class="idx" style="grid-template-columns:repeat(%d,minmax(0,1fr))">' % (2 if m else 4) + ''.join(f'<div><h4>{gn}</h4>' + ''.join(f'<a href="#">{n}</a>' for n, k in its) + '</div>' for gn, its in GROUPS) + '</div>'
        h1 = 'font-size:44px' if m else 'font-size:72px'
        return f'''<article class="doc" style="max-width:none"><h1 style="{h1}">Guide</h1><p class="lead" style="max-width:620px;{"font-size:17px" if m else ""}">Run Shop yourself in three stages, then keep it running. Pick up where you are.</p>
<div style="{grid}">{cards}</div>
<h2>I want to…</h2><div style="{tgrid}">{tasks}</div>
<h2>All pages</h2>{idx}</article>'''

    def page(kind, m):
        if kind == 'menu':
            return header(m, True) + f'<div style="padding:8px 12px">{sidebar("install", True)}</div>'
        if kind == 'home':
            inner = f'<div style="padding:{"16px 20px 56px" if m else "24px 80px 96px"}">{home_doc(m)}</div>'
        elif m:
            inner = f'<div style="padding:12px 20px 56px">{install_doc(True)}</div>'
        else:
            inner = f'<div class="gd" style="padding:24px 80px 96px"><div style="width:232px;flex:none;align-self:stretch">{sidebar("install")}</div>{install_doc(False)}{rail()}</div>'
        return header(m) + inner + footer(m, False)

    def notes_board():
        ILL = [('What Shop is', 'home screen'), ('Install the server', 'terminal: make setup, healthz (new capture)'), ('Connect the iPhone app', 'Connect your server screen (new capture)'), ('Add a receipt', 'home + scan and corner-adjust (new capture)'),
               ('Review and fix a receipt', 'receipt screen'), ('Products, categories and shops', 'products and product screens'), ('Analytics', 'analytics screen'), ('Update, back up and restore', 'terminal + Settings backup status (new capture)'),
               ('Ask an assistant (MCP)', 'chat with demo data (new capture)'), ('Limits and privacy', 'data-flow diagram (new, not an app screen)'), ('Troubleshooting', 'healthz output (new capture)')]
        il = ''.join(f'<div class="r" style="grid-template-columns:260px 1fr"><span>{a}</span><span>{b}</span></div>' for a, b in ILL)
        items = ['Task first, not a sitemap. The Guide home opens with the three stages of getting running (install, connect, first receipt) and an "I want to…" grid for people who already run it. The full page list is last.',
                 'Every Getting-started page shows where you are: a three-segment bar above the title, and a vertical tracker in the right rail. Other pages show "On this page" there. Below 1100 px the rail goes; below 800 px the sidebar becomes a Menu button.',
                 'Steps are cards, not a numbered list: a dark number, a short title, a sentence, and the command in a dark bar with a Copy pill. Output captures sit in a blue panel like the home page hero.',
                 'Settings are a table inside a rounded card (stacked rows on mobile). Callouts carry an icon and one bold line: Warning (amber), Note (blue), Tip (green).',
                 'Each page ends with one big "Next" card and a quiet "Something went wrong? Troubleshooting" line, because self-hosting is where people get stuck.',
                 'Sidebar: group labels in small caps, 36 px rows, current page filled dark; Getting started rows carry 1, 2, 3.',
                 'TODO: dashed amber outline with a small TODO tag. Draft only, so nothing unverified looks finished. About 20 exist in the draft; strip them before publishing.',
                 'Not drawn: search, "was this useful" row, language switch. Add search once there are more than about 15 pages.']
        li = ''.join(f'<li style="margin-bottom:10px">{t}</li>' for t in items)
        return f'''<section style="padding:56px 80px;border-bottom:1px solid var(--line)"><div style="font-size:13px;color:var(--blue);font-weight:500;margin-bottom:8px">Guide · design notes</div><h1 class="h1" style="font-size:48px;line-height:1.05;max-width:900px">A guide built around getting Shop running.</h1><p style="color:var(--ink2);font-size:18px;max-width:760px;margin-top:14px">Content is the research agent's draft in notes/website-guide.md: 11 pages in 4 groups. Boards: Guide home, one full page (Install the server), and the mobile menu, each in light and dark.</p></section>
<section style="padding:48px 80px;border-bottom:1px solid var(--line)"><h2 class="h2" style="font-size:26px;margin-bottom:16px">What is different from a standard docs site</h2><ul style="margin:0;padding-left:20px;font-size:15.5px;color:var(--ink2);max-width:900px">{li}</ul></section>
<section style="padding:48px 80px"><h2 class="h2" style="font-size:26px;margin-bottom:8px">Illustrations per page</h2><p style="font-size:14px;color:var(--ink2);margin-bottom:16px">Real app captures only, from the mock server with invented shops (never real receipts). Five pages need captures that do not exist yet.</p><div class="tbl" style="max-width:820px">{il}</div></section>'''

    boards = [('GD-Home-Desktop', 'home', False, 1280, 1530, 'Guide · home, desktop, light'), ('GD-Install-Desktop', 'install', False, 1280, 2940, 'Guide · Install the server, desktop, light'),
              ('GD-Install-Mobile', 'install', True, 390, 3360, 'Guide · Install the server, mobile, light'), ('GD-Menu-Mobile', 'menu', True, 390, 760, 'Guide · menu, mobile, light'),
              ('GD-Home-Mobile', 'home', True, 390, 2470, 'Guide · home, mobile, light')]
    for name, kind, m, w, h, title in boards:
        write(f'{name}.dc.html', doc('x', title, page(kind, m), w, h, 'light'))
        dark_wrapper(f'D-{name}.dc.html', name, title.replace('light', 'dark'), w, h)
        flat = re.sub(r'<dc-import name="Brand"[^>]*></dc-import>', brand(True), page(kind, m))
        for th in ('light', 'dark'):
            write(f'{name.lower()}-{th}.html', doc('x', title, flat, w, h, th, preview=True), preview=True)
    write('GD-Notes.dc.html', doc('x', 'Guide design notes', notes_board(), 1280, 1700, 'light'))
