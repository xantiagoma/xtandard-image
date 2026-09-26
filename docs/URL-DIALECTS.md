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
| imgix | `s` = hex(MD5(token ‖ path ‖ ("?" ‖ query-without-s if any))), token used verbatim; imgix puts `s` last, xi accepts it anywhere (same signed bytes); optional `expires` (unix) | query | https://github.com/imgix/imgix-blueprint#securing-urls |
| cloudinary | `s--XXXXXXXX--` = first 8 chars of base64url(SHA-1(transformations/…/public_id ‖ api_secret)); 32 chars with SHA-256 "long" signatures | path segment | https://cloudinary.com/documentation/delivery_url_signatures |
| imagekit | `ik-s` = hex(HMAC-SHA1(private_key, url-relative-to-endpoint (path ["?" query without ik-s/ik-t]) ‖ ik-t)), `ik-t` = expiry unix seconds; absent = 9999999999, which is signed but not written | query | https://imagekit.io/docs/media-delivery-basic-security |
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
- Presets (`internal/preset`, expanded by the dialect at parse time): `preset:a:b` expands each preset in place, so later options override it argument by argument and it overrides earlier ones; the `default` preset is applied before the URL's options (in the first pipeline). A preset's first pipeline applies to the pipeline it is referenced from and its chained pipelines merge into the following ones (created when missing); a later URL `-` moves into them and URL options there win (imgproxy chained-pipelines docs example: `width:400/-/preset:test/width:500/-/width:600` with `test=width:300/height:300/-/width:200/height:200/-/width:100/height:200` → `width:400/-/width:500/height:300/-/width:600/height:200/-/width:100/height:200`). Presets may reference other presets; undefined references, cycles, expansions beyond 16 pipelines, bad arguments and unknown option names are startup errors (imgproxy skips recursive references at request time with a warning). An undefined preset in a URL is 400. Presets apply to processing URLs only: in `/info` URLs (imgproxy resolves those against separate info presets, not implemented) `preset` is recorded but not expanded, and neither the `default` preset nor only-presets mode applies. `XI_PRESETS_ONLY`: the first path segment is a separator-joined preset list (`/thumbnail:blurry:watermarked/plain/…`), further segments containing the separator must also be preset lists or `pr:`/`preset:` options, and anything else (options, unknown names, `-`, no preset at all) is 403 `forbidden`.
- Chained pipelines: pipeline-scoped options (geometry, effects, watermark, animation) apply to the pipeline they appear in; request-scoped ones (`O.*`, `D.*`, `TR.Limits`, `raw`) apply to the whole request wherever they appear (imgproxy `options.Main()`). Max 16 pipelines; an empty pipeline is 400. Each pipeline runs on the result of the previous one and is planned on its real size, frames and alpha (after a trim too); EXIF orientation, shrink-on-load, `page`/`pages` and `disable_animation` act in the first pipeline only, and every pipeline has its own filters, extend, padding, background and watermarks (WP-58).
- Later occurrences of an option override earlier ones argument by argument (empty arguments keep the previous value); the source extension overrides `format` unless `raw`. An empty boolean is false; any other non-boolean is 400 (imgproxy: false). `sp` is accepted as a `skip_processing` alias because `imgproxy-js-core` emits it.
- Malformed option arguments → 400. Unknown option names are recorded in `TR.Ignored` and return 400 only with `XI_DIALECTS_STRICT=true` (ADR 0002). imgproxy itself returns 404 for both (`options/parser/errors.go`); see ROADMAP open question 3.
- Options tagged M2/M3 below are fully parsed and validated into TR and also recorded in `TR.Ignored` (reason "takes effect in M2"/"M3") until the executor implements them, so the server can ignore or 501 them. Strict mode rejects only unknown names.

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
| `objects_position` (`obj_pos`, `op`) Pro | type:x:y, `fp:x:y`, `prop` | `P.Gravity.ObjectsPosition` (kept when `gravity` changes) | ce | M3 |
| `crop` (`c`) | w:h[:gravity] (abs ≥1, rel <1) | `P.Crop` | — | M1 |
| `crop_aspect_ratio` (`crop_ar`, `car`) Pro | ratio:enlarge | `P.Crop.AspectRatio/AspectEnlarge` (whole image without `crop`) | 0 | M2 |
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
| `progressive_blur` (`pbl`) Pro | sigma:direction:start:stop; direction as `gradient` | `P.ProgressiveBlur` | off (…:down:0:1) | M2 |
| `blur_detections` (`bd`) Pro | sigma:classes… | `P.BlurDetections` | off | M3 |
| `draw_detections` (`dd`) Pro | bool:classes… | `P.DrawDetections` | off | M3 |
| `crop_objects` (`c_obj`) Pro | scale:classes… | `P.CropObjects` | off | M3 |
| `colorize` (`col`) Pro | opacity:color:keep_alpha | `P.Colorize` | off | M2 |
| `gradient` (`gr`) Pro | opacity:color:direction:start:stop; direction `down`/`up`/`left`/`right` or degrees | `P.Gradient` | 0:000:down:0:1 | M2 |
| `watermark` (`wm`) | opacity:pos:x:y:scale; pos incl. `re` (tile), `ch` (chessboard, Pro) | `P.Watermarks[i]` | off | M1 (`re` M1, `ch` M3) |
| `watermark_url` (`wmu`) Pro | base64url URL | `Watermark.Source` | config image | M3 |
| `watermark_text` (`wmt`) Pro | base64url Pango markup | `Watermark.Text` (replaces `watermark_url`) | — | M3 |
| `watermark_size` (`wms`) Pro | w:h | `Watermark.Width/Height` | 0:0 | M3 |
| `watermark_rotate` (`wm_rot`, `wmr`) Pro | degrees | `Watermark.Rotate` | 0 | M3 |
| `watermark_shadow` (`wmsh`) Pro | sigma | `Watermark.Shadow` | off | M3 |
| `style` (`stl`, `st`) Pro | base64url CSS | `P.SVGStyle` | — | M2 |
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
| `video_thumbnail_tile` (`vtt`) Pro | step:cols:rows:tw:th:extend:trim:fill:fx:fy; step 0 = off, < 0 = from duration (stored as 0) | `P.Video.Tile` | off | M3 |
| `video_thumbnail_animation` (`vta`) Pro | step:delay(ms):frames:fw:fh:extend:trim:fill:fx:fy; step as `vtt` | `P.Video.Animation` (delay in seconds) | off | M3 |
| `fallback_image_url` (`fiu`) Pro | base64url URL | `D.Fallback`; honoured only with `XI_FALLBACK_ALLOW_REQUEST_URL` and when the source allowlist accepts it | config | M1 (WP-34c) |
| `skip_processing` (`skp`) | ext… | `D.SkipProcessing` | — | M1 |
| `raw` | bool | `TR.Kind = Raw` | false | M1 |
| `cachebuster` (`cb`) | string | `D.CacheBuster` | — | M1 |
| `bypass_cache` (`bc`) Pro | bool | skips result cache (needs `XI_CACHE_ALLOW_BYPASS`) | false | M2 |
| `cache_tags` (`ct`) Pro | tags… (visible ASCII, no commas; replaces earlier tags) | `D.CacheTags` → `Cache-Tag`/`Surrogate-Key` header (`XI_CACHE_TAGS_HEADER`) | — | M2 |
| `expires` (`exp`) | unix ts | `D.Expires` | — | M1 |
| `filename` (`fn`) | name:encoded | `D.Filename` | source basename | M1 |
| `return_attachment` (`att`) | bool | `D.Attachment` | config | M1 |
| `preset` (`pr`) | names… | expanded in place | — | M1 |
| `hashsum` (`hs`) Pro | none, or md5/sha1/sha256/sha512:hex | `TR.Source.Hashsum` (lowercased) | none | M2 |
| `max_src_resolution` (`msr`), `max_src_file_size` (`msfs`), `max_animation_frames` (`maf`), `max_animation_frame_resolution` (`mafr`), `max_result_dimension` (`mrd`) | int | `TR.Limits` (only if security options allowed) | config | M1 |
| removed upstream: `gif_options` | — | accepted and ignored for old URLs | — | — |

### 3.3 Info options

Parsed by `imgproxy.Parser.ParseInfo` (the path after `/info`; same signature bytes as processing URLs) into `TR.Kind = Info` and `TR.Info`. Info options have their own namespace (`s`, `f`, `a`, `c`, `p`, `ph` mean something else in processing URLs); processing options are unknown here (400 in strict mode). Chained pipelines are 400; a source extension is ignored. Options that enable an M2/M3 feature are recorded in `TR.Ignored` with the milestone. JSON field names match imgproxy's.

| Option (aliases) | Args | TR field | Default | Milestone |
|---|---|---|---|---|
| `size` (`s`) | bool | `Info.Size` | true | M1 |
| `format` (`f`) | bool | `Info.Format` | true | M1 |
| `dimensions` (`d`) | bool | `Info.Dimensions` | true | M1 |
| `exif` | enabled:canonical_names | `Info.EXIF` (canonical names: `Ignored`, not in model) | true:false | M1 |
| `iptc` | bool | `Info.IPTC` | true | M1 |
| `xmp` | bool | `Info.XMP` | true | M1 |
| `video_meta` (`vm`) | bool | `Info.VideoMeta` | true | M3 |
| `detect_objects` (`do`) Pro | bool | `Info.DetectObjects` | false | M3 |
| `classify` (`cl`) Pro | top_k:classes… | `Info.Classify` (top_k/classes: `Ignored`, not in model) | 0 | M3 |
| `colorspace` (`cs`) | bool | `Info.Colorspace` | false | M1 |
| `bands` (`b`) | bool | `Info.Bands` | false | M1 |
| `sample_format` (`sf`) | bool | `Info.SampleFormat` | false | M1 |
| `pages_number` (`pn`) | bool | `Info.PagesNumber` | false | M1 |
| `alpha` (`a`) | alpha:check_transparency | `Info.Alpha` (check_transparency: `Ignored`, not in model) | false:false | M1 |
| `crop` (`c`) | w:h:gravity (as processing `crop`) | `Info.Crop` | — | M2 |
| `objects_position` (`obj_pos`, `op`) Pro | as processing | `Info.ObjectsPos` + `P.Gravity.ObjectsPosition` | ce:0:0 | M3 |
| `palette` (`p`) | 0 or 2..256 | `Info.Palette` | 0 | M2 |
| `average` (`avg`) | enabled:ignore_transparent | `Info.Average` | false:true | M2 |
| `dominant_colors` (`dc`) | enabled:build_missed | `Info.DominantColors` | false:false | M2 |
| `blurhash` (`bh`) | x:y (1..9 each, 0:0 off) | `Info.Blurhash` | 0:0 | M2 |
| `thumbhash` (`thumb_hash`, `th`) | bool | `Info.Thumbhash` | false | M2 |
| `perceptual_hash` (`phash`, `ph`) | bool | `Info.PHash` | false | M2 |
| `calc_hashsums` (`chs`) | md5, sha1, sha256, sha512… | `Info.CalcHashsums` | — | M2 |
| `page` (`pg`), `video_thumbnail_second` (`vts`), `video_thumbnail_keyframes` (`vtk`), `cachebuster` (`cb`), `expires` (`exp`), `preset` (`pr`), `hashsum` (`hs`), `max_src_resolution` (`msr`), `max_src_file_size` (`msfs`), `bypass_cache` (`bc`), `cache_tags` (`ct`) | as in §3.2 | as in §3.2 | as in §3.2 | as in §3.2 |

Response (WP-35; golden files per fixture in `testdata/info/`): `size` (source bytes), `format` + `mime_type` (imgproxy names: UltraHDR is `jpeg`, HEIC is `image/heif`), `width`/`height` (one page, EXIF orientation applied) + `orientation` (1–8), `exif` (libexif titles as keys, e.g. `Date and Time (Original)`, and libexif's human-readable values, as imgproxy; `exif:1:1` canonical names are not in the model yet and are listed in `Ignored`), `iptc` (IIM dataset titles, repeatable datasets as arrays, plus imgproxy's `Name`/`Caption` aliases) and `photoshop.resolution` when present, `xmp` (properties grouped by namespace prefix; `rdf:Seq`/`rdf:Bag` → arrays, `rdf:Alt` → first item, structs → objects), `colorspace`/`bands`/`sample_format` (libvips names: `srgb`, `b-w`, `rgb16`, `uchar`, `ushort`, …), `pages_number` (pages, or frames of an animation), `alpha` (boolean). A requested `exif`/`iptc`/`xmp` section is `{}` when the source has none. `video_meta` and the M2/M3 options produce no field until implemented.

### 3.4 Examples

```
/unsafe/rs:fill:300:400:0/g:sm/plain/https://example.com/a.jpg@webp
/AfrOrF3gWeDA6VOlDG4TzxMv39O7MXnF4CXpKUwGqRM/pr:sharp/rs:fill:300:400:0/g:sm/aHR0cDovL2V4YW1w/bGUuY29tL2ltYWdl/cy9jdXJpb3NpdHku/anBn.png
/sig/rs:fit:500:500/wm:0.5:nowe/-/wm:0.7:soea/plain/s3://bucket/key.jpg
/info/unsafe/s:1/f:1/d:1/bh:4:3/plain/https://example.com/a.jpg
```

### 3.5 Client hints (WP-65; all dialects)

A hint is read when the URL opts into it (imgix `ch=Width,DPR,Save-Data`, ImageKit `w-auto`/`dpr-auto`; the cloudinary `w_auto`/`dpr_auto` and cloudflare `width=auto` rows map to the same fields once those dialects set them) or, with `XI_CLIENT_HINTS_ENABLED`, whenever the URL leaves the value unset. `negotiate.ApplyClientHints` resolves hints into the request before defaults, so hinted values are part of the canonical key and ETag (Save-Data through `qualityDelta`, ARCHITECTURE §9.1).

| Hint | Rule |
|---|---|
| DPR | `Sec-CH-DPR`, else `DPR` (an invalid Sec-CH value falls back to the legacy name); `(0, 8]`. Used when the URL sets no `dpr`. Sets `Content-DPR` when ≠ 1. |
| Width | `Sec-CH-Width`, else `Width`: physical px ÷ the effective DPR (URL `dpr`, else the DPR hint, else 1). Used when the URL sets no width. |
| Viewport-Width | `Sec-CH-Viewport-Width`, else `Viewport-Width` (CSS px), only without a Width hint. |
| Save-Data / ECT | `Save-Data: on` or `ECT: slow-2g`/`2g`: the URL's slow-connection quality, else `XI_FORMATS_SAVE_DATA_QUALITY_DELTA`, only when the URL fixes no quality. Not consulted when the delta is 0 and the URL gives no slow-connection quality. |
| `Vary` | Only the names that decided the result: a hint the URL overrode is not listed; a valid `Sec-CH-*` value drops the legacy name; a Width hint drops Viewport-Width; `Save-Data: on` drops `ECT`. A hint that could apply but was absent lists both its names, because its presence would have changed the result (a shared cache must not serve the hint-less rendition to hinted clients). |
| `Accept-CH` | `Sec-CH-DPR, Sec-CH-Width, Sec-CH-Viewport-Width, ECT` on every image response (200, HEAD, 304, unprocessed) when hints are enabled or the URL opted in; `Critical-CH` too with `XI_CLIENT_HINTS_CRITICAL_CH`. |
| Unsigned policy | `restricted` rejects URLs that opt into width/DPR hints (403) and, for the server-wide switch, applies only Save-Data to unsigned requests: a hinted width or DPR would bypass `XI_SECURITY_ALLOWED_WIDTHS`. Signed requests and `allow` mounts get every hint. |

Browsers (Chromium only; Firefox and Safari send none of these except Save-Data where enabled) act on `Accept-CH` only for navigations, over HTTPS. A page embedding images from this server must itself send `Accept-CH: Sec-CH-DPR, Sec-CH-Width, Sec-CH-Viewport-Width` and delegate them cross-origin, e.g. `Permissions-Policy: ch-dpr=(self "https://img.example.com"), ch-width=(self "https://img.example.com"), ch-viewport-width=(self "https://img.example.com")`. `Sec-CH-Width` is only sent for `<img>` elements with a `sizes` attribute.

imgproxy parity (`IMGPROXY_ENABLE_CLIENT_HINTS`, `clientfeatures/detector.go`, `options/parser/processing_options.go`):

| Behaviour | imgproxy | xtandard-image |
|---|---|---|
| DPR / Width header names and precedence | `Sec-CH-DPR` → `DPR`; `Sec-CH-Width` → `Width` | Same |
| URL options beat hints | Yes (hints are defaults, URL options applied after) | Yes |
| Width hint ÷ DPR | ÷ the DPR hint only, even when the URL sets `dpr` | ÷ the effective DPR (URL `dpr` wins), so the physical width equals the hint. Deliberate |
| Explicit `w:0` in the URL | Overrides the hint | Indistinguishable from unset; the hint applies. Deliberate (the model has no "explicit 0") |
| `Viewport-Width` | Removed (v3.16.0) | Fallback when no Width hint |
| `Save-Data` / `ECT` | Ignored | Quality delta / slow-connection quality |
| `Vary` | Static `Sec-CH-DPR, DPR, Sec-CH-Width, Width` whenever enabled | Only the names that decided the result (table above) |
| `Accept-CH` / `Critical-CH` | Not sent | Sent (Critical-CH opt-in) |
| `Content-DPR` | Not sent | Sent for a hinted DPR ≠ 1 |
| Restricted unsigned requests | n/a (imgproxy has no unsigned allowlist) | Save-Data only |

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

Grammar: path = object key relative to mount source (escaped, `SourceRelative`, `Source.Mount` = the mount's source); a path whose decoded form starts with `http:`/`https:` is a web proxy source (plain absolute URL, subject to the allowlist and the unsigned policy). Query params are applied by name, the last occurrence wins; any param may be base64url-encoded (padding optional) by appending `64` (`txt64`, `mark64`, `bg64`, …). Legacy spellings (`markalign`, `txtclr`, `trimcolor`, …) are aliases. `ixlib`/`ixid`/`ixjsv` (SDK and analytics tags) are accepted silently, also in strict mode. Unknown params and the "Unsupported" rows below are recorded in `Ignored` (strict → 400); unsupported *values* of known params (`auto=enhance|redeye|true`, `fill=gen`, `cs=adobergb1998`, image masks) are recorded in `Ignored` in both modes. Implemented in `internal/dialect/imgix` (WP-40).

| imgix | TR | Notes |
|---|---|---|
| `w`, `h` | Resize.Width/Height (value in (0,1) → relative; fractions of a pixel rounded) | M1-level semantics in M2 |
| `max-w`, `max-h`, `min-w`, `min-h` | Resize.MaxWidth/MaxHeight/MinWidth/MinHeight | |
| `fit=clip` (default once `w` or `h` is set) | Fit + Enlarge | imgix clip upscales; `fit=max` does not |
| `fit=max` / `fit=min` | Fit, no enlarge / FillDown (gravity from `crop`) | `min` = crop without upscaling |
| `fit=crop` | Fill + Enlarge; gravity from `crop` | |
| `fit=smart-crop` | as `fit=crop` with `crop=auto` (Smart) unless `crop` is set | |
| `fit=scale` | Force + Enlarge | |
| `fit=fill`, `fill=solid|blur|gen`, `fill-color` | Fit + Enlarge + Extend; `fill-color` → Background (wins over `bg`); `fill=blur` → Extend.Mode Blur (M3); `fill=gen` unsupported (solid) | `fill`/`fill-color` only with `fit=fill|fillmax` |
| `fit=fillmax` | as `fill` without Enlarge | |
| `fit=facearea`, `facepad`, `faceindex` | Fill + Enlarge, Gravity Face with FacePad, FaceIndex (1-based → 0-based) | M3 |
| `fit=clamp` | Fit + Enlarge + Extend with edge replication (Extend.Mode Copy, vips `embed extend=copy`) | |
| `crop=top,bottom,left,right` | compass Gravity | combinations → corner |
| `crop` fallback chain | the first detector listed wins (imgix falls back left to right); compass values only apply without a detector | |
| `crop=faces` | Face gravity (M3; falls back to Smart) | |
| `crop=entropy` / `edges` / `auto` | Entropy / Smart (attention) / Smart | |
| `crop=<object class>` (COCO class or supercategory, e.g. `car,vehicle`) | Gravity Object with Classes | M3 |
| `crop=focalpoint`, `fp-x`, `fp-y`, `fp-z` | FocusPoint (default 0.5, 0.5) + FocusZoom (1–100) | `fp-*` only with `crop=focalpoint` |
| `ar=16:9` (also `1.5:1`, `2`) | Resize.AspectRatio | only with `fit=crop|smart-crop`, else ignored as in imgix |
| `rect=x,y,w,h` | Crop (pre-resize, px or relative (0,1)), anchored north-west with offsets x, y | |
| `dpr` | Resize.DPR (0 < dpr ≤ 10) | |
| `q` | Quality (`q=0` → 1) | |
| `fm=jpg|pjpg|png|png8|png32|webp|avif|jxl|gif|json|blurhash|mp4|webm` | Format; `pjpg` → Progressive; `png8` → PNG quantize 256; `json` → Info; `blurhash` → Info blurhash (JSON, not imgix's bare string: approximated, M2); `mp4/webm` M3 | `jp2`, `jxr`, `pdf` unsupported → 422 |
| `auto=format` | negotiate (Accept); wins over `fm` except `json`, `blurhash`, `mp4`, `webm` (imgix uses `fm` only as the fallback; xi records `fm` as ignored) | |
| `auto=compress` | Quality 45 when `q` absent; StripMetadata; PreferJPEGIfOpaque when neither `fm` nor `auto=format` decides the format (imgix: PNG if transparent, else JPEG) | |
| `auto=enhance`, `auto=redeye`, `auto=true` | Unsupported | recorded in `Ignored` even in strict mode |
| `lossless` | Lossless | |
| `chromasub=420|422|444` | ChromaSubsampling | |
| `cs=srgb|tinysrgb|strip|origin|adobergb1998` | ColorProfile `srgb` (M2) / StripColorProfile true / false; `adobergb1998` not implemented | |
| `dpi` | DPI | M2 |
| `bg` (RGB, ARGB, RRGGBB, AARRGGBB (alpha first), `#` optional, CSS named, `transparent`) | Background | |
| `pad`, `pad-top/right/bottom/left` | Padding | |
| `border=w,color`, `border-top/right/bottom/left`, `border-radius` (`r` or `tl,tr,br,bl`), `border-radius-inner` | Border, Radius; `border-radius-inner` not implemented | M2 |
| `blur` (0–2000) | Blur sigma = v/20 | |
| `px` | Pixelate | |
| `sharp` (0–100) | Sharpen sigma = 0.5 + v/50 | |
| `usm` (−100..100), `usmrad` (0–500, default 2.5) | Unsharp{Amount: usm/100, Sigma: usmrad} | M2 |
| `bri`, `con`, `exp`, `gam`, `sat`, `hue`, `vib` | Adjust: bri → Brightness v·255/100; con, exp, sat, vib → 1 + v/100; gam → 2^(v/100); hue → degrees; 0 = unchanged | M2 |
| `high`, `shad` | Unsupported | |
| `sepia`, `monochrome`, `duotone`, `duotone-alpha`, `invert` | Sepia (any amount > 0; partial amounts approximated) / Monochrome (colour alpha = intensity) / Duotone (intensity `duotone-alpha`/100) / Negate | M2 |
| `orient` (1–8, 90, 180, 270) | AutoRotate off + the Rotate/Flip of that EXIF orientation; 90/180/270 = EXIF 6/3/8 | overrides the EXIF orientation |
| `rot` (0–359, counter-clockwise) | RotateAny = −rot (multiples of 90 fold into Rotate) | imgix zooms to hide the corners; xi fills them with Background. M2 |
| `flip=h|v|hv` | Flip (combined with `orient`) | |
| `trim=auto|color`, `trim-color`, `trim-tol`, `trim-md`, `trim-sd`, `trim-pad` | Trim: `auto` → Threshold `trim-md` (default 11), `trim-sd` approximated; `color` → Color `trim-color` (default corner pixel), Threshold `trim-tol` (default 0); `trim-pad` not implemented | |
| `mask=ellipse|corners`, `mask-bg`, `corner-radius` | Mask ellipse (+ Background) / Radius; image masks (a URL) unsupported | M2 |
| `mark`, `mark-alpha`, `mark-align` (default bottom,right), `mark-pad` (default 5), `mark-scale`, `mark-w`, `mark-h`, `mark-x`, `mark-y`, `mark-tile=grid`, `mark-rot`, `mark-fit`, `mark-base` | Watermark from URL (or a path on the mount's source) | M3 |
| `txt`, `txt-font`, `txt-size`, `txt-color`, `txt-align`, `txt-pad` (default 10), `txt-shad`, other `txt-*` | Text watermark (Pango; `txt` is escaped, not markup) | M3 |
| `blend*` | Unsupported (only `blend-mode=normal` via watermark) | |
| `page`, `frame` (1-based) | Page; `frame` also Pages = 1 | M2 |
| `dl` | Filename + Attachment | |
| `ch=Width,DPR,Save-Data` | enable client hints for this URL | |
| `expires` | Expires (unix seconds; 0 = never); checked after the signature, 404 once passed | |
| `s` | signature: hex(MD5(token ‖ path ‖ "?" ‖ query-without-s)); `s` may appear anywhere, the message is built without it | |
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
| `g_xy_center` + `x_`,`y_` | FocusPoint: relative `x_`/`y_` (0..1) → `FocusX/FocusY`; absolute (integer pixels on both axes) → `FocusPx` via `transform.FocusFromPixels(x, y, x, y)` (WP-86a); a relative axis mixed with an absolute one is approximated to the centre | |
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

Reference: https://imagekit.io/docs/transformations, https://imagekit.io/docs/image-resize-and-crop, https://imagekit.io/docs/effects-and-enhancements, https://imagekit.io/docs/media-delivery-basic-security; SDKs `imagekit-javascript` `src/url.ts` (URL builder) and `imagekit-nodejs` `src/resources/helper.ts` (signing); unpic `imagekit.ts` (defaults `c-maintain_ratio,fo-auto`). Implemented in `internal/dialect/imagekit` (WP-42).

```
https://ik.example.com/tr:w-400,h-300,fo-auto/path/a.jpg
https://ik.example.com/path/tr:w-400/a.jpg
https://ik.example.com/path/a.jpg?tr=w-400,h-300:rt-90&ik-t=1767225600&ik-s=…
/imagekit/<mount>/tr:w-400/path/a.jpg
```

Grammar: the path after the mount is the file path, relative to the mount's bound source. When a host-bound mount keeps the ImageKit URL-endpoint id in the path (`https://ik.imagekit.io/<id>/…`), `imagekit.Options.EndpointPath` (`/<id>`) is stripped first (not yet a config key). Transformations: a `tr:` path segment anywhere before the file name (`:` may be written `%3A`; the segment is percent-decoded), or the `tr` query parameter (query-decoded, so unpic's `%2C` works). Both present: the path wins and the query is recorded in `TR.Ignored` (400 in strict mode); the SDKs never emit both and ImageKit documents no order. More than one `tr:` segment or `tr` parameter is 400. `:` separates chained steps (→ chained pipelines, max 16), `,` separates `key-value` parameters (split on the first `-`; `e-<effect>[-value]` and `l-<layer>` keep their two-part names). Empty steps and parameters are skipped. Request-scoped parameters (`q`, `f`, `pr`, `lo`, `md`, `cp`, `orig`) apply to the whole request, the last occurrence winning. Other query parameters (SDK `queryParameters`, e.g. `?v=2`) become `D.CacheBuster`; `ik-attachment=true` → `D.Attachment`. ImageKit arithmetic expressions (`w-iw_div_2`, `lx-bw_mul_0.5`) are recorded as ignored (strict: 400).

| ImageKit | TR |
|---|---|
| `w`, `h` (integer px, fraction in (0,1) relative) | Resize.Width/Height |
| `w-auto` | ClientHints.Width (`h-auto` is 400) |
| `ar-<w>-<h>` (also `_`) | Resize.AspectRatio (ignored with `cm-extract`) |
| `c-maintain_ratio` (default whenever a size is given) | Fill + Enlarge |
| `c-maintain_ratio_no_enlarge` | Fill |
| `c-force` / `c-at_max` / `c-at_max_enlarge` / `c-at_least` | Force+Enlarge / Fit / Fit+Enlarge / Outside+Enlarge |
| `cm-pad_resize` / `cm-pad_resize_no_enlarge` (+ `bg`) | Fit (+Enlarge) + Extend; `fo-left` etc. place the image (Extend gravity), default centre |
| `cm-extract` | Crop w×h, no resize; position from `x`,`y` (top-left, px), `xc`,`yc` (centre, px; needs px w and h), else `fo`, else centre |
| `cm-pad_extract` | Crop + Fit (no enlarge) + Extend to w×h |
| `fo-center|top|left|bottom|right|top_left|top_right|bottom_left|bottom_right` | compass Gravity |
| `fo-auto`, `fo-auto-custom_override` | Smart |
| `fo-face`, `fo-faces` | Face (M3) |
| `fo-<object>` (`car`, `truck_car_train`) | Object, Classes = [value] (M3) |
| `fo-custom` | Unsupported (per-file crop area stored in ImageKit) |
| `z` (with face/object focus) | FocusZoom |
| `q` (1–100) | Quality |
| `f-auto`, `f-orig` | Format unset (negotiated / source) |
| `f-jpg|jpeg|png|webp|avif|gif|jxl` | Format; `f-mp4|webm` M3; `f-svg` unsupported |
| `pr-true|false`, `lo-true|false` | Progressive, Lossless |
| `md-true|false` | StripMetadata = false / true (keep metadata) |
| `cp-true|false` | StripColorProfile = false / true (colour profile; not copyright) |
| `orig-true` | Kind raw |
| `dpr` (0.1–5), `dpr-auto` | Resize.DPR / ClientHints.DPR |
| `bl-<1..100>` (also unpic's `blur-`) | Blur σ = v/2 |
| `e-sharpen[-n]` | Sharpen σ = 1 (bare) or 0.5 + n/50 (approximation) |
| `e-usm-<radius>-<sigma>-<amount>-<threshold>` | Unsharp{Sigma, Amount} (M2); radius, threshold dropped |
| `e-grayscale` | Grayscale (M2) |
| `e-contrast` (auto contrast stretch) | Equalize (M2) |
| `r-<n>`, `r-<tl>_<tr>_<br>_<bl>`, `r-max` | Radius (M2) |
| `b-<w>_<color>` | Border (M2) |
| `bg-<RRGGBB|RRGGBBAA|RGB|css name>` | Background |
| `bg-blurred[_…]` | Extend.Mode = blur (M3); `bg-dominant`, `bg-gradient…`, `bg-genfill…` unsupported |
| `rt-<deg>`, `rt-N<deg>` | Rotate (multiples of 90), else RotateAny (M2) |
| `rt-auto` | AutoRotate |
| `fl-h|v|h_v` | Flip |
| `t-true|<1..99>` | Trim threshold 10 / n |
| `o-<0..100>` | Adjust.Opacity (M2) |
| `n-<name>` | preset `<name>` expanded in place (named transformation) |
| `di-<path>` (`@@` = `/`) | Fallback, relative to the mount source (M2) |
| `pg-<n>`, `pg-<a>_<b>` | Page (1-based) / Pages (M2) |
| `l-image,i-<path>|ie-<b64>,lx,ly,lfo,w,h,o,…,l-end` | Watermark from the mount source: `lx`/`ly` offsets from top-left (`N` = from right/bottom), else `lfo` compass, else centre; `w`/`h` px size; `o` opacity (M3) |
| `l-text,i-<text>|ie-<b64>,fs,ff,co,…,l-end` | Text watermark (escaped for Pango), Font = `ff fs`, Color (M3) |
| `l-image,i-ik_canvas`, `l-video`, `l-subtitles`, nested layers, in-layer transformations, `lxc`, `lyc`, `lap`, `lm` | Unsupported |
| `e-bgremove`, `e-removedotbg`, `e-upscale`, `e-retouch`, `e-genvar`, `e-dropshadow`, `e-shadow`, `e-gradient`, `e-changebg`, `e-edit`, `e-distort`, `e-colorize`, `cr`, `dn`, video `vc`,`ac`,`so`,`eo`,`du`,`sr` | Unsupported |
| `ik-s`, `ik-t` | signature, Expires |

Signing (imagekit-nodejs `getSignature`): `ik-s` = hex(HMAC-SHA1(private key, path-relative-to-endpoint ‖ ["?" query without `ik-s`/`ik-t`] ‖ expiry)), expiry = the `ik-t` value or `9999999999` when absent (the SDK signs but does not write it). The private key is used as written (`XI_DIALECTS_IMAGEKIT_KEYS`, one list per mount). An `ik-t` in the past is 404 (`KindExpired`), checked after the signature.

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

Pixel focal points and `meta` (WP-86): `focal(AxB:CxD)` with pixel coordinates (thumbor's norm) and the crop rectangle found by `extract_focal()` become `Gravity{Type: FocusPoint, FocusPx: &FocusRect{A, B, C, D}}` through `transform.FocusFromPixels(A, B, C, D)`; the planner focuses on the rectangle's centre (`A + (C−A)/2`, rounded down like thumbor's `FocalPoint.from_square`) in the displayed source, relative to the manual crop `AxB:CxD` when there is one. Rectangles given in fractions (all ≤ 1) keep `FocusX/FocusY`. `meta` answers thumbor's JSONEngine shape (`server/info_thumbor.go`): `source` (`url` as written, displayed `width`/`height`, `frameCount`), `operations` (`crop` left/top/right/bottom in displayed source pixels, `resize` width/height, shrink-on-load listed as a resize, `flip_horizontally`/`flip_vertically`; rotations, fills and filters are not listed, as in thumbor), `target` (the planned result size) and `focal_points` (the explicit point with `z` = its area, or one `alignment` point). The JSONP `callback` argument is not supported.

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
