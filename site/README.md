# Shop website (static)

Plain HTML and CSS. No framework, no build step, no JavaScript, no external requests.

## Preview

    cd site && python3 -m http.server 8000     # then open http://localhost:8000/

Pages: `index.html` (landing), `guide/index.html` (Guide home), `guide/install.html` (Install the server).
Open `guide/` pages from a server or from disk; all links are relative.

## Layout

- `css/base.css`: tokens (light and dark, follows the system), fonts, header, buttons, footer, phone frame, motion.
- `css/home.css`: landing page. `css/guide.css`: guide pages. Mobile first; desktop layout from 800 px.
- `fonts/`: GT America 400/500/700 and GT America Mono 400 (woff2, `font-display: swap`).
- `img/`: `logo-badge.svg` (also the favicon) and the app screens as WebP, light and dark, 603 px and 1206 px wide
  (`<picture>` picks the theme with `prefers-color-scheme`, `srcset` picks the size).
- No JS: the burger and the guide menu are `<details>`; the feature cycle, hero rise and scroll reveal are CSS animations
  (`animation-timeline: view()` where supported; everything is simply visible elsewhere and with `prefers-reduced-motion`).

Regenerate an image (source PNGs are in `design/website/screens/`, 1206x2622):

    cwebp -q 82 -m 6 -resize 1206 0 receipt-light.png -o img/receipt-light-1206.webp
    cwebp -q 82 -m 6 -resize 603 0 receipt-light.png -o img/receipt-light-603.webp

## Product name

"Shop" is a placeholder. To rename, search and replace in `index.html` and `guide/*.html`:

    grep -rn "Shop" index.html guide/

Places that carry the name: `<title>` and meta description of every page; the header brand (`.brand`) and the footer brand on every page; a few copy lines ("What Shop can tell you...", "Everything you need to run Shop yourself...",
the "What Shop is" sidebar and previous-link entries). Marked with `PRODUCT NAME` HTML comments where it is a brand or meta.

## Links

GitHub links point to https://github.com/sunday-studio/shop (header pill, mobile menu, hero and closing buttons, footer, guide header);
`grep -rn "sunday-studio/shop" .` finds them all. The footer also links to https://www.thesunday.studio/.
Other guide pages are not built yet; their sidebar and previous/next links point to `#`.

## Layout, motion

- Content sits in a centred 1280 px column (80 px side padding); the hero and closing sky art stay full-bleed.
- `html{scroll-behavior:smooth}` and the cross-document view transition (`@view-transition`) are only enabled with
  `prefers-reduced-motion: no-preference`.
- Feature showcase: four hidden radios (`#sf1`..`#sf4`) before `.shrow`; the list items are `<label>`s. The per-selection rules in
  `css/home.css` ("Click-to-select") restart the 20 s cycle from the clicked item.

## Licences

- **Fonts: TRIAL licence.** The GT America files in `fonts/` (Grilli Type) are trial files. A web licence must be bought
  before the site goes public; do not publish with these files.
- Logo and screens: project assets from `design/website/`.
- Site code: MIT, like the project.
