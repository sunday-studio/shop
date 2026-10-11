#!/usr/bin/env python3
"""Share cards (Open Graph / Twitter) for every page of the site.

    python3 design/website/og/make_og.py

Writes site/img/og/*.jpg, each rendered at 2x (landscape 2400x1260, square 2400x2400) with headless Chrome:
  landing.jpg          wide card for the landing page (tagline + phone)
  landing-square.jpg   square card (1:1, for apps that crop to a square)
  guide-index.jpg      wide card for the Guide home
  guide-<slug>.jpg     one wide card per guide page (group, title, summary, the page's app screen)
The phone is drawn flat (no tilt) from the full-resolution screens so text stays sharp.
Needs Google Chrome (macOS path below) and, for 4:4:4 JPEG, Pillow; falls back to `sips` without Pillow.
"""
import html, os, re, subprocess, sys, tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(os.path.dirname(HERE)))
SITE = os.path.join(ROOT, 'site')
OUT = os.path.join(SITE, 'img', 'og')
SCREENS = os.path.join(ROOT, 'design', 'website', 'screens')
CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
sys.path.insert(0, os.path.join(ROOT, 'design', 'website', 'canvas-src'))
import web_b  # painted-dusk sky art and light tokens

TAGLINE = ['Know what', 'your groceries', 'really cost', 'over time.']
TAGLINE_SQUARE = ['Know what your', 'groceries really', 'cost over time.']

VARS = ';'.join(f'--{k}:{v}' for k, v in web_b.TOKS['light'].items())
CSS = f'''
@font-face{{font-family:'GT America';src:url(file://{SITE}/fonts/GTAmerica-Regular.woff2) format('woff2');font-weight:400}}
@font-face{{font-family:'GT America';src:url(file://{SITE}/fonts/GTAmerica-Medium.woff2) format('woff2');font-weight:500}}
:root{{{VARS};--ink:#2B2D3F;--ink2:#686B7E;--blueText:#0071E3}}
*{{box-sizing:border-box}}html,body{{margin:0;background:#fff}}
.card{{position:relative;overflow:hidden;font-family:'GT America',system-ui,sans-serif;color:var(--ink);-webkit-font-smoothing:antialiased}}
.brand{{position:absolute;left:72px;top:64px;display:flex;align-items:center;gap:16px;font-size:38px;font-weight:500;letter-spacing:-.02em;z-index:2}}
.brand img{{width:56px;height:56px;display:block}}
h1{{margin:0;font-weight:400;letter-spacing:-.045em}}
.dev{{border-radius:46px;background:#33364D;padding:7px;box-shadow:0 30px 60px -20px rgba(43,45,63,.35),0 0 0 1px rgba(43,45,63,.10)}}
.dev img{{display:block;width:100%;height:auto;border-radius:39px}}
.phone{{position:absolute;z-index:2}}
'''


def read_pages():
    out = []
    d = os.path.join(SITE, 'content', 'guide')
    for fn in sorted(os.listdir(d)):
        if not fn.endswith('.md'):
            continue
        txt = open(os.path.join(d, fn), encoding='utf-8').read()
        fm = dict(re.findall(r'^(\w+):\s*(.*)$', txt.split('---')[1], flags=re.M))
        slug = re.sub(r'^\d+-', '', fn[:-3])
        out.append(dict(slug=slug, title=fm['title'], group=fm['group'], summary=fm['summary'], screen=fm.get('screen', 'home')))
    return out


def art(w, h, box):
    a = web_b.art('o')
    a = re.sub(r'viewBox="0 0 1232 1120" preserveAspectRatio="[^"]*"', f'viewBox="{box}" preserveAspectRatio="none"', a)
    return a.replace('class="art"', f'style="position:absolute;inset:0;width:{w}px;height:{h}px;display:block"')


def phone(screen, left, top, width):
    return (f'<div class="phone" style="left:{left}px;top:{top}px;width:{width}px"><div class="dev">'
            f'<img src="file://{SCREENS}/{screen}-light.png" alt=""></div></div>')


def brand():
    return f'<div class="brand"><img src="file://{SITE}/img/logo-badge.svg" alt="">Shop</div>'


def card(w, h, inner, box):
    return f'<!doctype html><meta charset="utf-8"><style>{CSS}</style><div class="card" style="width:{w}px;height:{h}px">{art(w, h, box)}{brand()}{inner}</div>'


def landing():
    h = '<br>'.join(TAGLINE)
    inner = f'<h1 style="position:absolute;left:72px;top:178px;font-size:84px;line-height:1;width:600px;z-index:2">{h}</h1>' + phone('receipt', 770, 92, 340)
    return card(1200, 630, inner, '16 340 1200 630')


def square():
    h = '<br>'.join(TAGLINE_SQUARE)
    inner = f'<h1 style="position:absolute;left:72px;top:190px;font-size:104px;line-height:1;width:1056px;z-index:2">{h}</h1>' + phone('receipt', 390, 640, 420)
    return card(1200, 1200, inner, '56 0 1120 1120')


def wide_page(title, kicker, summary, screen):
    size = 76 if len(title) < 24 else 64
    inner = (f'<div style="position:absolute;left:72px;top:176px;width:600px;z-index:2">'
             f'<div style="font-size:26px;font-weight:500;color:var(--blueText);margin-bottom:16px">{html.escape(kicker, quote=False)}</div>'
             f'<h1 style="font-size:{size}px;line-height:1.02">{html.escape(title, quote=False)}</h1>'
             f'<p style="font-size:28px;line-height:1.4;color:#4B4E63;margin:22px 0 0;max-width:560px">{html.escape(summary, quote=False)}</p></div>'
             + phone(screen, 770, 92, 340))
    return card(1200, 630, inner, '16 340 1200 630')


def render(name, doc, w, h, tmp):
    src = os.path.join(tmp, name + '.html')
    png = os.path.join(tmp, name + '.png')
    open(src, 'w', encoding='utf-8').write(doc)
    subprocess.run([CHROME, '--headless=new', '--disable-gpu', '--hide-scrollbars', '--force-device-scale-factor=2',
                    f'--window-size={w},{h}', '--virtual-time-budget=4000', f'--screenshot={png}', 'file://' + src],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    dst = os.path.join(OUT, name + '.jpg')
    try:
        from PIL import Image
        Image.open(png).convert('RGB').save(dst, 'JPEG', quality=93, subsampling=0, optimize=True)
    except ImportError:
        subprocess.run(['sips', '-s', 'format', 'jpeg', '-s', 'formatOptions', '93', png, '--out', dst], check=True, stdout=subprocess.DEVNULL)
    print(f'{name}.jpg  {os.path.getsize(dst) // 1024} KB')


def main():
    os.makedirs(OUT, exist_ok=True)
    pages = read_pages()
    with tempfile.TemporaryDirectory() as tmp:
        render('landing', landing(), 1200, 630, tmp)
        render('landing-square', square(), 1200, 1200, tmp)
        render('guide-index', wide_page('Guide', 'Shop', 'Install the server, pair the iPhone app, and read your numbers.', 'home'), 1200, 630, tmp)
        for p in pages:
            render('guide-' + p['slug'], wide_page(p['title'], p['group'], p['summary'], p['screen']), 1200, 630, tmp)


if __name__ == '__main__':
    main()
