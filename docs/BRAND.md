# Brand

The xtandard-image mark is a chevron (`>`, a request going through the proxy) beside an image
glyph: a sun and two mountains. It comes as an app icon, a bare symbol and a wordmark lockup.
Everything lives in [`docs/assets/`](assets/). The SVGs are the masters and every PNG and ICO is
built from them (WP-153).

The owner provided the originals as AI-generated rasters at low resolution, with background-removal
halos and no light-background wordmark. The SVGs redraw them as clean geometry: rounded polygons,
one circle and linear gradients, with no filters except the soft shadow in the app icon and no
embedded fonts or images. The originals are not in the repository.

## Files

| File | What it is | Use it for |
|---|---|---|
| `logo.svg` | App icon: the symbol on a near-black rounded square with faint orbit lines | The master for every square raster |
| `logo.png` (1024), `logo-256.png`, `logo-128.png` | App icon rasters | Client READMEs and registries (`logo-128.png` at 128 px, or `logo-256.png` at `width="128"` for 2× screens), avatars, the GitHub org and repo picture, the Docker Hub/GHCR logo upload |
| `symbol.svg`, `symbol.png` (256 high) | The symbol alone, transparent | Inline UI (the playground header), places that draw their own surface |
| `lockup-horizontal-dark.svg` / `.png` (2×) | Symbol + wordmark, `xtandard-` in near-white | Headers on dark backgrounds: the README in GitHub dark mode, the Pages site in dark mode |
| `lockup-horizontal-light.svg` / `.png` (2×) | Symbol + wordmark, `xtandard-` in ink | Headers on light backgrounds: the README in GitHub light mode, npm, the Pages site |
| `lockup-stacked-{dark,light}.svg` / `.png` (2×) | Symbol left, `xtandard-` over `image` | Square-ish slots: slides, stickers, sidebars |
| `monochrome-white.svg`, `monochrome-black.svg` | One-colour horizontal lockup | Photos and busy backgrounds (white); print, fax and engraving (black) |
| `favicon.svg`, `favicon.ico` (16/32/48), `apple-touch-icon.png` (180) | Favicon set: the icon without the orbit lines and the shadow, which do not survive 16 px | Browser tabs: the playground (embedded, `internal/playground/assets/brand/`) and the Pages site |
| `social.svg`, `social.png` (1280×640) | Social card: icon, wordmark, tagline, `docker run` line | The GitHub repository social preview (Settings → General → Social preview, uploaded by hand), `og:image` of the Pages site |

Rebuild every raster after editing a master:

```sh
task brand:assets   # sh scripts/brand-assets.sh: rsvg-convert (librsvg) + magick (ImageMagick 7), oxipng -o max when installed
task brand:check    # rasters present, playground copies equal to docs/assets
```

The script also copies `favicon.svg`, `favicon.ico`, `apple-touch-icon.png` and `symbol.svg` into
`internal/playground/assets/brand/`, which the playground embeds with `go:embed` and serves from its
own origin (CSP `default-src 'self'`).

### Where each file goes

- **Root README header**: a `<picture>` that picks `lockup-horizontal-dark.png` or
  `lockup-horizontal-light.png` with `prefers-color-scheme`, by relative `docs/assets/...` path. The
  root README is not shipped to any registry, and relative paths also render while the repository is
  private (absolute raw URLs need a token there).
- **Client READMEs** (`clients/*/README.md`, shipped to npm, PyPI, crates.io, pkg.go.dev, Packagist,
  RubyGems, NuGet, Hex, pub.dev, Maven Central, Swift Package Index): `logo-128.png` by absolute raw
  URL, because registries do not ship `docs/`. HTML `<p align="center"><img …></p>` where the registry
  renders HTML (npm, PyPI, crates.io, Packagist, GitHub for Java/Kotlin/Swift/Ruby); plain Markdown
  `![xtandard-image](…)` for pkg.go.dev, NuGet, pub.dev and Hex/ex_doc, whose sanitisers drop or
  escape raw HTML.
- **Favicon**: `favicon.ico` (with `sizes`) first, then `favicon.svg` (`type="image/svg+xml"`, which
  modern browsers prefer), then `apple-touch-icon.png`.
- **Social preview**: `social.png`.
- **Docker / OCI**: there is no OCI annotation for a logo (`org.opencontainers.image.*` has title,
  description, url, source, documentation, licenses and so on, all set in the `Dockerfile`). Registries
  that show one (Docker Hub, Artifact Hub for the Helm chart) take an upload or a URL: use `logo.png`
  or `logo-256.png`. Artifact Hub reads `icon:` in `Chart.yaml` (`logo-256.png` by raw URL) when the
  chart is listed there.

## Palette

xtandard-image is blue. The other xtandard projects (xyte, flags, webhooks) use lime; the blue is the
owner's choice for this project, from the provided artwork. The family shares the near-black icon
surface, the orbit motif and the social-card layout.

| Token | Hex | OKLCH | Role | Contrast on white / on ink |
|---|---|---|---|---|
| `xi-blue-deep` | `#0C59FC` | `oklch(53.8% 0.251 262.7)` | Accent on light surfaces (links, primary buttons), the front mountain, the chevron's end | 5.45 / 3.44 |
| `xi-blue` | `#156BFA` | `oklch(57.1% 0.227 260.8)` | Bottom of the `image` gradient | 4.65 / 4.04 |
| `xi-blue-mid` | `#2A95FB` | `oklch(66.2% 0.178 251.9)` | Middle of the chevron gradient | 3.09 / 6.08 |
| `xi-sky` | `#2EB9FC` | `oklch(74.4% 0.148 235.8)` | Accent on dark surfaces, top of the `image` gradient | 2.22 / 8.47 |
| `xi-cyan` | `#65CEFC` | `oklch(80.7% 0.117 229.3)` | Chevron highlight, the social-card tagline | 1.78 / 10.55 |
| `xi-ice` | `#9CD7FC` | `oklch(85.2% 0.079 236.5)` | Sun, the back mountain | 1.55 / 12.11 |
| `xi-mist` | `#E6F4FD` | `oklch(95.9% 0.019 235.7)` | The back mountain's peak | 1.12 / 16.75 |
| `xi-ink` | `#0F1215` | `oklch(18.0% 0.008 248.3)` | Icon surface, dark page background, `xtandard-` on light | 18.79 / — |
| `xi-surface` | `#171A1E` | `oklch(21.6% 0.009 255.6)` | Raised dark surface (playground panels) | 17.46 / 1.08 |
| `xi-orbit` | `#4E5459` | `oklch(44.2% 0.011 243.0)` | Orbit lines and dots in the icon | 7.67 / 2.45 |
| `xi-slate` | `#8B939C` | `oklch(66.0% 0.016 251.2)` | Secondary text on ink (the social-card command) | 3.11 / 6.04 |
| `xi-paper` | `#FAFAFA` | `oklch(98.5% 0 0)` | `xtandard-` on dark | 1.04 / 18.00 |

Text accents: on light surfaces use `xi-blue-deep` (AA for body text); on dark surfaces use `xi-sky`
(AAA). `xi-blue-mid`, `xi-cyan` and `xi-ice` are artwork colours, not text colours on white.

### Gradients

All are linear, light at the top left and deep at the bottom right, as if lit from the top left.

| Shape | Stops |
|---|---|
| Chevron | `#65CEFC` 0 → `#2A95FB` 45% → `#0C59FC` 100%, top left to bottom right |
| Sun | `#8FDCFD` → `#4AAEF8`, top to bottom |
| Back mountain | `#E6F4FD` → `#B4DEFB` 50% → `#6DBAF8`, peak to bottom right (light lockups start at `#C4E4FB` so the peak does not melt into white) |
| Front mountain | `#2FA0FB` → `#0A4DFB`, top left to bottom right |
| `image` (wordmark) | `#2EB9FC` → `#156BFA`, top to bottom; light lockups use `#1E9BFB` → `#0C59FC` for contrast on white |
| Icon surface | `#1A1C1F` → `#08090B`, top to bottom, with a 4-unit white rim at 14% → 3% opacity |

## Wordmark

`xtandard-image` in **Urbanist** Black (900), tracking −1.2%, kerned by HarfBuzz, converted to paths:
the SVGs need no font. `xtandard-` is `xi-paper` on dark and `xi-ink` on light; `image` is always
the blue gradient. Urbanist (Corey Hu and The Urbanist Project Authors, https://github.com/coreyhu/Urbanist)
is the closest open match to the original artwork: a geometric sans with a single-storey `a` and `g`
and a diagonally cut `t`. The social card adds Urbanist SemiBold (600) for the tagline and
**JetBrains Mono** Regular for the command.

Licence: both fonts are under the SIL Open Font License 1.1, which ADR 0006 allows. The SVGs contain
glyph outlines (artwork made with the font), not the font software, so the OFL's conditions on
distributing the font do not apply to them; the fonts are credited in `NOTICE` anyway. To regenerate
the outlines, shape the text with HarfBuzz at `wght=900` from the variable `Urbanist[wght].ttf` of
google/fonts, draw each glyph with fontTools' `SVGPathPen`, and keep the lockup geometry of the
current SVGs (x-height 45 units against a 90-unit symbol in the horizontal lockup).

## Geometry

The symbol's grid is 384 × 285 units (`symbol.svg`). The chevron's top and bottom edges are
horizontal, its bottom shares the mountains' baseline, and the sun's diameter is 100 units. In the app
icon the symbol is scaled by 1024/543 and centred, a few units above the geometric centre; the icon's
corner radius is 224 of 1024 (21.9%).

**Clear space**: keep the sun's diameter (35% of the symbol's height) free on every side of the
symbol or lockup. The app icon needs no clear space inside its square; outside it, keep 10% of its
width.

**Minimum sizes**: app icon 16 px (use the favicon set below 48 px), symbol 20 px high, horizontal
lockup 20 px high (about 170 px wide), stacked lockup 48 px high. Below these use the icon alone.

## Do and don't

Do:

- Use the dark lockup on dark backgrounds and the light lockup on light ones; on GitHub, switch with
  `<picture>` and `prefers-color-scheme`.
- Use the app icon wherever a square avatar is expected.
- Use the monochrome lockups on photos, coloured backgrounds or in one-colour print.
- Reference the files by absolute URL outside the repository (registries, other sites).

Don't:

- Recolour the symbol (no lime: that is the siblings' colour), swap gradient directions or flatten
  the gradients into one blue, except in the monochrome files.
- Set the wordmark in another font, or as live text in a different weight.
- Put the dark lockup (near-white `xtandard-`) on a light background, or the light one on dark.
- Stretch, rotate, outline or add effects (glow, bevel, extra shadows) to the symbol.
- Rearrange the symbol: the chevron stays on the left and the sun at the top right.
- Re-introduce the original rasters: they have halos from background removal.

## Fidelity to the original artwork

The SVGs follow the originals' proportions, measured on the 555 px icon. Deliberate differences:

- The original icon and symbol have a bevel highlight on every edge and a heavy shadow per shape;
  the SVGs keep the gradients and one soft shadow (icon only), and drop the bevel.
- The original symbol and lockup rasters show a single mountain, while the app icon has two. The SVGs
  use the two-mountain symbol everywhere, so every file shows the same mark.
- The original is not quite symmetric (the chevron's lower arm is longer, and its bottom sits on the
  mountains' baseline); the SVG keeps that, but centres the symbol in the icon horizontally.
- The wordmark is Urbanist, the closest OFL font, not the unknown original typeface: letter widths
  differ slightly (the SVG wordmark is about 5% wider at the same x-height).
- The orbit lines are two arcs of one circle around the icon centre, plus a fainter inner ring, as in
  the original.
