# URL dialects

Every dialect parser turns a URL into the same `transform.Request` (see `ARCHITECTURE.md` §4). This document is the spec each parser work package implements. Researched September 2026; each section cites its upstream reference.

Conventions used below:

- **TR** = `transform.Request`; `P` = the current `Pipeline`; `O` = `Output`; `D` = `Delivery`.
- "M1/M2/M3" = roadmap milestone in which the option actually takes effect. Options scheduled for a later milestone are still **parsed** from M1 on (so URLs never 400 because of them); until implemented they are recorded in `TR.Ignored` and surfaced in `X-Xi-Ignored` when debug headers are on.
- **Unsupported** = parsed and ignored forever (listed so behaviour is deliberate). In `XI_DIALECTS_STRICT=true` mode, unsupported and unknown options return 400 instead.

## 1. Dialect selection

Selection happens before parsing, in `dialect/router`:

1. **Host binding** (`dialects.<name>.hosts`): if the request `Host` matches, that dialect handles the whole path with an empty prefix. This is what makes unpic work unmodified: point `images.example.com` at xi, bind it to `imgix`, and URLs shaped exactly like `https://images.example.com/photo.jpg?w=400&auto=format` just work.
2. **Path prefix** (`dialects.<name>.prefix`): longest matching prefix wins; the prefix is stripped.
3. **Default dialect** (`XI_DIALECTS_DEFAULT`, default `imgproxy`) for everything else.

A mount may bind a **named source** (`dialects.<name>.source`) used to resolve relative paths (imgix "source", Cloudinary "cloud", ImageKit "URL endpoint", Bunny "pull zone"). Multiple mounts of one dialect are allowed (`dialects.imgix.mounts[]`), each with its own prefix/hosts/source/signing keys.

Default mounts (all dialects other than imgproxy are disabled until enabled in config):

| Dialect | Default prefix | Notes |
|---|---|---|
| imgproxy | `/` (default dialect), `/info/` | Canonical |
| query | `/q/` | Native query + imaginary endpoints |
| wsrv | `/` when query has `url=` and path is `/` or empty; also `/wsrv/` | Router checks `?url=` before falling back to imgproxy |
| cloudflare | `/cdn-cgi/image/` | Exact Cloudflare path shape, no config needed besides enabling |
| cloudflare-images | `/cdn-cgi/imagedelivery/` (and host-bound `imagedelivery.net` shape) | M2 |
| imgix | `/imgix/<mount>/` | Usually host-bound |
| cloudinary | `/cloudinary/` then `/<cloud>/image/{upload,fetch}/…` | Or host-bound as `res.cloudinary.com`-like |
| imagekit | `/imagekit/<mount>/` | Or host-bound |
| bunny | `/bunny/<mount>/` | Or host-bound |
| thumbor | `/thumbor/` | Cannot share `/` with imgproxy (both signature-first) unless host-bound |
| simple:* | provider-native prefixes (`/_next/image`, `/_vercel/image`, `/.netlify/images`, `/_ipx/`, `/_image`, `/storage/v1/render/image/`) | Table-driven, see §12 |

## 2. Signing schemes at a glance

| Dialect | Scheme | Where | Spec |
|---|---|---|---|
| imgproxy | HMAC-SHA256(key, salt ‖ path-after-signature, starting with `/`), keys and salts hex-decoded, URL-safe base64 without padding, optionally truncated to `signature_size` bytes; multiple key/salt pairs; trusted signatures list | first path segment | https://docs.imgproxy.net/usage/signing_url |
| query | imaginary-compatible `sign` = base64url-nopad(HMAC-SHA256(key, path ‖ url.Values.Encode() of the query without `sign`)), i.e. keys sorted, no `?` separator | query | imaginary `middleware.go` `validateURLSignature` |
| imgix | `s` = hex(MD5(token ‖ path ‖ ("?" ‖ query-without-s if any))), must be last; optional `expires` (unix) | query | https://github.com/imgix/imgix-blueprint#securing-urls |
| cloudinary | `s--XXXXXXXX--` = first 8 chars of base64url(SHA-1(transformations/…/public_id ‖ api_secret)); 32 chars with SHA-256 "long" signatures | path segment | https://cloudinary.com/documentation/delivery_url_signatures |
| imagekit | `ik-s` = hex(HMAC-SHA1(private_key, url-relative-to-endpoint ‖ ik-t)), `ik-t` = expiry unix seconds (9999999999 = never) | query | https://imagekit.io/docs/media-delivery-basic-security |
| thumbor | HMAC-SHA1(SECURITY_KEY, path-after-signature **without** the leading `/`), URL-safe base64 **with** padding (28 chars, Python `urlsafe_b64encode`); `unsafe` when disabled | first path segment | https://thumbor.readthedocs.io/en/latest/security.html |
| bunny | token auth: `token` + `expires`; basic = MD5, advanced = SHA-256 of key ‖ path ‖ expires (‖ optional params), base64url | query | https://bunny.net/docs/cdn/security/token-authentication (exact byte layout to be verified against Bunny SDK code in the WP) |
| cloudflare `/cdn-cgi/image` | none upstream | — | https://developers.cloudflare.com/images/transform-images/transform-via-url/ |
| cloudflare-images | `sig` = hex(HMAC-SHA256(key, path ‖ "?exp=" ‖ exp)), `exp` | query | M2 (WP-79) |
| wsrv, simple:* | none upstream | — | — |

For dialects without a native scheme xi offers the optional **xi scheme**: query param `xi_sig` = base64url(HMAC-SHA256(key, salt ‖ path ‖ "?" ‖ query-without-xi_sig)) using the imgproxy key/salt pairs. Unsigned traffic is governed by `XI_SECURITY_UNSIGNED_POLICY` (`allow` | `restricted` | `deny`), settable per mount. `restricted` is the recommended production setting for client-generated URLs (unpic): only widths in `XI_SECURITY_ALLOWED_WIDTHS` (default the Next.js device+image sizes: 16,32,48,64,96,128,256,384,640,750,828,1080,1200,1920,2048,3840), heights derived from width and aspect ratio or in the same list, qualities in `XI_SECURITY_ALLOWED_QUALITIES` (default 1–100 step 5 plus named levels), and no watermark URLs, security options or `raw`. The exact restricted option set (an allowlist that fails closed on new fields; it also admits DPRs from a router-level list and rejects info requests, chained pipelines, effects, crops, fallback URLs, cache busters and width/DPR client hints) is documented on `router.Restrictions` in `internal/dialect/router`.

## 3. imgproxy (canonical)

Reference: https://docs.imgproxy.net/usage/processing (imgproxy v4.0.x docs, source `imgproxy/imgproxy-docs` `docs/usage/processing.mdx`), https://docs.imgproxy.net/usage/getting_info, https://docs.imgproxy.net/usage/encrypting_source_url, https://docs.imgproxy.net/features/chained_pipelines.

### 3.1 Grammar

```
/%signature/%options.../plain/%source_url[@%extension]
/%signature/%options.../%base64url_source_url[.%extension]      (may be split with "/")
/%signature/%options.../enc/%aes_cbc_source[.%extension]        (may be split with "/")
/info/%signature/%info_options.../{plain/…|%base64…|enc/…}

option      = name ":" arg *(":" arg)        ; separator configurable (XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR)
pipeline    = option *("/" option)
pipelines   = pipeline *("/-/" pipeline)       ; chained pipelines
bool        = "1" | "t" | "true" | "0" | "f" | "false"
```

- Signature segment always present; `unsafe`/`_`/anything when signing is off.
- Plain source: `%`, `?`, `@` must be percent-encoded; everything after `@` is the extension.
- `IMGPROXY_BASE_URL`-style prefixing: `XI_DIALECTS_IMGPROXY_BASE_URL` prepended to relative sources. `XI_DIALECTS_IMGPROXY_URL_REPLACEMENTS` supported (regex → replacement) for migrations.
- Source schemes: `http(s)://`, `s3://`, `gs://`, `abs://`, `local:///`, `ftp://`, `sftp://`, `xi://<source>/`. `swift://` is not supported (open question).
- Presets (`internal/preset`, expanded by the dialect at parse time): `preset:a:b` expands each preset in place, so later options override it argument by argument and it overrides earlier ones; the `default` preset is applied before the URL's options (in the first pipeline). A preset's first pipeline applies to the pipeline it is referenced from and its chained pipelines merge into the following ones (created when missing); a later URL `-` moves into them and URL options there win (imgproxy chained-pipelines docs example: `width:400/-/preset:test/width:500/-/width:600` with `test=width:300/height:300/-/width:200/height:200/-/width:100/height:200` → `width:400/-/width:500/height:300/-/width:600/height:200/-/width:100/height:200`). Presets may reference other presets; undefined references, cycles, expansions beyond 16 pipelines, bad arguments and unknown option names are startup errors (imgproxy skips recursive references at request time with a warning). An undefined preset in a URL is 400. `XI_PRESETS_ONLY`: the first path segment is a separator-joined preset list (`/thumbnail:blurry:watermarked/plain/…`), further segments containing the separator must also be preset lists or `pr:`/`preset:` options, and anything else (options, unknown names, `-`, no preset at all) is 403 `forbidden`.
- Chained pipelines: pipeline-scoped options (geometry, effects, watermark, animation) apply to the pipeline they appear in; request-scoped ones (`O.*`, `D.*`, `TR.Limits`, `raw`) apply to the whole request wherever they appear (imgproxy `options.Main()`). Max 16 pipelines; an empty pipeline is 400.
- Later occurrences of an option override earlier ones argument by argument (empty arguments keep the previous value); the source extension overrides `format` unless `raw`. An empty boolean is false; any other non-boolean is 400 (imgproxy: false). `sp` is accepted as a `skip_processing` alias because `imgproxy-js-core` emits it.
- Malformed option arguments → 400. Unknown option names are recorded in `TR.Ignored` and return 400 only with `XI_DIALECTS_STRICT=true` (ADR 0002). imgproxy itself returns 404 for both (`options/parser/errors.go`); see ROADMAP open question 3.

### 3.2 Processing options → TR

Aliases in parentheses. "Pro" marks options that are paid in imgproxy; all are free here.

| Option (aliases) | Args | TR field | Default | Milestone |
|---|---|---|---|---|
| `resize` (`rs`) | type:w:h:enlarge:extend(:gravity…) | `P.Resize.{Type,Width,Height,Enlarge}`, `P.Extend` | — | M1 |
| `size` (`s`) | w:h:enlarge:extend | same minus type | — | M1 |
| `resizing_type` (`rt`) | fit, fill, fill-down, force, auto | `P.Resize.Type` | fit | M1 |
| `resizing_algorithm` (`ra`) Pro | nearest, linear, cubic, lanczos2, lanczos3 | `P.Resize.Algorithm` | lanczos3 | M1 |
| `width` (`w`), `height` (`h`) | int | `P.Resize.Width/Height` | 0 | M1 |
| `min-width` (`mw`), `min-height` (`mh`) | int | `P.Resize.MinWidth/MinHeight` | 0 | M1 |
| `zoom` (`z`) | x[:y] | `P.Resize.ZoomX/ZoomY` | 1 | M1 |
| `dpr` | float | `P.Resize.DPR` | 1 | M1 |
| `enlarge` (`el`) | bool | `P.Resize.Enlarge` | false | M1 |
| `extend` (`ex`) | bool:gravity:x:y | `P.Extend` | false:ce:0:0 | M1 |
| `extend_aspect_ratio` (`extend_ar`, `exar`) | bool:gravity:x:y | `P.ExtendAR` | false | M1 |
| `gravity` (`g`) | no,so,ea,we,noea,nowe,soea,sowe,ce[:x:y]; `sm`; `fp:x:y`; `obj:classes…` Pro; `objw:class:weight…` Pro | `P.Gravity` | ce:0:0 | M1 (sm, fp), M3 (obj, objw) |
| `objects_position` (`obj_pos`, `op`) Pro | type:x:y, `fp:x:y`, `prop` | `P.Gravity` object placement | ce | M3 |
| `crop` (`c`) | w:h[:gravity] (abs ≥1, rel <1) | `P.Crop` | — | M1 |
| `crop_aspect_ratio` (`crop_ar`, `car`) Pro | ratio:enlarge | `P.Crop.AspectRatio` | 0 | M2 |
| `trim` (`t`) | threshold:color:equal_hor:equal_ver | `P.Trim` | — | M1 |
| `padding` (`pd`) | t:r:b:l (CSS shorthand) | `P.Padding` | — | M1 |
| `auto_rotate` (`ar`) | bool | `P.AutoRotate` | config (true) | M1 |
| `rotate` (`rot`) | multiple of 90 | `P.Rotate` | 0 | M1 |
| `flip` (`fl`) | h:v bools | `P.Flip` | false:false | M1 |
| `background` (`bg`) | r:g:b or hex | `P.Background` | none | M1 |
| `background_alpha` (`bga`) Pro | 0..1 | `P.Background.A` | 1 | M2 |
| `adjust` (`a`) Pro | brightness:contrast:saturation | `P.Adjust` | — | M2 |
| `brightness` (`br`) Pro | −255..255 | `P.Adjust.Brightness` | 0 | M2 |
| `contrast` (`co`) Pro | float | `P.Adjust.Contrast` | 1 | M2 |
| `saturation` (`sa`) Pro | float | `P.Adjust.Saturation` | 1 | M2 |
| `monochrome` (`mc`) Pro | intensity:color | `P.Monochrome` | 0:b3b3b3 | M2 |
| `duotone` (`dt`) Pro | intensity:c1:c2 | `P.Duotone` | 0:000000:ffffff | M2 |
| `blur` (`bl`) | sigma | `P.Blur` | off | M1 |
| `sharpen` (`sh`) | sigma | `P.Sharpen` | off | M1 |
| `pixelate` (`pix`) | size | `P.Pixelate` | off | M1 |
| `unsharp_masking` (`ush`) Pro | mode:weight:divider | `P.Unsharp` | config | M2 |
| `blur_areas` (`ba`) Pro | sigma:l:t:w:h… | `P.BlurAreas` | off | M2 |
| `progressive_blur` (`pbl`) Pro | sigma:direction:start:stop | `P.ProgressiveBlur` | off | M2 |
| `blur_detections` (`bd`) Pro | sigma:classes… | `P.BlurDetections` | off | M3 |
| `draw_detections` (`dd`) Pro | bool:classes… | `P.DrawDetections` | off | M3 |
| `crop_objects` (`c_obj`) Pro | scale:classes… | `P.CropObjects` | off | M3 |
| `colorize` (`col`) Pro | opacity:color:keep_alpha | `P.Colorize` | off | M2 |
| `gradient` (`gr`) Pro | opacity:color:direction:start:stop | `P.Gradient` | off | M2 |
| `watermark` (`wm`) | opacity:pos:x:y:scale; pos incl. `re` (tile), `ch` (chessboard, Pro) | `P.Watermarks[i]` | off | M1 (`re` M1, `ch` M3) |
| `watermark_url` (`wmu`) Pro | base64url URL | `Watermark.Source` | config image | M3 |
| `watermark_text` (`wmt`) Pro | base64url Pango markup | `Watermark.Text` | — | M3 |
| `watermark_size` (`wms`) Pro | w:h | `Watermark.Width/Height` | 0:0 | M3 |
| `watermark_rotate` (`wm_rot`, `wmr`) Pro | degrees | `Watermark.Rotate` | 0 | M3 |
| `watermark_shadow` (`wmsh`) Pro | sigma | `Watermark.Shadow` | off | M3 |
| `style` (`stl`) Pro | base64url CSS | `P.SVGStyle` | — | M2 |
| `strip_metadata` (`sm`) | bool | `O.StripMetadata` | config (true) | M1 |
| `keep_copyright` (`kcr`) | bool | `O.KeepCopyright` | config (true) | M1 |
| `dpi` Pro | int | `O.DPI` | 0 | M2 |
| `strip_color_profile` (`scp`) | bool | `O.StripColorProfile` | config (true) | M1 |
| `preserve_hdr` (`ph`) | bool | `O.PreserveHDR` | config (false) | M2 |
| `color_profile` (`cp`, `icc`) Pro | srgb, cmyk, file | `O.ColorProfile` | — | M2 |
| `enforce_thumbnail` (`eth`) | bool | `O.EnforceThumbnail` | config | M2 |
| `quality` (`q`) | 0..100 | `O.Quality` | 0 (config) | M1 |
| `format_quality` (`fq`) | fmt:q… | `O.FormatQuality` | — | M1 |
| `autoquality` (`aq`) Pro | method:target:min:max:allowed_error | `O.AutoQuality` | config | M2 |
| `max_bytes` (`mb`) | int | `O.MaxBytes` | 0 | M1 |
| `jpeg_options` (`jpgo`) Pro | progressive:no_subsample:trellis_quant:overshoot_deringing:optimize_scans:quant_table | `O.JPEG` | config | M2 |
| `png_options` (`pngo`) Pro | interlaced:quantize:quantization_colors | `O.PNG` | config | M2 |
| `webp_options` (`webpo`) Pro | compression:smart_subsample:preset | `O.WebP` | config | M2 |
| `avif_options` (`avifo`) Pro | subsample | `O.AVIF` | config | M2 |
| `format` (`f`, `ext`) | extension or `best` Pro | `O.Format` | source/negotiated | M1 (`best` M2) |
| `page` (`pg`) Pro | int | `P.Page` | 0 | M2 |
| `pages` (`pgs`) Pro | int | `P.Pages` | 1 | M2 |
| `disable_animation` (`da`) Pro | bool | `P.DisableAnimation` | false | M1 |
| `video_thumbnail_second` (`vts`) Pro | float | `P.Video.Second` | config | M3 |
| `video_thumbnail_keyframes` (`vtk`) Pro | bool | `P.Video.Keyframes` | config | M3 |
| `video_thumbnail_tile` (`vtt`) Pro | step:cols:rows:tw:th:extend:trim:fill:fx:fy | `P.Video.Tile` | off | M3 |
| `video_thumbnail_animation` (`vta`) Pro | step:delay:frames:fw:fh:extend:trim:fill:fx:fy | `P.Video.Animation` | off | M3 |
| `fallback_image_url` (`fiu`) Pro | base64url URL | `D.Fallback` | config | M2 |
| `skip_processing` (`skp`) | ext… | `D.SkipProcessing` | — | M1 |
| `raw` | bool | `TR.Kind = Raw` | false | M1 |
| `cachebuster` (`cb`) | string | `D.CacheBuster` | — | M1 |
| `bypass_cache` (`bc`) Pro | bool | skips result cache (needs `XI_CACHE_ALLOW_BYPASS`) | false | M2 |
| `cache_tags` (`ct`) Pro | tags… | `Cache-Tag`/`Surrogate-Key` header (`XI_CACHE_TAGS_HEADER`) | — | M2 |
| `expires` (`exp`) | unix ts | `D.Expires` | — | M1 |
| `filename` (`fn`) | name:encoded | `D.Filename` | source basename | M1 |
| `return_attachment` (`att`) | bool | `D.Attachment` | config | M1 |
| `preset` (`pr`) | names… | expanded in place | — | M1 |
| `hashsum` (`hs`) Pro | type:hex | `TR.Source.Hashsum` | none | M2 |
| `max_src_resolution` (`msr`), `max_src_file_size` (`msfs`), `max_animation_frames` (`maf`), `max_animation_frame_resolution` (`mafr`), `max_result_dimension` (`mrd`) | int | `TR.Limits` (only if security options allowed) | config | M1 |
| removed upstream: `gif_options` | — | accepted and ignored for old URLs | — | — |

### 3.3 Info options

`/info/…` supports: `size` (`s`), `format` (`f`), `dimensions` (`d`), `exif`, `iptc`, `xmp`, `video_meta` (`vm`) in M1/M3; Pro ones `detect_objects` (`do`), `classify` (`cl`) M3; `colorspace` (`cs`), `bands` (`b`), `sample_format` (`sf`), `pages_number` (`pn`), `alpha` (`a`), `crop` (`c`), `objects_position` (`op`), `palette` (`p`), `average` (`avg`), `dominant_colors` (`dc`), `blurhash` (`bh`), `thumbhash` (`th`), `phash` (`ph`), `calc_hashsums` (`chs`) in M2; plus `page`, `vts`, `vtk`, `cachebuster`, `expires`, `preset`, `hashsum`, `max_src_resolution`, `max_src_file_size`. JSON field names match imgproxy's.

### 3.4 Examples

```
/unsafe/rs:fill:300:400:0/g:sm/plain/https://example.com/a.jpg@webp
/AfrOrF3gWeDA6VOlDG4TzxMv39O7MXnF4CXpKUwGqRM/pr:sharp/rs:fill:300:400:0/g:sm/aHR0cDovL2V4YW1w/bGUuY29tL2ltYWdl/cy9jdXJpb3NpdHku/anBn.png
/sig/rs:fit:500:500/wm:0.5:nowe/-/wm:0.7:soea/plain/s3://bucket/key.jpg
/info/unsafe/s/f/d/bh:4:3/plain/https://example.com/a.jpg
```

## 4. Query dialect (native + imaginary)

Reference: https://github.com/h2non/imaginary (API section). Mount `/q/`.

Two shapes:

```
/q/?url=<source>&w=300&h=200&fit=cover&g=smart&f=webp&q=80&dpr=2&sign=…
/q/resize?url=<source>&width=300&height=200&type=webp          (imaginary endpoint)
```

Native keys (long and short): `url`, `width|w`, `height|h`, `fit` (`fit|contain|inside` → Fit, `fill|cover` → Fill, `fill-down`, `force|stretch` → Force, `outside`), `gravity|g` (compass words, `smart`, `entropy`, `fp:x:y`), `crop` (`w,h[,x,y]`), `dpr`, `enlarge`, `format|f`, `quality|q`, `lossless`, `progressive`, `blur`, `sharpen`, `pixelate`, `rotate|rot`, `flip` (`h|v|hv`), `bg`, `strip`, `trim`, `pad`, `preset`, `page`, `cb`, `filename`, `attachment`, `expires`, `sign`. Semantics are those of the equivalent imgproxy option; anything imgproxy can do is reachable via `o=<imgproxy option path>` (for example `o=rs:fill:300:200/g:sm`) so the query dialect is complete by construction.

imaginary endpoints: `resize`, `enlarge`, `extract`, `crop`, `smartcrop`, `rotate`, `autorotate`, `flip`, `flop`, `thumbnail`, `zoom`, `convert`, `fit`, `blur`, `watermark` (text, M3), `watermarkimage` (M3), `info`, `pipeline` (JSON `operations` → chained pipelines, M2). imaginary params: `width`, `height`, `areawidth`, `areaheight`, `quality`, `compression`, `type`, `force`, `rotate`, `embed`, `norotation`, `noprofile`, `stripmeta`, `flip`, `flop`, `extend`, `background`, `gravity`, `top`, `left`, `factor`, `dpi`, `interlace`, `aspectratio`, `palette`, `sigma`, `minampl`, `url`. Watermark params (M3): `text`, `font`, `textwidth`, `opacity`, `margin`, `color`, `noreplicate`, `image` (watermark image URL). Also accepted: `nocrop`, `colorspace`, `operations` (JSON, `pipeline` endpoint). Unsupported: POST/multipart uploads (form field `file`), the `file` query param for local paths (use an `fs` source with `xi://` instead), `speed`. Params verified against `.refs/imaginary/params.go` and `server.go`.

Implementation notes (WP-23, `internal/dialect/query`): native keys are applied in query order through the imgproxy option parser, so a later key overrides an earlier one and keys after an `o=` that chains pipelines (`-`) apply to the last pipeline; `o=` must contain options only (a source in it is a 400). Multi-argument native values (`crop`, `pad`, `trim`, `bg` as `r,g,b`, `preset`, `g=fp:x:y`) accept `,` or `:` as separator, and `crop=w,h,x,y` places the top-left corner. Signing: `sign` covers the decoded path after the mount (`/resize`, or `/` for native mode) plus the sorted query; `xi_sig` covers the escaped path after the mount plus the query; both present is a 403. imaginary params are stricter than upstream (malformed or negative values are a 400 instead of silently falling back); `extend=mirror` is approximated as `copy`, `/extract` crops before resizing, `/zoom` scales by `factor+1` like bimg, `/info` uses the imgproxy info schema, and text/image watermarks are parsed but take effect in M3.

## 5. imgix

Reference: https://docs.imgix.com/apis/rendering; unpic transformer `ascorbic/unpic` `src/providers/imgix.ts` (defaults `fit=min`, `auto=format`).

```
https://images.example.com/photos/a.jpg?w=400&h=300&fit=crop&crop=faces,entropy&auto=format,compress&q=60&dpr=2&s=…
/imgix/<mount>/photos/a.jpg?w=400                     (prefix mode)
/imgix/<mount>/https%3A%2F%2Forigin.com%2Fa.jpg?w=400  (web-proxy source, must be signed upstream; here: allowlist + policy)
```

Grammar: path = object key relative to mount source; query params; any param may be base64url-encoded by appending `64` (`txt64`, `mark64`).

| imgix | TR | Notes |
|---|---|---|
| `w`, `h` | Resize.Width/Height (value in (0,1) → relative) | M1-level semantics in M2 |
| `max-w`, `max-h`, `min-w`, `min-h` | clamp before planning | |
| `fit=clip` (default) | Fit + Enlarge | imgix clip does upscale when both dims set |
| `fit=max` / `fit=min` | Fit, no enlarge / Fill-down | `min` = crop without upscaling |
| `fit=crop` | Fill; gravity from `crop` | |
| `fit=scale` | Force | |
| `fit=fill`, `fill=solid|blur`, `fill-color` | Fit + Extend + Background; `fill=blur` M3 | |
| `fit=fillmax` | Fit no enlarge + Extend | |
| `fit=facearea`, `facepad`, `faceindex` | M3 face crop | |
| `fit=clamp` | Partial: Fit + Extend with edge replication (vips `embed extend=copy`) | |
| `crop=top,bottom,left,right` | compass Gravity | combinations → corner |
| `crop=faces` | Face gravity (M3; falls back to Smart) | |
| `crop=entropy` / `edges` | Entropy / Smart (attention) | |
| `crop=focalpoint`, `fp-x`, `fp-y`, `fp-z` | FocusPoint (+ FocusZoom) | |
| `ar=16:9` | derive missing dimension (with fit=crop) | |
| `rect=x,y,w,h` | Crop (pre-resize, px or relative) | |
| `dpr` | Resize.DPR | |
| `q` | Quality | |
| `fm=jpg|pjpg|png|png8|png32|webp|avif|jxl|gif|json|blurhash|mp4|webm` | Format; `pjpg` → Progressive; `png8` → PNG quantize 256; `json` → Info; `blurhash` → Info blurhash (text); `mp4/webm` M3 | `jp2`, `jxr` unsupported → 422 |
| `auto=format` | negotiate (Accept) | |
| `auto=compress` | Quality default 45 when `q` absent; PNG → quantize | |
| `auto=enhance`, `auto=redeye` | Unsupported | |
| `lossless` | Lossless | |
| `chromasub=420|422|444` | ChromaSubsampling | |
| `cs=srgb|strip|tinysrgb|adobergb1998` | ColorProfile / StripColorProfile; `tinysrgb` → srgb | |
| `dpi` | DPI | |
| `bg` (RGB, ARGB, RRGGBB, AARRGGBB, named) | Background | |
| `pad`, `pad-top/right/bottom/left` | Padding | |
| `border=w,color`, `border-radius`, `border-radius-inner` | Border, Radius | M2 |
| `blur` (0–2000) | Blur sigma = v/20 | |
| `px` | Pixelate | |
| `sharp` (0–100) | Sharpen sigma = 0.5 + v/50 | |
| `usm`, `usmrad` | Unsharp | M2 |
| `bri`, `con`, `exp`, `gam`, `sat`, `hue`, `vib`, `high`, `shad` | Adjust (−100..100 → multipliers); `high`/`shad` unsupported | M2 |
| `sepia`, `monochrome`, `duotone`, `duotone-alpha`, `invert` | Sepia/Monochrome/Duotone/Negate | M2 |
| `orient` (1–8, 90, 180, 270) | Rotate/Flip | |
| `rot` (any) | RotateAny | |
| `flip=h|v|hv` | Flip | |
| `trim=auto|color`, `trim-color`, `trim-tol`, `trim-md`, `trim-sd` | Trim (tolerance mapped to threshold) | |
| `mark`, `mark-alpha`, `mark-align`, `mark-pad`, `mark-scale`, `mark-w`, `mark-h`, `mark-x`, `mark-y`, `mark-fit`, `mark-tile` | Watermark from URL | M3 |
| `txt`, `txt-font`, `txt-size`, `txt-color`, `txt-align`, `txt-pad`, `txt-shad` | Text watermark (Pango) | M3 |
| `blend*` | Unsupported (only `blend-mode=normal` via watermark) | |
| `page`, `frame` | Page | M2 |
| `dl` | Filename + Attachment | |
| `ch=Width,DPR,Save-Data` | enable client hints for this URL | |
| `expires` | Expires | |
| `s` | signature | |
| AI/ML: `bg-remove`, `bg-replace`, `upscale`, `nr`, `nrs`, `fill=gen`, `palette`, `colorquant`, `faces` debug, `fp-debug`, `htn` halftone | Unsupported | |

## 6. Cloudinary

Reference: https://cloudinary.com/documentation/transformation_reference, https://cloudinary.com/documentation/delivery_url_signatures; unpic `cloudinary.ts` (defaults `f_auto,c_lfill`).

```
/<cloud>/image/upload/c_fill,g_auto,w_400,h_300/q_auto/f_auto/v1712345678/folder/sample.jpg
/<cloud>/image/upload/s--iDy_JeBq--/c_fill,w_300,h_250/e_grayscale/sample.png
/<cloud>/image/fetch/w_400,f_auto/https://example.com/a.jpg
```

Grammar: `/<cloud>/<asset_type>/<delivery_type>/[s--SIG--/][<tx>/]*[v<digits>/]<public_id>[.<ext>]`. When host-bound as a private CDN or custom domain (`<cloud>-res.cloudinary.com`-style, unpic `privateRegex`), the `<cloud>/` segment is absent and the cloud comes from the mount. `asset_type` = `image` (M1/M2) or `video` (thumbnails M3). `delivery_type` = `upload` (key under the cloud's bound source) or `fetch` (absolute URL, allowlisted); `private`/`authenticated` treated as `upload` requiring a signature. Each `<tx>` component is `key_value(,key_value)*` → one Pipeline; multiple components → chained pipelines. `<cloud>` maps to a named source via `dialects.cloudinary.clouds`. Extension sets `O.Format` unless `f_` given.

| Cloudinary | TR | Notes |
|---|---|---|
| `w_`, `h_` (int, float ≤1.0 relative, `w_auto[:step]` client hints) | Width/Height | |
| `c_scale` | Force if both dims else Fit + Enlarge | |
| `c_fit` / `c_limit` / `c_mfit` | Fit+Enlarge / Fit / Outside (min) | |
| `c_fill` / `c_lfill` | Fill+Enlarge / FillDown | |
| `c_fill_pad` | Fill with Smart gravity, Extend if needed | partial |
| `c_pad` / `c_lpad` / `c_mpad` | Fit+Extend(+Enlarge) / no enlarge / no scale | |
| `c_crop` | Crop region (x,y,w,h or gravity), no scaling | |
| `c_thumb` | Fill + Face gravity + zoom `z_` | faces M3 |
| `c_auto`, `c_auto_pad` | Fill + Smart / Fit + Extend | |
| `c_imagga_*` | Unsupported | |
| `g_` compass (`north_east`…), `center` | Gravity | |
| `g_auto`, `g_auto:subject`, `g_auto:classic` | Smart | |
| `g_face`, `g_faces`, `g_face:center` | Face (M3; fallback Smart) | |
| `g_<object>` (e.g. `g_cat`) | Object | M3 |
| `g_xy_center` + `x_`,`y_` | FocusPoint | |
| `ar_16:9` / `ar_1.5` | derive dimension | |
| `x_`, `y_` | crop offset / gravity offset | |
| `z_` | FocusZoom / Zoom | |
| `q_<n>`, `q_auto[:best|good|eco|low]` | Quality / AutoQuality presets (dssim targets) | M2 |
| `f_auto`, `f_<fmt>` (jpg, png, webp, avif, jxl, gif, heic, tiff, bmp, ico, mp4, webm) | Format / negotiate | video out M3 |
| `dpr_<float>`, `dpr_auto` | DPR / client hints | |
| `a_<deg>`, `a_auto`, `a_hflip`, `a_vflip`, `a_ignore` | Rotate/RotateAny/Flip/AutoRotate=false | |
| `b_<color>` (`rgb:ff0000`, named) | Background | `b_auto`, `b_blurred`, `b_gen_fill` unsupported |
| `bo_4px_solid_rgb:000` | Border | M2 |
| `r_<n>`, `r_max` | Radius | M2 |
| `o_<0-100>` | image opacity (alpha multiply) | M2 |
| `e_blur[:n]`, `e_pixelate[:n]`, `e_sharpen[:n]`, `e_unsharp_mask[:n]` | Blur (n/100×σ scale), Pixelate, Sharpen, Unsharp | |
| `e_grayscale`, `e_sepia`, `e_negate`, `e_brightness:n`, `e_contrast:n`, `e_saturation:n`, `e_gamma:n`, `e_hue:n`, `e_colorize:n,co_<color>`, `e_trim[:tol]` | corresponding Pipeline fields | M2 |
| `e_blur_faces`, `e_pixelate_faces`, `e_blur_region` | BlurDetections(face) / BlurAreas | M3/M2 |
| other `e_*` (AI, artistic, `background_removal`, `gen_*`, `upscale`, `vectorize`) | Unsupported | |
| `fl_progressive`, `fl_lossy`, `fl_attachment[:name]`, `fl_keep_iptc`, `fl_keep_attribution`, `fl_force_strip`, `fl_strip_profile`, `fl_animated`, `fl_awebp`, `fl_apng`, `fl_png8`, `fl_immutable_cache`, `fl_getinfo` | Output/Delivery flags; `fl_getinfo` → Info | |
| `pg_<n>` | Page | M2 |
| `t_<name>` | preset `<name>` | |
| `d_<public_id>` | Fallback (same cloud source) | M2 |
| `l_<id>`, `l_fetch:<b64url>`, `l_text:<font>:<text>` + `fl_layer_apply`, `g_`, `x_`, `y_`, `o_` | Watermark (image / URL / text) | M3 |
| `u_` underlays, `if_`, `$var`, `fn_`, `vc_`/`du_`/`so_`/`eo_` video params | Unsupported (video `so_` → Video.Second in M3) | |

## 7. ImageKit

Reference: https://imagekit.io/docs/transformations, https://imagekit.io/docs/media-delivery-basic-security; unpic `imagekit.ts` (defaults `c-maintain_ratio,fo-auto`).

```
https://ik.example.com/tr:w-400,h-300,fo-auto/path/a.jpg
https://ik.example.com/path/a.jpg?tr=w-400,h-300:rt-90&ik-t=1767225600&ik-s=…
/imagekit/<mount>/tr:w-400/path/a.jpg
```

Grammar: `key-value` params, `,` separated; `:` chains steps (→ chained pipelines); `tr:` path segment anywhere before the file, or `tr` query param. `n-<name>` named transform → preset.

| ImageKit | TR |
|---|---|
| `w`, `h` (px, `<1` relative, `auto` client hints) | Width/Height |
| `ar-4-3` | aspect ratio |
| `c-maintain_ratio` (default) | Fill + Enlarge |
| `c-force` / `c-at_max` / `c-at_max_enlarge` / `c-at_least` | Force / Fit / Fit+Enlarge / Outside |
| `cm-pad_resize` (+ `bg`) | Fit + Extend + Background |
| `cm-extract` / `cm-pad_extract` | Crop region (no scale) / + Extend |
| `fo-center|top|left|bottom|right|top_left|…` | Gravity |
| `fo-auto` | Smart |
| `fo-face`, `fo-<object>` | Face/Object (M3) |
| `x`, `y`, `xc`, `yc` | crop offsets / focus point |
| `z` | FocusZoom |
| `q` | Quality |
| `f-auto|jpg|png|webp|avif|gif|jxl` | Format |
| `lo-true`, `pr-true`, `md-true`, `cp-true` | Lossless, Progressive, keep metadata, keep copyright |
| `bl-<1..100>` | Blur σ = v/2 |
| `e-sharpen[-n]`, `e-usm-…`, `e-grayscale`, `e-contrast`, `e-gradient` | Sharpen, Unsharp, Grayscale, Adjust, Gradient (M2) |
| `rt-<deg>|auto`, `fl-h|v|h_v` | Rotate/RotateAny, Flip |
| `r-<n>|max` | Radius (M2) |
| `bg-<hex>` | Background (`bg-blurred`, `bg-dominant` unsupported) |
| `b-<w>_<color>` | Border (M2) |
| `dpr` | DPR |
| `t-true|<threshold>` | Trim |
| `di-<path>` | Fallback |
| `orig-true` | Raw |
| `l-image`, `l-text`, `i-`, `lx`, `ly`, `lfo`, `l-end` | Watermarks (M3) |
| `e-bgremove`, `e-removedotbg`, `e-upscale`, `e-genvar`, `e-dropshadow`, `e-shadow`, `e-retouch`, `l-video`, `lm-*` | Unsupported |
| `ik-s`, `ik-t` | signature, Expires |

## 8. Cloudflare Images (`/cdn-cgi/image/`)

Reference: https://developers.cloudflare.com/images/transform-images/transform-via-url/; unpic `cloudflare.ts` (defaults `f=auto,fit=cover`).

```
/cdn-cgi/image/width=400,height=300,fit=cover,f=auto,q=75/uploads/a.jpg
/cdn-cgi/image/w=400,g=face,onerror=redirect/https://other.example.com/a.jpg
```

Grammar: `/cdn-cgi/image/<k=v(,k=v)*>/<source>`; source is a path relative to the mount source (default: the request host's configured origin) or an absolute (percent-encoded if it has `?`) URL subject to the allowlist.

| Cloudflare | TR |
|---|---|
| `width`/`w`, `height`/`h` (`width=auto` → client hints) | Width/Height |
| `fit=scale-down` / `contain` / `cover` / `crop` / `pad` / `squeeze` / `scale-up` | Fit / Fit+Enlarge / Fill+Enlarge / FillDown / Fit+Extend+Enlarge / Force / Fit+Enlarge only-if-smaller |
| `fit=aspect-crop` | Fill (crop to aspect, no scale) — partial |
| `gravity`/`g` = `auto` / `face` / `left|right|top|bottom` / `0.5x0.3` | Smart / Face (M3) / compass / FocusPoint |
| `zoom` (face-zoom) | FocusZoom (M3) |
| `quality`/`q` = 1–100 or `high|medium-high|medium-low|low` | Quality (90/80/65/50) |
| `format`/`f` = `auto|avif|webp|jpeg|baseline-jpeg|png|gif|json` | negotiate / Format / Progressive=false / Info |
| `dpr` | DPR |
| `anim=false` | DisableAnimation |
| `background` (`%23RRGGBB`, CSS names, `rgb()`, `rgba()`) | Background |
| `blur` (1–250) | Blur σ = v/2 |
| `sharpen` (0–10) | Sharpen σ = v/4 (M1) |
| `brightness`, `contrast`, `gamma`, `saturation` (multipliers) | Adjust (M2) |
| `rotate=90|180|270`, `flip=h|v|hv` | Rotate, Flip |
| `trim=border` or `trim.top/right/bottom/left/width/height` | Trim / Crop |
| `border=color;width` (and per side) | Border (M2) |
| `metadata=keep|copyright|none` | StripMetadata / KeepCopyright |
| `onerror=redirect` | OnErrorRedirect (302 to source) |
| `compression=fast` | lower encoder effort |
| `slow-connection-quality`/`scq` | Quality when `Save-Data`/`ECT` hints indicate slow network (M2) |
| `segment=foreground`, `upscale=generate` | Unsupported |

Cloudflare Images delivery URLs (`/cdn-cgi/imagedelivery/<account_hash>/<image_id>/<variant|k=v,…>`, `imagedelivery.net`): M2 (WP-79, needed for the unpic `cloudflare_images` provider), `variant` → preset, `image_id` → key in a bound source, `sig`/`exp` signing.

## 9. Bunny Optimizer

Reference: https://bunny.net/docs/stream-image-processing (Dynamic Image API); unpic `bunny.ts` (sets `aspect_ratio` when w and h given; `format` → `output`).

```
https://cdn.example.com/a.jpg?width=400&aspect_ratio=16:9&quality=80&output=webp
/bunny/<mount>/a.jpg?width=400&crop_gravity=north&token=…&expires=…
```

| Bunny | TR |
|---|---|
| `width`, `height` | Width/Height (Fit + no enlarge when one dim; Fill when both) |
| `aspect_ratio=16:9` | Fill to aspect with `crop_gravity` |
| `crop=w,h[,x,y]` | Crop |
| `crop_gravity=center|north|south|east|west|northeast|…|top_left…` | Gravity |
| `face_crop` | Face (M3) |
| `quality` | Quality |
| `output`/`format` = `webp|jpeg|png|gif|avif` | Format |
| `auto_optimize=low|medium|high` | negotiate + Quality 85/75/65 |
| `sharpen=true` | Sharpen σ 1 |
| `blur` (0–100) | Blur σ = v/4 |
| `flip`, `flop` | Flip V / H |
| `rotate` (90 multiples) | Rotate |
| `brightness`, `contrast`, `saturation`, `gamma` (−100..100), `hue` (0–100), `sepia` | Adjust / Sepia (M2) |
| `class=<name>` | preset |
| `optimizer=image` | ignored (meaning is implicit) |
| `token`, `expires` | signature |

## 10. Thumbor

Reference: https://thumbor.readthedocs.io/en/latest/usage.html, https://thumbor.readthedocs.io/en/latest/filters.html, security page above.

```
/thumbor/unsafe/300x200/smart/filters:format(webp):quality(80)/https://example.com/a.jpg
/thumbor/<hmac>/trim/10x20:310x220/fit-in/-300x0/left/top/filters:blur(3)/s3-key/a.jpg
```

Grammar:

```
/<hmac|unsafe>/[meta/][trim[:top-left|:bottom-right][:tolerance]/][AxB:CxD/]
  [fit-in/|full-fit-in/|adaptive-fit-in/|adaptive-full-fit-in/][-][W|orig]x[-][H|orig]/
  [left|center|right/][top|middle|bottom/][smart/][filters:name(args)[:name(args)…]/]<image>
```

| Thumbor | TR |
|---|---|
| `WxH` (default) | Fill + Enlarge |
| `fit-in` / `full-fit-in` | Fit / Outside |
| `adaptive-*` | swap W/H when orientation differs |
| `-W`, `-H` | Flip H / V |
| `0`, `orig` | auto / source dimension |
| `AxB:CxD` | Crop (pre-resize, px) |
| `trim[:corner][:tol]` | Trim (color from corner, threshold) |
| halign/valign | Gravity compass |
| `smart` | Smart (M3: faces first) |
| `meta` | Info (thumbor JSON shape adapter) |
| `quality(n)`, `format(webp|jpeg|png|gif|avif|heic)` | Quality, Format |
| `blur(r[,σ])`, `sharpen(amount,radius,luma_only)` | Blur σ, Unsharp |
| `brightness(n)`, `contrast(n)`, `saturation(f)`, `rgb(r,g,b)`, `grayscale()`, `equalize()` | Adjust / Grayscale; `rgb`, `equalize` partial (M2) |
| `rotate(deg)` | Rotate/RotateAny |
| `round_corner(r[|r2],r,g,b[,transparent])` | Radius (M2) |
| `background_color(hex)`, `fill(color|auto|blur|transparent[,fill_transparent])` | Background / Extend; `auto`, `blur` M3 |
| `no_upscale()`, `upscale()`, `stretch()`, `proportion(f)` | Enlarge=false, Enlarge, Force, Zoom |
| `strip_exif()`, `strip_icc()` | StripMetadata, StripColorProfile |
| `max_bytes(n)`, `max_age(s)` | MaxBytes, TTL |
| `focal(AxB:CxD)`, `extract_focal()` | FocusPoint (rect centre) |
| `watermark(url,x,y,alpha[,w_ratio,h_ratio])` | Watermark from URL (M3) |
| `autojpg()` | Format JPEG when no alpha |
| `convolution`, `noise`, `red_eye`, `curve`, `cover` | Unsupported |

`<image>` resolves like imgproxy plain sources (absolute URL or key relative to the mount source).

## 11. weserv / wsrv.nl

Reference: https://wsrv.nl/docs/quick-reference; unpic `wsrv.ts` (default `fit=cover`; `url` without scheme is `https://`).

```
/?url=example.com/a.jpg&w=300&h=300&fit=cover&a=attention&output=webp&q=80
/wsrv/?url=ssl:example.com/a.jpg&w=50%25&filt=greyscale&default=example.com/404.png
```

| wsrv | TR |
|---|---|
| `url` (scheme optional, legacy `ssl:` prefix) | Source |
| `w`, `h` (px or `%`) | Width/Height |
| `dpr` (1–8) | DPR |
| `fit=inside|outside|cover|fill|contain` | Fit / Outside / Fill / Force / Fit+Extend |
| `cbg` | Background for contain |
| `we` | Enlarge=false (default enlarges) |
| `a=center|top|…|top-left…|entropy|attention|focal`, `fpx`, `fpy` | Gravity (Entropy, Smart, FocusPoint) |
| `crop=w,h,x,y`, `precrop` | Crop pre- or post-resize |
| `trim`, `tbg` | Trim |
| `mask=circle|ellipse|triangle|triangle-180|pentagon|pentagon-180|hexagon|square|star|heart`, `mtrim`, `mbg` | Mask (M2) |
| `flip`, `flop` | Flip V / H |
| `ro`, `rbg` | Rotate/RotateAny + Background |
| `bg` | Background |
| `blur` (0.3–1000 σ) | Blur |
| `sharp` (σ) | Sharpen |
| `con`, `sat`, `hue`, `gam`, `mod=b,s,h`, `tint`, `filt=greyscale|sepia|duotone|negate`, `start`, `stop` | Adjust / Tint / filters (M2) |
| `output=jpg|png|gif|tiff|webp|avif|json` | Format / Info |
| `q`, `l` (zlib 0–9), `ll`, `af`, `il` | Quality, PNG compression, Lossless, adaptive filter, Progressive |
| `n` (−1 all), `page` | Pages, Page |
| `default` | Fallback (302 redirect, like wsrv) |
| `filename`, `maxage` (`31d`, `1w`…) | Filename, TTL |
| `encoding=base64` | DataURL response (M2) |

## 12. unpic long tail ("simple" micro-dialects)

unpic (https://unpic.pics/providers/, source `ascorbic/unpic/src/providers/*.ts`) supports 28 providers. The ones above are full dialects. The rest only emit width, height, quality, format and a fit/crop hint, so they are implemented as **data**: one `simple` parser engine plus a table per provider (path pattern, source extraction, key map, value maps, defaults). Adding a provider is a table entry and a test file.

| Provider | URL shape xi must accept | Keys |
|---|---|---|
| Next.js / Vercel | `/_next/image?url=&w=&q=`, `/_vercel/image?url=&w=&q=` | w, q (format by Accept) |
| Netlify | `/.netlify/images?url=&w=&h=&fit=&position=&fm=&q=` | fit contain/cover/fill; fm incl. blurhash |
| IPX (Nuxt) | `/_ipx/<k_v,…|_>/<src>` | w,h,s=WxH,q,f,fit,pos,b,blur,sharpen,rotate,flip,flop,grayscale,extract |
| Astro | `/_image?href=&w=&h=&q=&f=&fit=` | |
| Contentful | `<path>?w=&h=&fm=&q=&fit=pad|fill|scale|crop|thumb&f=<focus>&r=&bg=&fl=progressive|png8` | |
| Kontent.ai | `?w=&h=&fm=&q=&fit=crop|clip|scale&rect=&fp-x=&fp-y=&dpr=&lossless=&auto=format` | imgix-like |
| Directus | `/assets/<id>?width=&height=&quality=&format=&fit=cover|contain|inside|outside&withoutEnlargement=` | |
| WordPress / Jetpack Photon | `?w=&h=&crop=1&resize=w,h&fit=w,h&quality=&strip=all` | |
| Supabase | `/storage/v1/render/image/public/<bucket>/<path>?width=&height=&quality=&resize=cover|contain|fill&format=origin` | |
| Storyblok | `<asset>/m/<W>x<H>[/smart]/filters:format(webp):quality(80):focal(…)` | thumbor subset, reuses thumbor filter parser |
| Uploadcare | `/<uuid>/-/resize/WxH/-/format/webp/-/quality/smart/` | |
| Adobe Scene7 | `?wid=&hei=&qlt=&fmt=&fit=` | |
| ImageEngine | `?imgeng=/w_300/h_200/f_webp/cmpr_…/m_cropbox` | |
| Cloudimage | `?w=&h=&q=&force_format=&func=crop|fit|cropfit|bound|cover|face` | |
| KeyCDN | `?width=&height=&quality=&format=&fit=` | |
| Builder.io | `?width=&height=&format=&quality=&fit=` | |
| Contentstack | `?width=&height=&quality=&format=&auto=webp&disable=upscale&fit=bounds|crop` | |
| Hygraph | `/<envId>/resize=fit:crop,width:W,height:H/{auto_image\|output=format:webp}/<handle>` (host `<region>.graphassets.com`) | fit crop/clip/scale/max |
| Appwrite | `/storage/buckets/<b>/files/<f>/preview?width=&height=&quality=&output=&gravity=` | |
| Shopify | `?width=&height=&crop=center` and legacy `_WxH` / `_crop_center` filename suffixes | |

Because unpic generates these URLs client-side, simple dialects default to `XI_SECURITY_UNSIGNED_POLICY=restricted`.

## 13. Compatibility test strategy

Each dialect WP ships (a) a table of URLs → expected TR JSON, (b) URLs produced by the upstream SDK/transformer (`@imgix/js-core`, `cloudinary` npm, `imagekit-javascript`, `unpic` transformers, `libthumbor`, imgproxy URL examples) generated by a small Bun script in `tools/compat/` and committed as fixtures, (c) signature test vectors from upstream docs, (d) a fuzz target that must never panic.
