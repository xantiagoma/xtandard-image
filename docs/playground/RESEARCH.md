# Playground v2: prior-art research

Researched on 2026-09-26. The effort was modest: a few fetches per site. Several of these tools are
JavaScript apps that a plain HTTP fetch cannot render. In those cases the description comes from docs,
blog posts or source trees, and is marked "inferred". The playground v2 constraints are an embedded
binary, vanilla ES modules and CSS, CSP `default-src 'self'`, and offline use. They rule out anything
that needs a vendor account, a cloud AI service or a CDN.

## imgproxy demo

Sources: https://imgproxy.net/ (the `#demo` section)

What it does well:
- You resize the sample image directly (drag to resize) and the "imgproxy URL" text area updates as you drag.
- There is one sample image (a NASA Mars crater photo), so nothing distracts from the URL-to-result link.

What we adopt:
- The URL panel is the primary output and updates on every change, including drag-driven changes.
- Width and height can be set by dragging a handle on the preview frame as well as by typing numbers.

The fetched page did not show other option toggles, a before/after view or byte sizes. If the demo has
them, they are not in its server-rendered HTML.

## imgix Sandbox

Sources: https://sandbox.imgix.com (JS app, shell only), https://portfolio.miguelcardona.com/2016/01/30/imgix-sandbox.html (designer write-up)

What it does well:
- You paste an existing URL and the sandbox breaks it down into parameter/value pairs. You can then edit those pairs or add new ones.
- Its own URL (the browser bar) updates automatically, so the exact state can be shared.
- Parameter names autocomplete, value dropdowns give hints, and a "?" hover beside each parameter shows a short description.
- Composite parameters (blend, mark, mask) open a sub-editor with its own live preview. Changes there flow back into the parent image.
- It has quality comparison views, and entry points are URL paste, upload or example images.
- The layout is responsive down to phone width.

What we adopt:
- A "paste URL to parse into the builder" input that works for every supported dialect.
- The whole state lives in the location hash, so the address bar is always a share link.
- Each option has an inline "?" doc popover. A search/autocomplete box adds an option by name.
- Nested editors for watermark and overlay options.
- Three sources: sample gallery, upload and URL.

The exact category grouping in the current Sandbox is inferred. The fetched sources do not list the categories.

## Cloudinary Transformation Builder

Sources: https://cloudinary.com/documentation/transformation_builder_tutorial, search results for https://tx.cloudinary.com and https://cloudinary.com/documentation/sdks/js/transformation-builder/index.html

What it does well:
- In "construct mode" a transformation is an ordered list of self-contained actions, and you can see the result of each step as it is applied.
- The output is both a delivery URL and SDK code, each ready to copy.
- Saved presets are called "named transformations".
- The docs say that, as of June 2026, the Media Library builder handles video only and image editing has moved to "Studio". This was not observed first-hand.

What we adopt:
- Copy buttons for several output forms: raw URL, curl, and a TypeScript `fetch` snippet.
- Chained or ordered steps are shown as a visible list where the dialect supports them (Cloudinary `/` chains, ImageKit `:` chains).

Not adopted: "converse mode" (AI natural-language builder) and account-bound named transformations.

## ImageKit

Sources: https://imagekit.io/docs/image-transformation

What it does well:
- The docs are grouped by category: resize and crop, overlays, effects and enhancements, AI, and vector/animated.
- A diagram splits a URL into endpoint, `tr:` parameters and path.
- Examples pair the exact URL with its rendered result, and chaining is shown inline (`tr:w-300,h-300,fo-face:r-max`).
- No first-party interactive playground was found. A third-party "transformation-builder" skill uses an LLM.

What we adopt:
- The URL panel highlights the URL anatomy: endpoint, signature, options and source segments are coloured separately.
- Option groups in the builder use similar category names, so users of the provider docs can find their way.

## wsrv.nl docs

Sources: https://wsrv.nl/docs/

What it does well:
- A flat sidebar of categories: Quick reference, Size, Fit, Crop, Mask, Orientation, Adjustment, Format.
- A quick-reference page lists every parameter in one place.
- Each example gives the URL plus HTML and Markdown embed snippets.

What we adopt:
- Builder groups follow a similar small, fixed category set: Size, Fit/Crop, Orientation, Adjust, Effects, Watermark, Format/Quality, Metadata.
- A compact "all options" reference view that is searchable.

## Cloudflare Images docs

Sources: https://developers.cloudflare.com/images/transform-images/transform-via-url/, https://developers.cloudflare.com/images/transform-images/

What it does well:
- Each option has a description, accepted values, a highlighted default, a numeric range and "Note" callouts.
- Options are shown in two syntaxes side by side: the URL form (`width=...`) and the Workers form (`cf: {image: {...}}`).
- Visual examples come before text for spatial concepts such as `gravity=XxY`.
- There is no interactive playground.

What we adopt:
- Inline docs show the default, the range and caveats for each option. Typed inputs enforce the range (sliders or number inputs with min and max).
- The "same transform in every dialect" panel extends the two-syntax idea to all nine dialects. Options a dialect cannot express are flagged as unsupported.
- A focal-point picker: click on the preview to set gravity x/y.

## Squoosh

Sources: https://github.com/GoogleChromeLabs/squoosh (src/client/lazy-app/Compress: Options, Output, Results; Output/custom-els: TwoUp, PinchZoom), review articles found by search (e.g. https://meshworld.in/blog/web-dev/squoosh-image-optimization-guide/)

What it does well:
- A draggable two-up slider over a single shared viewport shows the original and the processed image.
- Pinch/wheel zoom and pan are synchronised across both sides, so you can inspect artifacts in gradients, text and hair.
- Output size and percentage saved update live as settings change.
- Each side has its own options panel, so you compare two encodings rather than just original versus processed.
- These are small, framework-free custom elements (TwoUp, PinchZoom).

What we adopt:
- A before/after slider with synchronised zoom and pan (wheel, pinch, drag). Keyboard focus moves the divider.
- A side-by-side mode as an alternative to the slider.
- A byte-size badge with "% saved" against the original.
- A checkerboard background behind images with alpha.
- Optionally, the right side can use a second option set (for example AVIF against WebP).

Squoosh's layout is observed from source and reviews rather than a live session (inferred).

## Bunny Optimizer

Sources: https://bunny.net/docs/optimizer/dynamic-images/overview, search results for https://bunny.net/optimizer/

What it does well:
- The docs state the processing order explicitly: crop, then resize, then filters, then colour, then format/quality.
- Each parameter documents its range and default (for example `quality` 0-100, default 85).
- Presets are called "Image Classes".
- The docs have no interactive demo.

What we adopt:
- The builder shows the effective pipeline order, so users understand why option order does or does not matter.
- Presets appear in the builder when the server has presets configured.

## Adopted in playground v2

| Pattern | Seen in | Our implementation |
|---|---|---|
| Live URL that updates on every change | imgproxy, imgix | URL panel with anatomy highlighting and copy button |
| Paste URL, parse into editable params | imgix Sandbox | Dialect auto-detect parser fills the builder |
| State in the address bar, shareable | imgix Sandbox | Full state in `location.hash`, plus a history list |
| Inline "?" docs, autocomplete | imgix Sandbox, Cloudflare docs | Popover with description, default and range. Option search box |
| Category-grouped options | wsrv, ImageKit | Collapsible groups with typed inputs |
| Multiple syntaxes for one transform | Cloudflare (URL and Workers) | "Every dialect" translation panel with unsupported flags |
| Copy as code | Cloudinary builder | Copy URL, curl, TypeScript `fetch` |
| Per-step chain view | Cloudinary, ImageKit | Ordered step list for chaining dialects |
| URL anatomy diagram | ImageKit docs | Coloured segments in the URL panel |
| Before/after slider, synced zoom/pan | Squoosh | Vanilla custom elements (two-up, pinch-zoom) |
| Size and % saved | Squoosh | Badge from `Content-Length` against the source size |
| Drag-to-resize and click-for-focal-point | imgproxy, Cloudflare gravity | Handles and click on the preview frame |
| Processing-order explanation | Bunny docs | Pipeline order shown in the builder footer |
| Presets | Cloudinary, Bunny | Server-configured presets listed in the builder |

We also add features none of these tools had in the fetched material: a response-headers view, a
Server-Timing bar chart, the `/info` JSON panel with detection boxes, an fs source browser, keyboard
shortcuts and dark mode.

## Not adopted

- Account features (Media Library, saved named transformations, Image Classes set up in a dashboard): they need server-side user state. Our presets come from config instead.
- AI builders (Cloudinary converse mode, LLM transformation builders) and AI transforms such as generative background removal or upscaling: they need a cloud model and network access, and the playground must work offline.
- WASM codecs in the browser (Squoosh): our encoding happens in the server's libvips. Client-side codecs would show results the server does not produce, and would make the embedded bundle much larger.
- Vendor sample CDNs and remote demo images: blocked by CSP `default-src 'self'` and the offline requirement. We ship samples in the binary.
- Mobile-first composite pop-up windows (imgix): we use nested inline editors, which avoid extra windows and focus traps.
