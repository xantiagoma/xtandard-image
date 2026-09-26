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
| awssih | `/sih/` | AWS Serverless Image Handler (§13); usually host-bound to the old CloudFront domain |
| simple:* | provider-native prefixes (`/_next/image`, `/_vercel/image`, `/.netlify/images`, `/_ipx/`, `/_image`, `/storage/v1/render/image/`) | Table-driven, see §12 |

## 2. Signing schemes at a glance

| Dialect | Scheme | Where | Spec |
|---|---|---|---|
| imgproxy | HMAC-SHA256(key, salt ‖ path-after-signature, starting with `/`), keys and salts hex-decoded, URL-safe base64 without padding, optionally truncated to `signature_size` bytes; multiple key/salt pairs; trusted signatures list | first path segment | https://docs.imgproxy.net/usage/signing_url |
| query | imaginary-compatible `sign` = base64url-nopad(HMAC-SHA256(key, path ‖ url.Values.Encode() of the query without `sign`)), i.e. keys sorted, no `?` separator | query | imaginary `middleware.go` `validateURLSignature` |
| imgix | `s` = hex(MD5(token ‖ path ‖ ("?" ‖ query-without-s if any))), token used verbatim; imgix puts `s` last, xi accepts it anywhere (same signed bytes); optional `expires` (unix) | query | https://github.com/imgix/imgix-blueprint#securing-urls |
| cloudinary | `s--XXXXXXXX--` = first 8 chars of base64url(SHA-1(transformations/…/public_id.ext ‖ api_secret)), version segment excluded (SHA-256 for sha256 accounts); 32 chars of base64url(SHA-256(…)) for "long" signatures; plain hash, not HMAC (§6) | path segment | https://cloudinary.com/documentation/delivery_url_signatures |
| imagekit | `ik-s` = hex(HMAC-SHA1(private_key, url-relative-to-endpoint (path ["?" query without ik-s/ik-t]) ‖ ik-t)), `ik-t` = expiry unix seconds; absent = 9999999999, which is signed but not written | query | https://imagekit.io/docs/media-delivery-basic-security |
| thumbor | HMAC-SHA1(SECURITY_KEY, escaped path-after-signature **without** the leading `/`), URL-safe base64 **with** padding (28 chars, Python `urlsafe_b64encode`); keys are raw strings, not hex; `unsafe` = unsigned (§10) | first path segment | https://thumbor.readthedocs.io/en/latest/security.html |
| bunny | token auth: `token` (or path segment `bcdn_token=`) + `expires`; basic = b64url(MD5(key ‖ path ‖ expires)); legacy advanced = b64url(SHA-256(key ‖ path ‖ expires ‖ data)); current advanced = `HS256-` b64url(HMAC-SHA256(key, path ‖ expires ‖ data)); path = decoded path or `token_path`, data = other params sorted `k=v&…`; b64url unpadded | query or first path segment | https://bunny.net/docs/cdn/security/token-authentication; layout verified against `BunnyWay/BunnyCDN.TokenAuthentication` (see §9) |
| cloudflare `/cdn-cgi/image` | none upstream | — | https://developers.cloudflare.com/images/transform-images/transform-via-url/ |
| cloudflare-images | `sig` = hex(HMAC-SHA256(key, path ‖ "?" ‖ query-without-sig)), i.e. `path?exp=<unix>` for Cloudflare's own URLs; raw key; `exp` checked after the signature (expired → 404) | query | https://developers.cloudflare.com/images/optimization/hosted-images/serve-private-images/ |
| cloudflare `/cdn-cgi/image` (xi) | optional `xi_sig` | query | — |
| awssih | `signature` = hex(HMAC-SHA256(secret, path [‖ "?" ‖ query])), secret used verbatim; query = decoded parameters without `signature`, last value per key, sorted by "key,value" (UTF-16), joined `k=v&…` unescaped; optional `expires=YYYYMMDDTHHmmssZ` | query | §13, SIH `image-request.ts` `validateRequestSignature` |
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
| `unsharp_masking` (`ush`) Pro | mode:weight:divider (3×3 kernel, ARCHITECTURE §8.1 step 8) | `P.Unsharp` | auto:1:24 when given; off otherwise | M2 (WP-61) |
| `blur_areas` (`ba`) Pro | sigma:l:t:w:h… (relative to the oriented source; follow rotate/flip/crops) | `P.BlurAreas` | off | M2 (WP-61) |
| `progressive_blur` (`pbl`) Pro | sigma:direction:start:stop; direction as `gradient` | `P.ProgressiveBlur` | off (…:down:0:1) | M2 (WP-61) |
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
| `autoquality` (`aq`) Pro | method:target:min:max:allowed_error; method `none`/`size`/`dssim`/`ml` (`ml` parsed, run as no auto-quality and reported skipped); `size` target in bytes (≤ 2³⁰) | `O.AutoQuality` | config | M2 (WP-56, FORMATS §6.4) |
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

Parsed by `imgproxy.Parser.ParseInfo` (the path after `/info`; same signature bytes as processing URLs) into `TR.Kind = Info` and `TR.Info`. Info options have their own namespace (`s`, `f`, `a`, `c`, `p`, `ph` mean something else in processing URLs); processing options are unknown here (400 in strict mode). Chained pipelines are 400; a source extension is ignored. Options that enable an M3 feature are recorded in `TR.Ignored` with the milestone; the M2 options below are implemented (WP-63). JSON field names match imgproxy's.

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

Response (WP-35; golden files per fixture in `testdata/info/`): `size` (source bytes), `format` + `mime_type` (imgproxy names: UltraHDR is `jpeg`, HEIC is `image/heif`), `width`/`height` (one page, EXIF orientation applied) + `orientation` (1–8), `exif` (libexif titles as keys, e.g. `Date and Time (Original)`, and libexif's human-readable values, as imgproxy; `exif:1:1` canonical names are not in the model yet and are listed in `Ignored`), `iptc` (IIM dataset titles, repeatable datasets as arrays, plus imgproxy's `Name`/`Caption` aliases) and `photoshop.resolution` when present, `xmp` (properties grouped by namespace prefix; `rdf:Seq`/`rdf:Bag` → arrays, `rdf:Alt` → first item, structs → objects), `colorspace`/`bands`/`sample_format` (libvips names: `srgb`, `b-w`, `rgb16`, `uchar`, `ushort`, …), `pages_number` (pages, or frames of an animation), `alpha` (boolean). A requested `exif`/`iptc`/`xmp` section is `{}` when the source has none. `video_meta` and the M3 options produce no field until implemented.

Pro options (WP-63; per-option goldens for a photo, an alpha PNG and an animated GIF in `testdata/info/pro/`). `crop` → `crop: {left, top, width, height}`: where the crop (sized and placed exactly like the processing pre-crop, `plan.InfoCrop`) lands on the upright image, as fractions of its width and height; it reports coordinates and does not change the other fields (as imgproxy documents it); `crop:0:0` adds nothing; content-aware gravities (`sm`, face, object) are placed at the centre and listed in `Ignored` as `crop:gravity`. The pixel fields are computed by `internal/imageinfo` (pure Go) on the first page or frame, decoded, EXIF-rotated, converted to 8-bit sRGB RGBA and fitted in 512×512 by `codec.Sample` (vips_thumbnail, so JPEG/WebP shrink on load) inside the same `vips.Run` as the header read: `palette` → `palette: [{R,G,B,A}…]`, at most N colours by weighted median cut over exact RGBA values (fully transparent pixels count as one transparent colour; most common first; an image with ≤ N colours gets exactly those); `average` → `average: {R,G,B,A}`, the rounded channel means, fully transparent pixels skipped unless `ignore_transparent` is 0; `dominant_colors` → `dominant_colors: {vibrant, light_vibrant, dark_vibrant, muted, light_muted, dark_muted}` each `{R,G,B}`, the Vibrant.js/Android Palette method (5-bit histogram of pixels with alpha ≥ 125, 64-swatch median cut, per-class luma/saturation windows and weighted scoring); a class no swatch fits is omitted, or with `build_missed` derived from a found class (same hue, the class's target lightness, and its target saturation when crossing vibrant/muted); `blurhash` → `blurhash` string, the woltapp reference encoder (bit-exact with its C encoder on its test images; alpha ignored); `thumb_hash` → `thumb_hash`, the evanw reference encoder on a copy fitted in 100×100, as upper-case hex (bit-exact with the reference JavaScript); `perceptual_hash` → `perceptual_hash`, 16 lower-case hex digits of a 64-bit DCT pHash (32×32 luma, 8×8 lowest frequencies against their median; compare with Hamming distance; imgproxy does not document its algorithm, so values are not comparable across the two). `calc_hashsums` → `hashsums: {md5, sha1, sha256, sha512}` (the requested ones), lower-case hex digests of the source bytes as fetched (an SVG is hashed before sanitising); no decode.

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
| `fm=jpg|pjpg|png|png8|png32|webp|avif|jxl|gif|json|blurhash|mp4|webm` | Format; `pjpg` → Progressive; `png8` → PNG quantize 256; `json` → Info; `blurhash` → Info blurhash 4:3 (JSON, not imgix's bare string: approximated; WP-63); `mp4/webm` M3 | `jp2`, `jxr`, `pdf` unsupported → 422 |
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
| `usm` (−100..100), `usmrad` (0–500, default 2.5) | Unsharp{Amount: usm/100, Sigma: usmrad}: classic unsharp mask v + amount × (v − blur) | M2 (WP-61) |
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

Grammar: `/<cloud>/<asset_type>/<delivery_type>/[s--SIG--/][<tx>/]*[v<digits>/]<public_id>[.<ext>]`. When host-bound as a private CDN or custom domain (`<cloud>-res.cloudinary.com`-style, unpic `privateRegex`), the `<cloud>/` segment is absent and the source comes from the mount: the first segment is read as the asset type when it is `image`/`video`/`raw` and the second is a delivery type, otherwise it is the cloud name. `asset_type` = `image`; `video` and `raw` are 400 until video thumbnails (M3). `delivery_type` = `upload` (public ID = key under the cloud's source, extension included, as written) or `fetch` (absolute http(s) URL, percent-decoded, a proxy-collapsed `https:/` repaired; the source allowlist applies); `private`/`authenticated` are `upload` that require a verified signature (403 `signature required` otherwise, also when signing is off); other delivery types are 400. `<cloud>` maps to a named source via `dialects.cloudinary.clouds`; with a non-empty map an unmapped cloud is 403, with an empty map the mount's source applies to every cloud. Public IDs, layer IDs and `d_` IDs whose first segment contains `:` (`https:`, `s3:`) are 400, so an `upload` URL can never name an absolute source. The query string (SDK `_a=` analytics) is ignored.

Components: a path segment is a transformation component when every comma-separated part is `key_value` with a lower-case key (or `$variable`) and at least one key is a Cloudinary parameter; the first segment that is not, or a `v<digits>` segment, starts the public ID (the SDKs add `v1` to foldered IDs for exactly this ambiguity). Components apply in URL order. A component with pipeline parameters (`w h c g x y ar z a bo e o r t`) starts a chained pipeline once the current one holds anything; components with only `f_`, `q_`, `fl_`, `dpr_`, `b_`, `cs_`, `d_`, `pg_` modify the current pipeline / the request, so `c_fill,w_400/q_auto/f_auto` is one pipeline and `c_fill,w_400/e_blur` two. The extension sets `O.Format` unless `f_` is given. In `XI_DIALECTS_STRICT` mode unknown and unsupported parameters are 400.

Signature: `s--SIG--` right after the delivery type; the signed message is the rest of the path with the version segment removed (`<tx>/…/<public_id>.<ext>`, or `<tx>/…/<escaped fetch URL>`), exactly as the SDKs build `to_sign` (cloudinary-npm `utils.url`). 8 characters = SHA-1 (SHA-256 for accounts whose signature algorithm is sha256, `sign.CloudinaryOptions.ShortSHA256`), 32 characters = SHA-256 (`long_url_signature`); both are accepted, `LongOnly` rejects the 48-bit short form. Keys are the api secrets of the mount (`dialects.cloudinary.keys`); with no keys the segment is skipped unchecked. Unsigned requests fall to the router's unsigned policy.

| Cloudinary | TR | Notes |
|---|---|---|
| `w_`, `h_` (int px; a decimal is relative: `w_0.5`, `w_1.0`; `w_auto[:step[:fallback]]` client hint) | Width/Height | expressions (`iw_div_2`), `$vars` unsupported |
| `c_scale` (default when `w_`/`h_`/`ar_` without `c_`) | Force if both dims or `ar_`, else Fit + Enlarge | after a `t_` preset that set a mode, the preset's mode is kept |
| `c_fit` / `c_limit` / `c_mfit` | Fit+Enlarge / Fit / Outside+Enlarge | |
| `c_fill` / `c_lfill` | Fill+Enlarge / FillDown | |
| `c_fill_pad` | Fill + gravity + Extend | approximated |
| `c_pad` / `c_lpad` / `c_mpad` | Fit+Enlarge+Extend / Fit+Extend / Fit+Extend | `c_mpad` approximated (never scales upstream); extend anchor = compass `g_` else centre |
| `c_crop` | Crop (w, h, `ar_`), no scaling; `x_`/`y_` without `g_` = top-left corner (NorthWest + offsets); `g_xy_center` = FocusPoint with relative `x_`/`y_` | absolute `xy_center` approximated (centre) |
| `c_thumb` | Fill + gravity, `z_` → FocusZoom | faces M3 |
| `c_auto`, `c_auto_pad` | Fill + Smart / Fit + Enlarge + Extend | |
| `c_imagga_*` | recorded unsupported, applied as `c_scale` | |
| `g_` compass (`north_east`…), `center` | Gravity | |
| `g_auto`, `g_auto:<anything>` | Smart | |
| `g_face`, `g_faces`, `g_face(s):…`, `g_adv_*` | Face (M3; fallback Smart) | |
| `g_<object>` (e.g. `g_dog`) | Object | M3 |
| `g_xy_center` + `x_`,`y_` | FocusPoint: relative `x_`/`y_` (0..1) → `FocusX/FocusY`; absolute (integer pixels on both axes) → `FocusPx` via `transform.FocusFromPixels(x, y, x, y)` (WP-86a); a relative axis mixed with an absolute one is approximated to the centre | |
| `g_custom`, `g_body`, `g_ocr_text`, `g_liquid`, … | unsupported (centre) | |
| `ar_16:9` / `ar_1.5` | Resize.AspectRatio (Crop.AspectRatio with `c_crop`, M2) | |
| `x_`, `y_` | gravity offsets | |
| `z_` | FocusZoom (`c_thumb`, `c_crop`) | other modes approximated |
| `q_<1-100>[:420\|422\|444]`, `q_auto[:best\|good\|eco\|low]` | Quality (+ChromaSubsampling) / AutoQuality DSSIM targets 0.005 / 0.02 / 0.035 / 0.05 (`q_auto` = good) | M2; `q_jpegmini` unsupported |
| `f_auto[:…]`, `f_<fmt>` (jpg/jpeg/jpe, png, webp, avif, jxl, gif, heic/heif, tif/tiff, bmp, ico, svg, mp4, webm) | Format unset (negotiated) / Format | mp4/webm M3; other formats (pdf, jp2, …) 400 |
| `dpr_<float>`, `dpr_auto` | DPR of the current pipeline / client hint | |
| `a_<deg>` (`.`-joined list), `a_hflip`, `a_vflip`, `a_exif`, `a_ignore` | Rotate (multiples of 90) / RotateAny (M2) / Flip / AutoRotate | `a_auto_right`, `a_auto_left` unsupported |
| `b_<color>` (`rgb:` 3/4/6/8 hex digits, `%23` hex, CSS names, `transparent`) | Background | `b_auto`, `b_blurred`, `b_gen_fill` unsupported |
| `bo_<n>px_solid_<color>` | Border | M2 |
| `r_<n>[:<n>…]` (1–4 values, Cloudinary corner order), `r_max` | Radius | M2 |
| `o_<0-100>` | Adjust.Opacity | M2 |
| `e_blur[:1-2000]` | Blur σ = n/20 (default 100 → 5, like imgix `blur`) | |
| `e_sharpen[:1-2000]`, `e_unsharp_mask[:1-2000]` | Sharpen σ = n/100 / Unsharp σ 1, amount n/100 (M2) | |
| `e_pixelate[:1-200]` | Pixelate (default 10) | |
| `e_grayscale`, `e_blackwhite` (approximated), `e_sepia`, `e_negate` | corresponding Pipeline fields | M2 |
| `e_brightness[:-99..100]` (default 80), `e_hue[:-100..100]` (80) | Brightness n × 2.55 / Hue n × 1.8° | M2 |
| `e_contrast`, `e_saturation`, `e_gamma`, `e_vibrance`, `e_red`/`e_green`/`e_blue` | multiplier 1 + n/100 | M2 |
| `e_colorize[:0-100]` + `co_<color>` | Colorize (opacity n/100, default grey) | M2 |
| `e_trim[:tolerance[:color]]` | Trim (threshold = tolerance, default 10) | |
| `e_blur_faces[:n]`, `e_pixelate_faces[:n]` | BlurDetections(face) σ n/20 / pixelate n | M3 |
| `e_blur_region[:n]` + relative `x_`,`y_`,`w_`,`h_` | BlurAreas (no region: whole-image blur) | M2; pixel regions approximated |
| other `e_*` (AI, artistic, `background_removal`, `gen_*`, `upscale`, `vectorize`, …) | Unsupported | |
| `fl_lossy`, `fl_progressive[:none]`, `fl_strip_profile`, `fl_keep_iptc`, `fl_keep_attribution`, `fl_force_strip`, `fl_attachment[:name]`, `fl_immutable_cache`, `fl_png8` (M2), `fl_getinfo` | Lossless=false, Progressive, StripColorProfile, StripMetadata=false, KeepCopyright, StripMetadata, Attachment/Filename, Immutable, PNG quantize 256, Kind=Info (size, format, dimensions) | `fl_animated`, `fl_awebp`, `fl_apng`, `fl_preserve_transparency`, `fl_any_format` are the default behaviour; `fl_rasterize`, `fl_clip`, … unsupported |
| `cs_srgb`/`cs_no_cmyk`/`cs_tinysrgb`, `cs_cmyk` | ColorProfile srgb / cmyk | M2; `cs_keep_cmyk`, `cs_icc:` unsupported |
| `pg_<n>` (1-based) | Page n−1 | M2; ranges and names unsupported |
| `t_<name>[.<name>…]` | presets expanded in place (imgproxy option syntax); only-presets mode allows nothing but `t_` | |
| `d_<public_id>` (`:` = folder) | Fallback (same cloud source) | M2 |
| `l_<id>` (`:` = folder), `l_fetch:<base64url>` with `g_`, `x_`, `y_`, `o_`, `a_`, `w_`/`h_` px, relative `w_` + `fl_relative` (scale), `fl_tiled`; legacy single component or `l_…/…/fl_layer_apply,…` group | Watermark (image / URL) on the current pipeline | M3; `l_text`, `l_video`, `l_subtitles`, `l_lut` unsupported; other parameters in a layer group approximated |
| `u_` underlays, `if_` (skips its component, or the block up to `if_end`), `$var`, `fn_`, `dn_`, video/audio `vc_`/`ac_`/`du_`/`so_`/`eo_`/`fps_`/… | Unsupported | |

Fixtures: `internal/dialect/cloudinary/testdata/urlgen/generate.ts` builds the corpus with `@cloudinary/url-gen` 1.22.0 and the `cloudinary` Node SDK 2.11.0 (including signed URLs, i.e. the signature vectors); unpic's `cloudinary.test.ts` URLs are asserted in `compat_test.go`.

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
| `e-usm-<radius>-<sigma>-<amount>-<threshold>` | Unsharp{Sigma, Amount} (WP-61, classic unsharp mask); radius, threshold dropped |
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

Reference: https://developers.cloudflare.com/images/optimization/features/ (one page per option, `src/content/partials/images/*.mdx` in cloudflare-docs); unpic `cloudflare.ts` (defaults `f=auto,fit=cover`) and `cloudflare_images.ts`. Implementation: `internal/dialect/cloudflare` (WP-43, WP-79).

```
/cdn-cgi/image/width=400,height=300,fit=cover,f=auto,q=75/uploads/a.jpg
/cdn-cgi/image/w=400,g=0.5x0.2,onerror=redirect/https://other.example.com/a.jpg
/cdn-cgi/image/w=400/https%3A%2F%2Fother.example.com%2Fa.jpg%3Fv%3D1
```

Grammar: `/cdn-cgi/image/<k=v(,k=v)*>/<source>`. Keys are case-insensitive; commas inside parentheses (`rgb(1,2,3)`) do not split; keys and values are percent-decoded; later options override earlier ones. The source is a path on the mount's bound source (kept with its leading `/`, so it always resolves as a relative key) or an absolute `http(s)` URL subject to the allowlist; an absolute URL may be percent-encoded as a whole (unpic does this when it has a `?`), and `https:/host` collapsed by path cleaning is repaired. The request query belongs to the source and is appended to it. A host-bound mount also accepts the full `/cdn-cgi/image/…` path. There is no upstream signature; the optional `xi_sig` (§2) authenticates URLs.

| Cloudflare | TR |
|---|---|
| `width`/`w`, `height`/`h` (positive px) | Width/Height |
| `width=auto` | `ClientHints.Width`; `wbreakpoints`/`wmobile`/`wdesktop` → Ignored |
| `fit=scale-down` (default) / `contain` / `cover` / `crop` / `pad` / `squeeze` | Fit / Fit+Enlarge / Fill+Enlarge / FillDown / Fit+Extend+Enlarge / Force+Enlarge |
| `fit=scale-up` / `aspect-crop` | Fit+Enlarge / Fill (both Ignored "approximated": no "never downscale" or "crop without scaling" field) |
| `gravity`/`g` = `auto` / `face` / `left` / `right` / `top` / `bottom` / `XxY` (0–1) | Smart / Face (M3) / West / East / North / South / FocusPoint |
| `zoom`/`face-zoom` (0–1) | Gravity.FocusZoom (M3) |
| `quality`/`q` = 1–100 or `high` / `medium-high` / `medium-low` / `low` | Quality (90 / 80 / 65 / 50) |
| `slow-connection-quality`/`scq` (same values) | SlowConnectionQuality + `ClientHints.SaveData` (used when Save-Data/ECT hints say slow) |
| `format`/`f` = `auto` / `avif` / `webp` / `jpeg` / `baseline-jpeg` / `png` / `gif` | negotiate (FormatUnset) / Format / JPEG+Progressive / JPEG+Progressive=false / … |
| `format=json` | Ignored "not implemented" (Cloudflare's JSON is not the /info schema; the image is served) |
| `dpr` (> 0) | DPR |
| `anim=false` / `true` | DisableAnimation |
| `background` (`%23RGB[A]`, `%23RRGGBB[AA]`, CSS names, `transparent`, `rgb()`/`rgba()` with commas or spaces and `/ alpha`) | Background |
| `blur` (0–250) | Blur σ = v/2 |
| `sharpen` (0–10) | Sharpen σ = v/4 |
| `brightness` (multiplier) | Adjust.Exposure (Adjust.Brightness is additive) (M2) |
| `contrast`, `saturation` (multipliers), `gamma` (0 and 1 = unchanged) | Adjust (M2) |
| `rotate=0|90|180|270`, `flip=h|v|hv` | Rotate, Flip |
| `trim=border`, `trim.border.color`, `trim.border.tolerance` (0–255, 255 = exact) | Trim (Threshold = 255 − tolerance, default 10); `trim.border.keep` → Ignored |
| `trim=top;right;bottom;left`, `trim.top/right/bottom/left/width/height` (px, or a fraction < 1) | Crop anchored north-west: left/top are gravity offsets, width/height the size; right/bottom fractions fold into a relative size when left/top are fractions, right/bottom pixels → Ignored "not implemented" |
| `border=color;width`, `border=color;top;right;bottom;left`, `border.color/width/top/right/bottom/left` (Workers-only upstream) | Border (M2) |
| `metadata=keep` / `copyright` / `none` | StripMetadata=false / StripMetadata+KeepCopyright / StripMetadata, KeepCopyright=false |
| `onerror=redirect` | Delivery.OnErrorRedirect: when the fetch or decode fails with a fallback-eligible error, the server answers 302 to the source URL (http sources only; ahead of the fallback image; the error Cache-Control; same path as wsrv `default=`, ARCHITECTURE §12) |
| `compression=fast` | Output.Effort = 1 (the format is not forced to JPEG) |
| `segment=foreground`, `upscale=generate` | Ignored "unsupported" (400 in strict mode); `upscale=interpolate` is the default |
| anything else | Ignored "unknown option" (400 in strict mode) |

### 8.1 Cloudflare Images delivery (`/cdn-cgi/imagedelivery/`, `imagedelivery.net`)

Reference: https://developers.cloudflare.com/images/optimization/hosted-images/ (serve-uploaded-images, create-variants, enable-flexible-variants, serve-private-images).

```
/cdn-cgi/imagedelivery/<account_hash>/<image_id>/<variant>
/cdn-cgi/imagedelivery/<account_hash>/<image_id>/w=400,sharpen=3        (flexible variant)
https://imagedelivery.net/<account_hash>/<image_id>/<variant>?exp=<unix>&sig=<hex>
```

Dialect `cloudflare_images`, mounted at `/cdn-cgi/imagedelivery/`; bind the host `imagedelivery.net` (`XI_DIALECTS_CLOUDFLARE_IMAGES_HOSTS`) to serve the bare shape. A host-bound mount also strips a leading `/cdn-cgi/imagedelivery`.

- `<account_hash>` → source: `XI_DIALECTS_CLOUDFLARE_IMAGES_ACCOUNTS` (`hash=source,…`); empty means every hash uses the mount's source, otherwise an unknown hash is 403.
- `<image_id>` → the object key in that source (custom ids may contain `/`: everything between the hash and the last segment).
- Last segment containing `=` → flexible variant, parsed with the §8 option table (`onerror` is Ignored: not supported for hosted images); 403 in only-presets mode (`XI_PRESETS_ONLY`, Cloudflare "flexible variants" off).
- Otherwise → a named variant, expanded as the preset of that name (imgproxy syntax, `XI_PRESETS`). `public`, or no last segment, without a preset of that name serves the image unchanged; any other unknown variant is 400.
- Signing: `sig` = hex(HMAC-SHA256(key, path ‖ "?" ‖ query without `sig`)) over the path after the prefix (`/<hash>/<id>/<variant>`, as for imagedelivery.net), keys from `XI_DIALECTS_CLOUDFLARE_IMAGES_KEYS` used raw. Then `exp` (unix seconds) must be in the future, else 404 (`KindExpired`). Unsigned requests follow the mount's unsigned policy.

## 9. Bunny Optimizer

Reference: https://bunny.net/docs/optimizer/dynamic-images/overview (Dynamic Images API: resizing, cropping, face-detection, luminosity, color-manipulation, reflections-and-rotations, blur-and-sharpen, quality, formats) and https://bunny.net/docs/optimizer/image-classes, fetched 2026-09-26; unpic `bunny.ts` (sets `aspect_ratio` when w and h given; `format` → `output`). Implementation: `internal/dialect/bunny` (WP-44).

```
https://cdn.example.com/a.jpg?width=400&aspect_ratio=16:9&quality=80&output=webp
/bunny/<mount>/a.jpg?width=400&crop_gravity=north&token=…&expires=…
/bunny/<mount>/bcdn_token=HS256-…&expires=…&token_path=%2Fimg%2F/img/a.jpg
```

The path after the mount is the source, a key relative to the mount's bound source (never an absolute URL; a `:` in its first segment is escaped). Parameters are unordered as on Bunny: the last value of a repeated parameter wins and options apply in Bunny's documented order (class, crop operations, resize, flip/flop/rotate, blur/sharpen, colour, format/quality). Numbers are Bunny's integer scales; out-of-range or malformed values are 400 naming the parameter; unknown parameters are `Ignored` (strict mode: 400).

| Bunny | TR |
|---|---|
| `width`, `height` (px) | Width/Height, Fit (Bunny keeps the aspect ratio with both, picking the smaller result), no enlarge |
| `upscaling=off|resampling` | Enlarge |
| `aspect_ratio=w:h` (`auto` = none) | Crop.AspectRatio anchored by `crop_gravity` (M2). With `width`+`height` of the same ratio (what unpic emits) it is Resize fill-down (fill with `upscaling=resampling`), which takes effect now |
| `crop=w,h` / `crop=w,h,x,y` (px, x,y = top-left) | Crop with gravity from `crop_gravity` / Crop at north-west + offsets |
| `crop_gravity=center|north|south|east|west|northeast|northwest|southeast|southwest` (+ unpic aliases `top|bottom|left|right|top_left|…`) | Gravity |
| `focus_crop=w,h,x,y` | Crop w×h: relative x,y (0–1 with a decimal point) → focus-point gravity; pixel x,y → north-west offsets (x−w/2, y−h/2), clamped by the planner |
| `face_crop=true` / `face_crop=w,h` | Face (M3); until then ignored / a centred w×h crop |
| `quality` (0–100) | Quality |
| `format` / `output` = `webp|jpeg|png|gif|avif` | Format |
| `auto_optimize=low|medium|high` | Quality 85/75/65 unless `quality` is set (format negotiation is the default) |
| `sharpen=true|false` (Bunny's examples also use `sharpen=<n>`) | Sharpen σ 1 (numeric: same, recorded as approximated) |
| `blur` (0–100) | Blur σ = v/4 |
| `flip`, `flop` | Flip V / H |
| `rotate` (−270…270, multiples of 90) | Rotate (normalised to 0/90/180/270) |
| `brightness` (−100..100) | Adjust.Brightness = v × 2.55 (M2) |
| `contrast`, `saturation` (−100..100) | multiplier 1 + v/100 (M2); `saturation=-100` is grayscale |
| `gamma` (−100..100) | multiplier 2^(v/100) (M2) |
| `hue` (0–100, 33 ≈ one primary) | Adjust.Hue = v × 3.6° (M2) |
| `tint=rrggbb` (`ffffff` = none) | Tint (M2) |
| `sepia` (0–100) | Sepia on when > 0 (M2; intensity not modelled) |
| `class=<name>` | preset (the mount shares `XI_PRESETS`); with `XI_PRESETS_ONLY` ("forced transformation classes") any other option is 403 |
| `optimizer=image` | ignored (meaning is implicit) |
| `token`/`bcdn_token`, `expires`, `token_path`, `token_ignore_params` | signature (below) |
| `token_countries`, `token_countries_blocked`, `limit` | signed but not enforced (no geo lookup, no rate limiting); recorded in `Ignored` |

**Token authentication** (`sign.BunnyMessage`, `sign.NewBunnyVerifier`). Keys are the mount's `keys` (the pull zone's URL token authentication key, a raw string, not hex); a mount without keys is unsigned. Three layouts are accepted, told apart by the `HS256-` prefix or the token length:

```
basic   token = b64url(MD5(key ‖ path ‖ expires))                   22 chars, deprecated by Bunny
sha256  token = b64url(SHA-256(key ‖ path ‖ expires ‖ data))        43 chars, legacy "advanced"
hs256   token = "HS256-" b64url(HMAC-SHA256(key, path ‖ expires ‖ data))   current "advanced"
```

b64url is URL-safe base64 without padding (padding is tolerated). `path` is the decoded path after the mount (the pull-zone path), or `token_path` when present; `expires` is the parameter exactly as written (unix seconds); `data` is every other parameter, Optimizer options included, except `token`/`bcdn_token` and `expires`, sorted by key and joined as `key=value` with `&` using the decoded values (empty values kept as `key=`); with `token_ignore_params=true` it is only `token_ignore_params=true` plus `token_path`, so options can be added freely. A verified token with `token_path` authorises only paths starting with it (else 403). A token without `expires`, a repeated `token`/`expires` or a repeated signed parameter is 403. `expires` in the past is 404 (`KindExpired`) for every URL that carries it. IP-locked tokens (basic with the IP appended, `HS256-1-…`) cannot be verified because dialects never see the client address; they are 403. Unsigned requests fall under the mount's unsigned policy (set `unsigned_policy: deny` to require tokens like Bunny does).

Verified sources: the HS256 layout is `go/token.go` of https://github.com/BunnyWay/BunnyCDN.TokenAuthentication at 48ff1af (2026-07-24), whose `e2e/vectors.json` (key `SecurityKey`, expires 1598024587) the tests reproduce; the legacy SHA-256 layout is `nodejs/token.js` and `php/url_signing.php` at 1019b23 (the last commit before the HMAC rewrite), whose C# `TokenSignerTests` expectations the tests reproduce, including path-based `bcdn_token` URLs; the basic layout is the PHP/JS/C#/Python sample on https://bunny.net/docs/cdn/security/token-authentication/basic (its example token omits the key, so the test vector is computed independently with `openssl md5`). Where the legacy SDKs disagree (C# put the IP before `expires`, Python appended it after `data`) only the IP-less layout is implemented.

## 10. Thumbor

Reference: https://thumbor.readthedocs.io/en/latest/usage.html, https://thumbor.readthedocs.io/en/latest/filters.html, security page above; code: libthumbor `url.py` (`Url.regex`), thumbor `context.py` (`RequestParameters`), `transformer.py`, `filters/*.py`, `ext/filters/*.c`, `handlers/imaging.py` (signature check). Implementation: `internal/dialect/thumbor` (grammar, mapping) and `internal/dialect/thumbor/filters` (filter-list syntax, reused by Storyblok §12).

```
/thumbor/unsafe/300x200/smart/filters:format(webp):quality(80)/https%3A%2F%2Fexample.com%2Fa.jpg
/thumbor/<hmac>/trim/10x20:310x220/fit-in/-300x0/left/top/filters:blur(3)/s3-key/a.jpg
```

Grammar (libthumbor `Url.regex`; every segment optional except the first and the image):

```
/<hmac|unsafe>/[debug/][meta/][trim[:top-left|:bottom-right][:tolerance]/][AxB:CxD/]
  [adaptive-][full-]fit-in/][-][W|orig]x[-][H|orig]/
  [left|center|right/][top|middle|bottom/][smart/][filters:name(args)[:name(args)…]/]<image>
```

- **Matching.** Segments are matched on the escaped path in this order; a segment is taken only when something follows it (the regex backtracks otherwise), so `/unsafe/smart` is the image `smart` and a segment out of order (`/unsafe/smart/300x200/a.jpg`) starts the image. Matched parts are percent-decoded afterwards, as Tornado does for thumbor's path arguments, so `https%3A%2F%2Fe.com%2Fa.jpg` is one segment and the image `https://e.com/a.jpg`. The query string is ignored.
- **Signature.** The first segment is `unsafe` or a signature (thumbor's `[^/]{28,}`: at least 28 characters); anything else is a 400 ("URL does not have hash or unsafe"). signature = base64url-**padded**(HMAC-SHA1(key, escaped path after `/<signature>/`)), keys are thumbor `SECURITY_KEY` strings used as raw bytes (`XI_DIALECTS_THUMBOR_KEYS`, per mount; extra mounts fall back to the primary mount's keys), no salt. A percent-encoded signature (`%3D`) is decoded first. Comparison is constant-time on the decoded MAC, so only the canonical padded spelling verifies (as in thumbor, which compares strings). `unsafe` is Unsigned and the mount's unsigned policy decides (thumbor `ALLOW_UNSAFE_URL`); with `XI_SIGNING_REQUIRED` and keys it is a 403. Vectors: thumbor `tests/test_url_composer.py` and `tests/handlers/test_base_handler*.py`.
- **Image.** A key relative to the mount's source or an absolute URL (`http(s)://…`, `s3://…`); the source resolver applies the allowlist. thumbor's HTTP loader treats a scheme-less `host/path` as an HTTP URL; here it is a relative key.
- **Invalid values are 400.** A known filter with malformed arguments is a 400 naming the filter (thumbor silently drops it); sizes, crop coordinates and trim tolerance above 65536 are 400s. Unknown filters are recorded in `Ignored` (400 with `XI_DIALECTS_STRICT`).

| Thumbor | TR |
|---|---|
| `WxH` (default) | Resize Fill + Enlarge (thumbor crops to fill and upscales) |
| `fit-in` / `full-fit-in` | Fit (no enlarge) / Outside + Enlarge |
| `adaptive-*` | `Resize.Adaptive` (swap W/H when orientation differs) |
| `-W`, `-H` | Flip H / V (`-0x-0` flips without resizing) |
| `0` or empty, `orig` | auto / `Dim{Rel: 1}` (source dimension) |
| `AxB:CxD` | Crop (pre-resize, px): width C−A, height D−B, gravity north-west with offsets A, B; all zero = none; C ≤ A or D ≤ B = none (`Ignored`) |
| `trim[:corner][:tol]` | Trim (`ColorFromBottomRight`, `Threshold` = tolerance clamped to 255); `trim:N` is tolerance N (thumbor reads a lone number as the corner name and ends up with top-left, 0) |
| halign/valign | Gravity compass (either alone pairs with center/middle); both absent = unset (centre) |
| `smart` | Gravity Smart (overrides halign/valign; M3: faces first) |
| `meta` | `Kind` Info, `Info{Schema: thumbor, Size, Format, Dimensions}`; the pipeline keeps the operations so the info path can report the target |
| `debug` | `Ignored` (not implemented) |
| `quality(n)` | Quality (0–100) |
| `format(png\|jpeg\|jpg\|gif\|webp\|avif\|heic\|heif)` | Format (case-insensitive; heif = HEIC) |
| `max_bytes(n)`, `max_age(s)` | MaxBytes, Delivery.TTL |
| `autojpg([bool])` | `Output.PreferJPEGIfOpaque` |
| `strip_exif()`, `strip_icc()` | StripMetadata, StripColorProfile |
| `no_upscale()`, `upscale()` | Enlarge false / true |
| `stretch()` | Resize Force (no effect with fit-in, as in thumbor) |
| `proportion(f)` | ZoomX = ZoomY = f for 0 < f ≤ 1, else no effect |
| `rotate(n)` | Rotate: thumbor rotates counter-clockwise, so Rotate = (360 − n mod 360) mod 360; non-multiples of 90 have no effect (thumbor ignores them) |
| `brightness(n)` | Adjust.Brightness = n·255/100 (n clamped to ±100; `_brightness.c`) |
| `contrast(n)` | Adjust.Contrast = ((100+n)/100)² (`_contrast.c`) |
| `saturation(n)` | Adjust.Saturation = max(0, 1 + n/100) (the documented percentage) |
| `rgb(r,g,b)` | Adjust.Red/Green/Blue = max(0, 1 + n/100); thumbor adds n% of 255, so recorded as approximated |
| `grayscale()`, `equalize()` | Grayscale, Equalize |
| `blur(radius[,sigma])` | Blur σ = sigma, or radius when sigma is 0/absent |
| `sharpen(amount,radius,luma_only)` | Unsharp{Amount, Sigma = radius}; luma_only is validated, not applied |
| `background_color(color)` | Background |
| `fill(color\|auto\|blur\|transparent[,fill_transparent])` | Extend (background) + Background; `blur` → Extend blur (M3), `auto` (median colour) M3 |
| `round_corner(a[\|b],r,g,b[,transparent])` | Radius a on every corner (b ≠ a approximated) + Background rgb unless transparent |
| `focal(AxB:CxD)` | Gravity FocusPoint: pixel rectangles (thumbor's norm) → `FocusPx` (WP-86, below); rectangles in fractions (all ≤ 1) → `FocusX/FocusY` at the centre; an empty rectangle is recorded in `Ignored` |
| `extract_focal()` | when the image is itself a thumbor URL (up to 10 levels), the innermost original image replaces it; the crop rectangle is a pixel focal point (`FocusPx`, WP-86); an empty crop rectangle is recorded in `Ignored` |
| `watermark(url,x,y,alpha[,w_ratio[,h_ratio]])` | Watermark: Source url, Opacity = (100 − alpha)/100, position x/y px from left/top, negative from right/bottom, `Np` fraction, `center`; `repeat` → Tile repeat (both axes); Scale = w_ratio/100; h_ratio approximated |
| `colorize`, `convolution`, `curve`, `noise`, `red_eye` | `Ignored` (unsupported) |
| `gifv`, `cover`, `frame` | `Ignored` (M3) |

Filter-list syntax (`filters` sub-package): `name(args)` separated by `:`, names `[A-Za-z0-9_]+`, arguments split on `,` outside parentheses and single quotes, trimmed, one pair of single quotes removed (thumbor `BaseFilter.String`); parentheses nest, so the list ends at the first `)/` at depth 0 (thumbor's lazy `.+?\)/` is the fallback when they do not balance). At most 64 filters. Colours: Pillow's 148 CSS3 keywords, `transparent`, hex `rgb`/`rgba`/`rrggbb`/`rrggbbaa` with optional `#`.

Pixel focal points and `meta` (WP-86): `focal(AxB:CxD)` with pixel coordinates (thumbor's norm) and the crop rectangle found by `extract_focal()` become `Gravity{Type: FocusPoint, FocusPx: &FocusRect{A, B, C, D}}` through `transform.FocusFromPixels(A, B, C, D)`; the planner focuses on the rectangle's centre (`A + (C−A)/2`, rounded down like thumbor's `FocalPoint.from_square`) in the displayed source, relative to the manual crop `AxB:CxD` when there is one. Rectangles given in fractions (all ≤ 1) keep `FocusX/FocusY`. `meta` answers thumbor's JSONEngine shape (`server/info_thumbor.go`): `source` (`url` as written, displayed `width`/`height`, `frameCount`), `operations` (`crop` left/top/right/bottom in displayed source pixels, `resize` width/height, shrink-on-load listed as a resize, `flip_horizontally`/`flip_vertically`; rotations, fills and filters are not listed, as in thumbor), `target` (the planned result size) and `focal_points` (the explicit point with `z` = its area, or one `alignment` point). The JSONP `callback` argument is not supported.

## 11. weserv / wsrv.nl

Reference: https://wsrv.nl/docs/quick-reference; the weserv source (`.refs/weserv-images`: `src/api/parsers/query.cpp` for the grammar, `src/api/processors/*.cpp` for semantics, `src/nginx` for `url`, `default`, `maxage`, `filename`, `encoding`); unpic `wsrv.ts` (default `fit=cover`; `url` without scheme is `https://`). Implemented in `internal/dialect/wsrv` (WP-46).

```
/?url=example.com/a.jpg&w=300&h=300&fit=cover&a=attention&output=webp&q=80
/wsrv/?url=ssl:example.com/a.jpg&w=50%25&filt=greyscale&default=example.com/404.png
```

Grammar (upstream semantics xi keeps): the **first** occurrence of a key wins (upstream uses `emplace`); synonyms fold into the canonical key (`width`/`height`/`quality`/`level`/`lossless`/`pages`/`align`, deprecated `t`→`fit`, `bri`→`mod`, `or`→`ro`, `shape`→`mask`, `strim`→`mtrim`, `errorredirect`→`default`); a key without `=` has the empty value; booleans are true unless `0`/`false`; query decoding is lenient (`w=50%` works). Parameter order is otherwise irrelevant: the processing order is fixed (trim → size/orientation/crop → effects). Deliberately stricter than upstream: malformed or out-of-range values are a 400 naming the parameter (wsrv silently ignores them); only the fallbacks wsrv documents keep their meaning (see below). The path after the mount must be `/`.

| wsrv | TR |
|---|---|
| `url` (scheme optional; `//host/…` and legacy `ssl:` prefix = https; any `scheme://` passes to the allowlist) | Source; on a mount bound to a named source a scheme-less value is a relative key |
| `w`, `h` (px or `%`, 0 = auto) | Width/Height (`Dim.Rel` for %) |
| `dpr` (0 < dpr ≤ 8; 0 = unset) | DPR |
| `fit=inside|outside|cover|fill|contain` (deprecated `fit`/`fitup`, `squaredown`/`square`/`crop*`, `absolute`, `letterbox`; `fit`/`squaredown` also imply `we`) | Fit / Outside / Fill / Force / Fit+Extend (background fill, gravity from `a`) |
| `cbg` | Background, only with `fit=contain` (approximated when `bg` is also set) |
| `we` | Enlarge=false; without it Enlarge=true (wsrv enlarges by default) |
| `a=center|top|right|bottom|left|top-left|…|entropy|attention|focal` (deprecated `t`,`l`,`r`,`b`), `fpx`, `fpy` (0–1), `a=focal-X-Y` / `crop-X-Y` (percent) | Gravity compass / Entropy / Smart / FocusPoint (default 0.5, 0.5) |
| `cx`, `cy`, `cw`, `ch` (px or %), deprecated `crop=w,h,x,y` | Crop with its top-left corner at (cx, cy); missing sizes run to the edge. Post-resize (wsrv default): a second chained pipeline holding the crop and every effect; `precrop` puts it before the resize in pipeline 0 |
| `trim` (1–254; empty = 10), `tbg` | Trim threshold / colour |
| `mask=circle|ellipse|triangle|triangle-180|pentagon|pentagon-180|hexagon|square|star|heart|none`, `mtrim`, `mbg` | Mask (M2) |
| `flip`, `flop` | Flip V / H |
| `ro` (multiples of 90 → Rotate, others → RotateAny), `rbg` | Rotate / RotateAny + Background (approximated when `bg` is set) |
| `bg` | Background |
| `blur` (σ 0.3–1000; empty or out of range = wsrv's mild 3×3 blur ≈ σ 0.8, approximated) | Blur |
| `sharp` (σ ≤ 10; empty or out of range = mild ≈ σ 0.5, approximated), `sharp=f,j,σ`, `sharpf`, `sharpj` | Sharpen (flat/jagged slopes recorded as not implemented) |
| `con` (−100–100) | Contrast multiplier 1+con/100 (wsrv uses a sigmoid; approximated, M2) |
| `mod=b[,s[,h]]` | Exposure (brightness multiplier on LCH L), Saturation, Hue (M2) |
| `sat` (multiplier, ≤ 1000), `hue` (degrees, normalised to 0–359) | Saturation / Hue (M2) |
| `gam` (1–3; empty or out of range = 2.2) | Gamma (M2) |
| `tint` | Tint (M2) |
| `filt=greyscale|sepia|duotone|negate|none`, `start`, `stop` (duotone colours, default `#C83658`/`#D8E74F`) | Grayscale / Sepia / Duotone (intensity 1) / Negate (M2) |
| colours | CSS names, or hex `RGB`/`ARGB`/`RRGGBB`/`AARRGGBB` with optional `#` — alpha comes **first** |
| `output=jpg|png|gif|tiff|webp|avif|jxl|origin|json` | Format (origin = unset) / Info (imgproxy info schema, recorded as ignored) |
| `q` (1–100), `l` (zlib 0–9), `ll`, `af`, `il` | Quality, PNG compression, Lossless, PNG adaptive filter, Progressive |
| `n` (−1 all, ≥ 1), `page` (≥ 0; −1 largest / −2 smallest recorded as not implemented) | Pages, Page (M2) |
| `loop`, `delay`, `fsol` | Ignored (not implemented / not applicable) |
| `default=<url>` (or deprecated `errorredirect`) | OnErrorRedirect + Fallback: on a fallback-eligible failure (ARCHITECTURE §12) the server answers **302** to that URL, but only when it is an absolute http(s) URL the source allowlist accepts (never an open redirect); otherwise the fallback-image path runs (the URL is used as the fallback image when `XI_FALLBACK_ALLOW_REQUEST_URL`) |
| `default=1` | OnErrorRedirect: 302 to the request's own http(s) source |
| `filename` | Filename |
| `maxage` (nginx time: `31d`, `1w`, `2M`, `1y`, `1w 3d`, plain seconds; 1d–1y) | TTL (Cache-Control max-age, capped by `XI_CACHE_HEADERS_TTL`) |
| `encoding=base64` | ResponseDataURL: body `data:<type>;base64,…` as `text/plain`, no Content-Disposition |
| `xi_sig` | optional xi signature (§2) |

Under the restricted unsigned policy `default=<url>` (a fallback URL), crops, trims, effects, `n` and `output=json` are rejected like in every other dialect; `default=1`, `maxage` and `encoding` are allowed.

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

## 13. AWS Serverless Image Handler ("Dynamic Image Transformation for Amazon CloudFront")

Reference: `awslabs/dynamic-image-transformation-for-amazon-cloudfront` v8.x (`.refs/aws-image-handler/source/image-handler/`: `image-request.ts`, `image-handler.ts`, `query-param-mapper.ts`, `thumbor-mapper.ts`, `lib/`, and the `test/` fixtures), sharp 0.35 (`.refs/sharp/lib`) for the edit semantics. Package `internal/dialect/awssih`, mount `/sih/` (usually host-bound to the CloudFront domain being migrated). Point the mount at the old API's path root so SIH signatures, which cover the path, keep verifying.

SIH picks one of three request types from the path after the mount:

```
/sih/eyJidWNrZXQiOiJteS1idWNrZXQiLCJrZXkiOiJhLmpwZyIsImVkaXRzIjp7InJlc2l6ZSI6eyJ3aWR0aCI6MjAwfX19   (Default: base64 JSON)
/sih/fit-in/200x200/filters:grayscale()/s3:my-bucket/a.jpg                                       (Thumbor)
/sih/legacy/200/a.jpg   + REWRITE_MATCH_PATTERN=/^\/legacy\/(\d+)\/(.*)$/  REWRITE_SUBSTITUTION=/fit-in/$1x$1/$2   (Custom)
?signature=<hex>&expires=20261231T235959Z&format=webp&width=300                                   (any type)
```

1. **Default (base64 JSON)**: the path is base64 of a JSON object. It is decoded like Node's `Buffer.from(s, "base64")`: standard and URL-safe alphabets, padding optional, anything after the first `=` ignored (SIH's own fixtures carry spurious `==`). If it decodes to a JSON object it is a Default request, otherwise the path goes to 2/3. xtandard-image implements this type completely.
2. **Thumbor**: a thumbor URL without the signature segment. With `dialects.awssih.thumbor: true` the mount forwards it to the thumbor dialect (§10, WP-45): an `s3:<bucket>` segment naming a listed bucket is removed and selects that bucket's source (SIH `parseImageBucket`), `/unsafe` is prepended, the query is dropped, and the thumbor result keeps SIH's own signature outcome, the query-param edits and `expires`. The forwarding target is a keyless thumbor parser of its own, so it accepts `/unsafe/` even when `dialects.thumbor` has keys and signing is required. Without forwarding these paths are a 400. SIH-only thumbor filters (`animated`, `smart_crop`, …) are whatever the thumbor dialect makes of them.
3. **Custom**: with `rewrite_match_pattern` + `rewrite_substitution` set, non-JSON paths are rewritten first (`/pattern/flags` literal with flags `g i m s`, RE2 syntax; substitution `$1`, `$<name>`, `$&`, `$$`) and then forwarded as in 2.

Request JSON (`DefaultImageRequest`):

| Field | TR |
|---|---|
| `bucket` | `Source.Mount` = `dialects.awssih.buckets[bucket]` (SIH `SOURCE_BUCKETS`); unlisted → 403; absent → the mount's `source` |
| `key` | `Source.Raw` (relative, `Encoding` relative); `%`, `?`, `#`, `:` are escaped so a key can never read as a URL or scheme; empty or non-string → 400 |
| `edits` | applied in JSON key order (see below); `null` = none |
| `outputFormat` | `O.Format` (`jpg`/`jpeg`, `png`, `webp`, `avif`, `gif`, `tiff`, `heif`/`heic` → HEIC, `jxl`); `raw` → 400 |
| `effort` | WebP output only: `O.Effort` from sharp's 0–6 (truncated; invalid → 4, like SIH) |
| `headers` | see "Response headers" |
| anything else (`requestType`, `reductionEffort`, …) | `Ignored` (never an error) |

Edits (sharp operations; `D` = discarded silently, `I` = recorded in `Ignored`; "strict" = `XI_DIALECTS_STRICT` turns `I` for unknown/unsupported edits into 400):

| SIH edit | TR |
|---|---|
| `resize: {width, height, fit, position, background, kernel, withoutEnlargement, ratio}` | `P.Resize`: width/height via `Number()` + `Math.round`, `null` = auto, ≤ 0 → 400; `Enlarge = !withoutEnlargement` (sharp enlarges by default); `fit` `cover` (default) → Fill, `contain` → Fit + `Extend` (gravity = position) + `Background` (default opaque black), `fill` → Force, `inside` → Fit, `outside` → Outside; `position`/`gravity` (names or sharp constants: `centre`, `top`, `right top`, …, `north`…`northwest`, `entropy` → Entropy, `attention` → Smart) → `P.Gravity` for cover; `kernel` nearest/linear/cubic/mitchell/lanczos2/lanczos3 → `Algorithm`, `mks2013`/`mks2021` I; `ratio` (thumbor mapper) scales width/height or, alone, → `ZoomX/Y`; `withoutReduction`, `fastShrinkOnLoad` I |
| `extract`, `crop: {left, top, width, height}` | `P.Crop` (north-west + offsets). Before `resize` in key order → pre-resize crop; after it → a chained second pipeline (sharp crops after resizing), which also receives `extend` |
| `extend: n \| {top, right, bottom, left, background, extendWith}` | `P.Padding` + `P.Background`; `extendWith` other than `background` I |
| `trim: true \| n \| {background, threshold}` | `P.Trim` (threshold default 10, `Color`); `lineArt`, `margin` I; `false` = none |
| `rotate: n \| null` | multiple of 90 → `P.Rotate`; other angles → `P.RotateAny` normalised to (−180, 180]; `null` → `AutoRotate` |
| `flip`, `flop` | `Flip.V`, `Flip.H` (sharp: any non-boolean value means true) |
| `greyscale`, `grayscale` | `Grayscale` (same boolean rule; later key wins) |
| `negate` | `Negate` |
| `normalise`, `normalize` | `Equalize` (SIH's thumbor mapper maps `equalize()` to `normalize`); `{lower, upper}` I |
| `flatten: true \| {background}` | `P.Background` (default black) |
| `tint` | `P.Tint` |
| `sharpen: {sigma, …} \| other` | `P.Sharpen` = sigma (0.000001–10); flat/jagged params I; non-object = sharp's mild sharpen ≈ σ 0.5 I |
| `blur: σ \| {sigma} \| true` | `P.Blur` (0.3–1000); `true`/`null` = mild box blur ≈ σ 0.8 I; `false` = none |
| `modulate: {brightness, saturation, hue, lightness}` | `Adjust.Exposure`, `Adjust.Saturation`, `Adjust.Hue`, `Adjust.Brightness` (lightness × 2.55, I) |
| `toFormat` | `O.Format` (wins over `outputFormat`, as in SIH) |
| `jpeg`, `png`, `webp`, `avif`, `heif`, `gif`, `tiff: {…}` | select the output format like sharp (unless `force: false`); `quality` → `O.FormatQuality[fmt]`; jpeg `progressive`, `chromaSubsampling`, `mozjpeg` (= trellis + overshoot + optimise scans + table 3), `trellisQuantisation`, `overshootDeringing`, `optimiseScans`, `quantisationTable` → `O.JPEG`; png `progressive` (interlace), `compressionLevel`, `adaptiveFiltering`, `palette`, `colours` → `O.PNG`; webp `lossless`, `nearLossless`, `smartSubsample`, `preset` → `O.WebP`/`O.Lossless`; avif/heif `chromaSubsampling` → `O.AVIF`; `lossless` and `effort` (native 0–6 / 0–9 / 1–10, normalised to 1–9) apply only when that format is the output; other options I |
| `overlayWith: {bucket, key, wRatio, hRatio, alpha, options: {left, top, gravity}}` | `P.Watermarks` with a per-request `Source` (bucket must be listed, else 403), `Opacity` = 1 − alpha/100, `Scale` = wRatio/100 (`hRatio` I), position from `gravity` or `left`/`top` (px or `"<n>p"`; negative = from the right/bottom edge) — takes effect in M3 (I) |
| `smartCrop: true \| {faceIndex, padding}` | `Gravity` face + `FaceIndex` — M3 (I); `padding` > 0 I |
| `roundCrop: true \| {top, left, rx, ry}` | `P.Mask` circle with trim (SIH's default ellipse is a centred circle then `trim()`); explicit centre/radii approximated (ellipse when rx ≠ ry, I) |
| `animated: bool` | `true` → `Pages = -1`; `false` → `DisableAnimation` |
| `stripExif`, `stripIcc: true` | `O.StripMetadata`, `O.StripColorProfile` |
| `contentModeration`, `median`, `threshold`, `gamma` (linear-light resampling), `linear`, `recomb`, `convolve`, `clahe`, `boolean`, `affine`, `unflatten`, channel and colourspace operations | unsupported: I (strict 400) |
| anything else (`toFile`, …) | unknown: I (strict 400) |

Arguments sharp would reject at processing time (SIH answers 500) are a 400 naming the field here. Colours accept `{r, g, b, alpha}`, `#rgb[a]`, `#rrggbb[aa]`, `rgb()`/`rgba()` and the CSS level 2 names (plus `transparent`, `orange`).

Query-param mapper (`dialects.awssih.query_params`, default on; SIH always applies it): `format` → `toFormat`; `width`, `height` (`parseInt`; `"0"`/`""` → null), `fit` → a `resize` edit; `rotate` (`""` → null); `flip`, `flop`, `grayscale`/`greyscale` (`"0"`, `"false"`, `""` are false) → the edits of the same name. They merge like SIH's `{...edits, ...queryEdits}`: an existing edit keeps its position and takes the new value, so `?width=` replaces the whole JSON `resize` object. With the mapper off they are recorded in `Ignored`. For forwarded thumbor paths they are applied to the thumbor result the same way. Other query parameters are ignored (the S3 Object Lambda `ol-` prefixes are not recognised).

Signature (`XI_DIALECTS_AWSSIH_KEYS`, SIH `SECRETS_MANAGER`/`SECRET_KEY`): `signature` = lowercase hex(HMAC-SHA256(secret, msg)), msg = the decoded path after the mount, plus `?` and the query rebuilt like SIH's `recreateQueryString` when non-empty (see §2). Several keys may be configured for rotation; `XI_SIGNING_TRUSTED` applies; `XI_SIGNING_REQUIRED=true` is SIH's `ENABLE_SIGNATURE=Yes` (missing → 403 here, 400 in SIH). The signature and then `expires` are checked before the path is decoded, as in SIH. `expires=YYYYMMDDTHHmmssZ` → `D.Expires`; malformed → 400, past → the common expired status (404; SIH answers 400). Vectors in `internal/security/sign/testdata/awssih/` are produced by SIH's own code under Bun.

Response headers (JSON `headers`): names on SIH's `HEADER_DENY_LIST` (`authorization`, `set-cookie`, `x-amz-*`, `access-control-*`, `content-*` including `Content-Disposition`, …) are dropped and recorded in `Ignored`, as SIH drops them. Of the rest, the model expresses `Cache-Control` `max-age` → `D.TTL` and `immutable` → `D.Immutable` (other directives I; the server's config still clamps the TTL), and `Cache-Tag` / `Surrogate-Key` / `Edge-Cache-Tag` → `D.CacheTags`. Any other header (custom `x-*`, `Expires`, …) is recorded in `Ignored` and not sent; headers never fail a request.

Other differences from SIH: `AUTO_WEBP` is the server's Accept negotiation (`O.Format` unset); SIH keeps all metadata by default while xtandard-image applies `XI_PROCESSING_STRIP_METADATA` unless `stripExif`/`stripIcc` ask for stripping; an SVG source without an explicit format follows the server's format rules rather than SIH's "PNG when edited"; a request without edits or format is still processed by the pipeline (SIH streams the original bytes); `DEFAULT_FALLBACK_IMAGE_*` maps to `XI_FALLBACK_*`, `CORS_ORIGIN` to `XI_SERVER_CORS_ORIGINS`.

## 14. Compatibility test strategy

Each dialect WP ships (a) a table of URLs → expected TR JSON, (b) URLs produced by the upstream SDK/transformer (`@imgix/js-core`, `cloudinary` npm, `imagekit-javascript`, `unpic` transformers, `libthumbor`, imgproxy URL examples) generated by a small Bun script in `tools/compat/` and committed as fixtures, (c) signature test vectors from upstream docs, (d) a fuzz target that must never panic.
