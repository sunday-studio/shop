#!/usr/bin/env python3
"""Renders the guide (site/content/guide/NN-slug.md) into static pages site/guide/<slug>.html
and regenerates site/guide/index.html. Python 3 standard library only.

    python3 build.py                       # content/guide -> guide/
    python3 build.py --content DIR --out DIR

The landing page (index.html) is hand-maintained and is not touched.
Anything the renderer does not know makes the build fail with file:line and a message.
Markdown subset: see README.md ("Writing guide pages").
"""
import argparse, html, os, re, sys

HERE = os.path.dirname(os.path.abspath(__file__))
GITHUB = 'https://github.com/sunday-studio/shop'
GH18 = ('<svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M12 .5C5.65.5.5 5.65.5 12c0 5.08 3.29 9.39 7.86 10.91.58.1.79-.25.79-.56v-2c-3.2.7-3.87-1.37-3.87-1.37-.52-1.33-1.28-1.69-1.28-1.69-1.04-.71.08-.7.08-.7 1.15.08 1.76 1.18 1.76 1.18 1.03 1.76 2.69 1.25 3.35.96.1-.75.4-1.25.73-1.54-2.55-.29-5.24-1.28-5.24-5.69 0-1.26.45-2.28 1.18-3.09-.12-.29-.51-1.46.11-3.05 0 0 .97-.31 3.17 1.18a11 11 0 0 1 5.77 0c2.2-1.49 3.17-1.18 3.17-1.18.63 1.59.23 2.76.11 3.05.74.81 1.18 1.83 1.18 3.09 0 4.42-2.69 5.39-5.25 5.67.41.36.78 1.06.78 2.14v3.17c0 .31.21.67.8.56A11.5 11.5 0 0 0 23.5 12C23.5 5.65 18.35.5 12 .5Z"/></svg>')
MENU_ICON = ('<svg class="when-closed" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M4 8h16M4 16h16"/></svg>'
             '<svg class="when-open" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>')

BLURBS = {'Getting started': 'Install, pair, first receipt.', 'Using Shop': 'Scan, fix, organise, read your numbers.',
          'Self-hosting': 'Update, back up, restore, connect an assistant.', 'Help': 'Limits, privacy and quick fixes.'}
TINTS = ['var(--blueTint)', 'var(--greenTint)', 'var(--amberTint)', 'var(--purpleTint)']
SCREENS = {
    'home': 'Home screen in the app: the receipts inbox',
    'receipt': 'Receipt screen in the app: every line listed and checked against the printed total',
    'analytics': 'Analytics screen in the app: spending by month, shop and category',
    'products': 'Products screen in the app: a tidy list of products with categories and best price per unit',
    'product': 'Product screen in the app: the price per litre at each shop over time',
}
CALLOUTS = {'Warning': 'warn', 'Note': 'note', 'Tip': 'tip'}


class Fail(Exception):
    pass


def fail(src, line, msg):
    raise Fail(f'{src}:{line}: {msg}')


# ------------------------------------------------------------------ inline
def inline(text, ctx):
    src, line, slugs = ctx
    out = []
    for i, part in enumerate(re.split(r'(`[^`\n]+`)', text)):
        if i % 2:
            out.append('<code>' + html.escape(part[1:-1], quote=False) + '</code>')
            continue
        p = html.escape(part, quote=False)

        def link(m):
            label, url = m.group(1), html.unescape(m.group(2))
            if re.match(r'^https?://\S+$', url):
                attrs = f'href="{html.escape(url)}" rel="noopener"'
            elif re.match(r'^[a-z0-9-]+(#[a-z0-9-]+)?$', url):
                slug = url.split('#')[0]
                if slug not in slugs:
                    fail(src, line, f'link to unknown guide page "{slug}"')
                attrs = f'href="{url.replace(slug, slug + ".html", 1)}"'
            elif re.match(r'^#[a-z0-9-]+$', url):
                attrs = f'href="{url}"'
            else:
                fail(src, line, f'unsupported link target "{url}"')
            return f'<a class="link" {attrs}>{label}</a>'
        p = re.sub(r'\[([^\]]+)\]\(([^)\s]+)\)', link, p)
        p = re.sub(r'\*\*(.+?)\*\*', r'<b>\1</b>', p)
        p = re.sub(r'(?<![\*\w])\*(?!\s)([^*]+?)(?<!\s)\*(?![\*\w])', r'<em>\1</em>', p)
        if '**' in p or '](' in p:
            fail(src, line, 'unbalanced inline markup: ' + part[:60])
        out.append(p)
    s = ''.join(out)
    if '`' in re.sub(r'<code>.*?</code>', '', s):
        fail(src, line, 'unbalanced backtick: ' + text[:60])
    return s


# ------------------------------------------------------------------ block parser
def parse_blocks(lines, src, top=True):
    """lines: list of (lineno, text). Returns a list of block tuples."""
    blocks, i, n = [], 0, len(lines)
    lettered = False

    def indent_of(t): return len(t) - len(t.lstrip(' '))

    while i < n:
        ln, t = lines[i]
        if not t.strip():
            i += 1
            continue
        if t.startswith('\t'):
            fail(src, ln, 'tabs are not supported; indent with spaces')
        if t.startswith('## '):
            if not top:
                fail(src, ln, 'headings are only allowed at the top level')
            blocks.append(('h2', ln, t[3:].strip()))
            i += 1
        elif t.startswith('#'):
            fail(src, ln, 'only "## " headings are supported (the title is front matter)')
        elif t.strip().startswith('```'):
            ind = indent_of(t)
            lang = t.strip()[3:].strip()
            if lang not in ('bash', 'json', 'terminal'):
                fail(src, ln, f'code fence must be ```bash, ```json or ```terminal, got "```{lang}"')
            body, j = [], i + 1
            while j < n and lines[j][1].strip() != '```':
                body.append(lines[j][1][ind:] if lines[j][1][:ind].strip() == '' else lines[j][1])
                j += 1
            if j >= n:
                fail(src, ln, 'unclosed code fence')
            blocks.append(('fence', ln, lang, body))
            i = j + 1
        elif t.strip() == '{lettered}':
            if i + 1 >= n or not re.match(r'^[-*] ', lines[i + 1][1]):
                fail(src, ln, '{lettered} must be followed by a bullet list')
            lettered = True
            i += 1
        elif re.match(r'^\d+\. ', t) or re.match(r'^[-*] ', t):
            ordered = bool(re.match(r'^\d+\. ', t))
            marker = re.compile(r'^\d+\. ' if ordered else r'^[-*] ')
            width = 3 if ordered else 2
            items = []
            while i < n and marker.match(lines[i][1]):
                first_ln, first = lines[i]
                item = [(first_ln, marker.sub('', first, 1))]
                i += 1
                while i < n:
                    l2, t2 = lines[i]
                    if not t2.strip():
                        # a blank line continues the item only if an indented line follows
                        k = i
                        while k < n and not lines[k][1].strip():
                            k += 1
                        if k < n and indent_of(lines[k][1]) >= width:
                            item.extend((lines[x][0], '') for x in range(i, k))
                            i = k
                            continue
                        break
                    if indent_of(t2) >= width:
                        item.append((l2, t2[width:]))
                        i += 1
                    else:
                        break
                items.append(parse_blocks(item, src, top=False))
                k = i
                while k < n and not lines[k][1].strip():
                    k += 1
                if k < n and marker.match(lines[k][1]):
                    i = k  # blank line(s) between items
            if ordered and lettered:
                fail(src, ln, '{lettered} needs a bullet list, not a numbered one')
            kind = 'steps' if ordered else ('letters' if lettered else 'bullets')
            lettered = False
            blocks.append((kind, ln, items))
        elif t.startswith('>'):
            body = []
            while i < n and lines[i][1].startswith('>'):
                body.append((lines[i][0], re.sub(r'^> ?', '', lines[i][1])))
                i += 1
            m = re.match(r'^\*\*(Warning|Note|Tip):\*\* ?(.*)$', body[0][1])
            if not m:
                fail(src, ln, 'blockquote must start with **Warning:**, **Note:** or **Tip:**')
            body[0] = (body[0][0], m.group(2))
            blocks.append(('callout', ln, m.group(1), parse_blocks(body, src, top=False)))
        elif t.startswith('|'):
            rows = []
            while i < n and lines[i][1].startswith('|'):
                rows.append((lines[i][0], lines[i][1]))
                i += 1
            cells = []
            for rl, r in rows:
                if not r.rstrip().endswith('|'):
                    fail(src, rl, 'table row must end with |')
                cells.append([c.strip() for c in re.split(r'(?<!\\)\|', r.strip()[1:-1])])
            if len(cells) < 3 or not all(re.match(r'^:?-{3,}:?$', c) for c in cells[1]):
                fail(src, ln, 'table needs a header row, a |---| separator row and at least one row')
            width_ = len(cells[0])
            if any(len(r) != width_ for r in cells):
                fail(src, ln, 'table rows must all have the same number of cells')
            blocks.append(('table', ln, cells[0], cells[2:]))
        elif t.lstrip().startswith('<') or t.startswith('!['):
            fail(src, ln, 'raw HTML and images are not supported')
        elif re.match(r'^(---+|\*\*\*+)\s*$', t):
            fail(src, ln, 'horizontal rules are not supported')
        else:
            para = [t.strip()]
            i += 1
            while i < n and lines[i][1].strip() and not re.match(r'^(## |```|>|\||\d+\. |[-*] |\{lettered\})', lines[i][1].strip()) \
                    and not lines[i][1].lstrip().startswith('<'):
                para.append(lines[i][1].strip())
                i += 1
            blocks.append(('p', ln, ' '.join(para)))
    if lettered:
        fail(src, lines[-1][0], 'dangling {lettered}')
    return blocks


# ------------------------------------------------------------------ renderers
def slugify(text):
    return re.sub(r'[^a-z0-9]+', '-', text.lower()).strip('-')


def render_terminal(body, src, ln):
    out = []
    for l in body:
        e = html.escape(l, quote=False)
        if l.startswith('$ '):
            out.append('<span class="m">$</span> ' + html.escape(l[2:], quote=False))
        elif l.startswith('~ '):
            out.append('<span class="m">' + html.escape(l[2:], quote=False) + '</span>')
        elif l.startswith('✓ '):
            out.append('<span class="g">✓</span> ' + html.escape(l[2:], quote=False))
        elif l.startswith('+ '):
            out.append('<span class="g">' + html.escape(l[2:], quote=False) + '</span>')
        else:
            out.append(e)
    return ('<div class="term"><div class="dots" aria-hidden="true"><i></i><i></i><i></i></div><pre>'
            + '\n'.join(out) + '</pre></div>')


def render_table(head, rows, ctx):
    th = ''.join(f'<th>{inline(h, ctx)}</th>' for h in head)
    tr = ''.join('<tr>' + ''.join(f'<td>{inline(c, ctx)}</td>' for c in r) + '</tr>' for r in rows)
    desk = f'<table class="tb desktop-only"><thead><tr>{th}</tr></thead><tbody>{tr}</tbody></table>'
    lis = []
    for r in rows:
        s = f'<li>{inline(r[0], ctx)}'
        rest = r[1:]
        if len(head) == 3:
            s += f' <span class="d">· {html.escape(head[1].lower(), quote=False)} {inline(rest[0], ctx)}</span>'
            rest = rest[1:]
        s += ''.join(f'<div class="m">{inline(c, ctx)}</div>' for c in rest if c)
        lis.append(s + '</li>')
    return desk + '<ul class="slist mobile-only">' + ''.join(lis) + '</ul>'


def render_blocks(blocks, ctx, toc, nested=False, first_inline=False):
    """nested=True: used inside list items and callouts (paragraphs become .sub / plain)."""
    out = []
    for idx, b in enumerate(blocks):
        k = b[0]
        if k == 'h2':
            hid = slugify(b[2])
            toc.append((hid, b[2]))
            out.append(f'<h2 id="{hid}">{inline(b[2], (ctx[0], b[1], ctx[2]))}</h2>')
            continue
        c = (ctx[0], b[1], ctx[2])
        if k == 'p':
            if nested and idx == 0 and first_inline:
                out.append(inline(b[2], c))
            elif nested:
                out.append(f'<div class="sub">{inline(b[2], c)}</div>')
            else:
                out.append(f'<p class="b">{inline(b[2], c)}</p>')
        elif k == 'fence':
            if b[2] in ('bash', 'json'):
                out.append('<pre class="code"><code>' + html.escape('\n'.join(b[3]), quote=False) + '</code></pre>')
            else:
                out.append(render_terminal(b[3], ctx[0], b[1]))
        elif k == 'steps':
            lis = []
            for n_, item in enumerate(b[2], 1):
                lis.append(f'<li><i aria-hidden="true">{n_:02d}</i><div>' + render_blocks(item, c, toc, True, True) + '</div></li>')
            out.append('<ol class="steps">\n' + '\n'.join(lis) + '</ol>')
        elif k == 'letters':
            lis = ''.join(f'<li><i aria-hidden="true">{chr(65 + n_)}.</i><span>{render_blocks(item, c, toc, True, True)}</span></li>' for n_, item in enumerate(b[2]))
            out.append(f'<ul class="letters">{lis}</ul>')
        elif k == 'bullets':
            lis = ''.join(f'<li>{render_blocks(item, c, toc, True, True)}</li>' for item in b[2])
            out.append(f'<ul class="bl">{lis}</ul>')
        elif k == 'callout':
            inner = render_blocks(b[3], c, toc, True, True)
            out.append(f'<div class="call {CALLOUTS[b[2]]}"><b class="t">{b[2]}</b>{inner}</div>')
        elif k == 'table':
            out.append(render_table(b[2], b[3], c))
    return '\n'.join(out) if not nested else ''.join(out)


# ------------------------------------------------------------------ page chrome
# Public address of the site (for example "https://shop.example.com", no trailing slash). Social sites need an
# absolute URL for the share image: while this is empty the og:image / twitter:image tags are left out.
SITE_URL = ""
OG_ALT = {'landing': 'Shop: Know what your groceries really cost over time', 'guide-index': 'Shop guide'}


def og_tags(name, alt, prefix='img/og/'):
    if not SITE_URL:
        return ''
    url = f'{SITE_URL}/{prefix}{name}.jpg'
    return (f'<meta property="og:image" content="{url}">\n<meta property="og:image:width" content="2400">\n'
            f'<meta property="og:image:height" content="1260">\n<meta property="og:image:alt" content="{html.escape(alt)}">\n'
            f'<meta name="twitter:image" content="{url}">\n<meta name="twitter:image:alt" content="{html.escape(alt)}">')


def head(title, desc, css, og=''):
    return f'''<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<!-- PRODUCT NAME: title and description -->
<title>{html.escape(title, quote=False)}</title>
<meta name="description" content="{html.escape(desc)}">
<meta property="og:type" content="website">
<!-- PRODUCT NAME: og:site_name -->
<meta property="og:site_name" content="Shop">
<meta property="og:title" content="{html.escape(title)}">
<meta property="og:description" content="{html.escape(desc)}">
<meta name="twitter:card" content="summary_large_image">
{og}
<meta name="color-scheme" content="light dark">
<meta name="theme-color" content="#FFFFFF" media="(prefers-color-scheme: light)">
<meta name="theme-color" content="#0C0C0B" media="(prefers-color-scheme: dark)">
<link rel="icon" href="../img/logo-badge.svg" type="image/svg+xml">
<link rel="preload" href="../fonts/GTAmerica-Regular.woff2" as="font" type="font/woff2" crossorigin>
<link rel="preload" href="../fonts/GTAmerica-Medium.woff2" as="font" type="font/woff2" crossorigin>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Geist:ital,wght@0,100..900;1,100..900&family=IBM+Plex+Mono:ital,wght@0,100;0,200;0,300;0,400;0,500;0,600;0,700;1,100;1,200;1,300;1,400;1,500;1,600;1,700&display=swap" rel="stylesheet">
{css}
</head>'''


BRAND = '<a class="brand" href="../index.html"><img src="../img/logo-badge.svg" width="32" height="32" alt=""><!-- PRODUCT NAME: brand -->Shop</a>'

FOOTER = f'''<footer class="foot" id="credits">
<div class="foot-row">{BRAND}<a class="link foot-link" href="{GITHUB}">{GH18}View the source and README on GitHub →</a></div>
<p>MIT licence. Product data from Open Food Facts (ODbL). Full credits and licences in the repository. Not affiliated with any supermarket.</p>
<p class="made">A product from <a class="link" href="https://www.thesunday.studio/">Sunday Studio</a>.</p>
</footer>'''


def sidebar(groups, cur, tag='aside', cls='side'):
    out = ''
    for gname, pages in groups:
        out += f'<div class="grp">{html.escape(gname, quote=False)}</div>' + ''.join(
            f'<a href="{p["slug"]}.html"' + (' aria-current="page"' if p['slug'] == cur else '') + f'>{html.escape(p["title"], quote=False)}</a>' for p in pages)
    return f'<{tag} class="{cls}" aria-label="Guide pages">{out}</{tag}>'


def header(groups, cur):
    return f'''<header class="nav">{BRAND}
<nav class="links" aria-label="Main"><a class="nl" href="../index.html#features">Features</a><a class="nl" href="index.html" aria-current="page">Guide</a><a class="btn sm" href="{GITHUB}">{GH18}GitHub</a>
<details class="gmenu"><summary class="menubtn">{MENU_ICON}<span class="when-closed">Menu</span><span class="when-open">Close</span></summary>
<div class="gmenu-panel">{sidebar(groups, cur, "nav", "side in-menu")}</div></details></nav></header>'''


def page(groups, cur, title, desc, body, og=''):
    css = '<link rel="stylesheet" href="../css/base.css">\n<link rel="stylesheet" href="../css/guide.css">'
    return (head(title, desc, css, og) +
            f'\n<body>\n<a class="skip" href="#main">Skip to content</a>\n{header(groups, cur)}\n<main id="main">\n{body}\n</main>\n{FOOTER}\n</body>\n</html>\n')


def screen_panel(shot):
    def ss(th): return ', '.join(f'../img/{shot}-{th}-{w}.webp {w}w' for w in (603, 1206))
    return (f'<div class="shot"><div class="dev"><picture><source media="(prefers-color-scheme: dark)" srcset="{ss("dark")}" sizes="236px" type="image/webp">'
            f'<img src="../img/{shot}-light-1206.webp" srcset="{ss("light")}" sizes="236px" width="1206" height="2622" alt="{SCREENS[shot]}" loading="lazy" decoding="async"></picture></div></div>')


# ------------------------------------------------------------------ content loading
def load_pages(content_dir):
    if not os.path.isdir(content_dir):
        raise Fail(f'{content_dir}: content folder not found')
    pages = []
    for fn in sorted(os.listdir(content_dir)):
        if not fn.endswith('.md'):
            continue
        m = re.match(r'^(\d\d)-([a-z0-9-]+)\.md$', fn)
        if not m:
            raise Fail(f'{fn}: file name must look like NN-slug.md')
        if m.group(2) == 'index':
            raise Fail(f'{fn}: slug "index" is reserved')
        src = os.path.join('content/guide', fn) if content_dir.endswith('content/guide') else fn
        raw = open(os.path.join(content_dir, fn), encoding='utf-8').read()
        if 'TODO' in raw:
            ln = next(i for i, l in enumerate(raw.split('\n'), 1) if 'TODO' in l)
            fail(src, ln, 'TODO markers must not reach the site; remove or resolve it')
        lines = raw.split('\n')
        if not lines or lines[0].strip() != '---':
            fail(src, 1, 'file must start with --- front matter')
        try:
            end = next(i for i in range(1, len(lines)) if lines[i].strip() == '---')
        except StopIteration:
            fail(src, 1, 'front matter is not closed with ---')
        fm = {}
        for i in range(1, end):
            if not lines[i].strip():
                continue
            mm = re.match(r'^([a-z]+):\s*(.*)$', lines[i])
            if not mm or mm.group(1) not in ('title', 'group', 'summary', 'screen'):
                fail(src, i + 1, f'unknown or malformed front matter line: {lines[i]}')
            v = mm.group(2).strip()
            if len(v) >= 2 and v[0] == v[-1] and v[0] in '"\'':
                v = v[1:-1]
            fm[mm.group(1)] = v
        for key in ('title', 'group', 'summary'):
            if not fm.get(key):
                fail(src, 1, f'front matter is missing "{key}"')
        if fm.get('screen') and fm['screen'] not in SCREENS:
            fail(src, 1, f'screen must be one of {", ".join(SCREENS)}')
        body = [(i + 1, lines[i]) for i in range(end + 1, len(lines))]
        pages.append(dict(num=m.group(1), slug=m.group(2), src=src, fm=fm, title=fm['title'], group=fm['group'], body=body))
    if not pages:
        raise Fail(f'{content_dir}: no NN-slug.md files')
    slugs = [p['slug'] for p in pages]
    if len(set(slugs)) != len(slugs):
        raise Fail('duplicate slugs')
    return pages


def group_order(pages):
    groups = []
    for p in pages:
        for g in groups:
            if g[0] == p['group']:
                g[1].append(p)
                break
        else:
            groups.append((p['group'], [p]))
    return groups


def build_page(pages, groups, idx, slugs):
    p = pages[idx]
    ctx = (p['src'], 1, slugs)
    toc = []
    blocks = parse_blocks(p['body'], p['src'])
    body_html = render_blocks(blocks, ctx, toc)
    lead = inline(p['fm']['summary'], ctx)
    panel = screen_panel(p['fm']['screen']) if p['fm'].get('screen') else ''
    prev_ = pages[idx - 1] if idx > 0 else None
    next_ = pages[idx + 1] if idx < len(pages) - 1 else None
    cards = ''
    if prev_:
        cards += f'<a href="{prev_["slug"]}.html"><small>← Previous</small><span>{html.escape(prev_["title"], quote=False)}</span></a>'
    if next_:
        cls = ' class="only-next"' if not prev_ else ''
        cards += f'<a href="{next_["slug"]}.html"{cls}><small>Next →</small><span>{html.escape(next_["title"], quote=False)}</span></a>'
    pn = f'<nav class="pn" aria-label="Previous and next">{cards}</nav>' if cards else ''
    on = next((i for i, (hid, t) in enumerate(toc) if t == 'Steps'), 0)
    rail = ''
    if toc:
        cur_attr = ' aria-current="location"'
        links = ''.join(f'<a href="#{hid}"{cur_attr if i == on else ""}>{html.escape(t, quote=False)}</a>' for i, (hid, t) in enumerate(toc))
        rail = f'\n<nav class="toc" aria-label="On this page"><div class="h">On this page</div>{links}</nav>'
    art = (f'<article class="doc"><p class="kicker">{html.escape(p["group"], quote=False)}</p><h1>{html.escape(p["title"], quote=False)}</h1><p class="lead">{lead}</p>{panel}\n'
           f'{body_html}\n{pn}</article>')
    body = f'<div class="gd"><div class="side-col">{sidebar(groups, p["slug"])}</div>\n{art}{rail}</div>'
    return page(groups, p['slug'], f'{p["title"]} · Shop Guide', p['fm']['summary'], body,
                og_tags('guide-' + p['slug'], f'{p["title"]} · Shop guide'))


def build_index(groups):
    cards = ''
    for i, (gname, ps) in enumerate(groups):
        lis = ''.join(f'<li><a href="{p["slug"]}.html">{html.escape(p["title"], quote=False)}<span aria-hidden="true">→</span></a></li>' for p in ps)
        cards += f'<div class="gc" style="background:{TINTS[i % 4]}"><h3>{html.escape(gname, quote=False)}</h3><p>{BLURBS.get(gname, "")}</p><ul>{lis}</ul></div>'
    path = ('<ol class="steps" style="margin-top:6px"><li><i aria-hidden="true">01</i><div>Install the server on a machine with Docker.</div></li>'
            '<li><i aria-hidden="true">02</i><div>Pair the iPhone app with it.</div></li>'
            '<li><i aria-hidden="true">03</i><div>Photograph receipts, review the ones that need it, read your numbers.</div></li></ol>')
    lead = 'Everything you need to run Shop yourself: install the server, pair the iPhone app, and read your numbers.'
    body = (f'<div class="gd home"><div class="side-col">{sidebar(groups, None)}</div>\n'
            f'<article class="doc wide"><h1>Guide</h1><p class="lead">{lead}</p><div class="gcards">{cards}</div><h2>The path in short</h2>{path}</article></div>')
    return page(groups, None, 'Guide · Shop', lead, body, og_tags('guide-index', OG_ALT['guide-index']))


def patch_landing():
    p = os.path.join(HERE, 'index.html')
    src = open(p, encoding='utf-8').read()
    new = re.sub(r'(<!-- OG:IMAGE:START -->).*?(<!-- OG:IMAGE:END -->)',
                 lambda m: m.group(1) + '\n' + og_tags('landing', OG_ALT['landing']) + ('\n' if SITE_URL else '') + m.group(2), src, flags=re.S)
    if new != src:
        open(p, 'w', encoding='utf-8').write(new)
    if not SITE_URL:
        print('note: SITE_URL is empty in build.py, so no og:image tags were written')


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--content', default=os.path.join(HERE, 'content', 'guide'))
    ap.add_argument('--out', default=os.path.join(HERE, 'guide'))
    a = ap.parse_args()
    try:
        pages = load_pages(a.content)
        groups = group_order(pages)
        slugs = {p['slug'] for p in pages}
        os.makedirs(a.out, exist_ok=True)
        outputs = {f'{p["slug"]}.html': build_page(pages, groups, i, slugs) for i, p in enumerate(pages)}
        outputs['index.html'] = build_index(groups)
    except Fail as e:
        print(f'build failed: {e}', file=sys.stderr)
        sys.exit(1)
    for fn in os.listdir(a.out):
        if fn.endswith('.html') and fn not in outputs:
            os.remove(os.path.join(a.out, fn))
    for fn, content in outputs.items():
        with open(os.path.join(a.out, fn), 'w', encoding='utf-8') as f:
            f.write(content)
    patch_landing()
    print(f'built {len(pages)} guide pages + index into {a.out}')


if __name__ == '__main__':
    main()
