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
| cloudflare-images | `/cdn-cgi/imagedelivery/` (and host-bound `imagedelivery.net` shape) | WP-79 (M2) |
| cloudflare_media | `/cdn-cgi/media/` (next to a Cloudflare mount whose prefix ends in `/cdn-cgi/image/`; host-bound and other prefixes reach it through `<prefix>/cdn-cgi/media/`) | Media Transformations, §8.2 (WP-125, M5) |
| imgix | `/imgix/<mount>/` | Usually host-bound |
| cloudinary | `/cloudinary/` then `/<cloud>/image/{upload,fetch}/…` | Or host-bound as `res.cloudinary.com`-like |
| imagekit | `/imagekit/<mount>/` | Or host-bound |
| bunny | `/bunny/<mount>/` | Or host-bound |
| thumbor | `/thumbor/` | Cannot share `/` with imgproxy (both signature-first) unless host-bound |
| awssih | `/sih/` | AWS Serverless Image Handler (§13); usually host-bound to the old CloudFront domain |
| simple:* | provider-native prefixes (`/_next/image`, `/_vercel/image`, `/.netlify/images`, `/_ipx/`, `/_image`, `/assets/`, `/storage/v1/render/image/public/`, `/storage/v1/object/public/`, `/is/image/`, `/v1/storage/buckets/`) and `/simple/<name>/` | Table-driven, see §12; `XI_DIALECTS_SIMPLE_HOSTS` binds a per-request dispatcher |

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
| cloudflare `/cdn-cgi/media` (xi) | optional `xi_sig` over the path after `/cdn-cgi/media` (none upstream) | query | §8.2 |
| awssih | `signature` = hex(HMAC-SHA256(secret, path [‖ "?" ‖ query])), secret used verbatim; query = decoded parameters without `signature`, last value per key, sorted by "key,value" (UTF-16), joined `k=v&…` unescaped; optional `expires=YYYYMMDDTHHmmssZ` | query | §13, SIH `image-request.ts` `validateRequestSignature` |
| wsrv, simple:* | none upstream | — | — |

For dialects without a native scheme xi offers the optional **xi scheme**: query param `xi_sig` = base64url(HMAC-SHA256(key, salt ‖ path ‖ "?" ‖ query-without-xi_sig)) using the imgproxy key/salt pairs. Unsigned traffic is governed by `XI_SECURITY_UNSIGNED_POLICY` (`allow` | `restricted` | `deny`), settable per mount. `restricted` is the recommended production setting for client-generated URLs (unpic): only widths in `XI_SECURITY_ALLOWED_WIDTHS` (default the Next.js device+image sizes: 16,32,48,64,96,128,256,384,640,750,828,1080,1200,1920,2048,3840), heights derived from width and aspect ratio or in the same list, qualities in `XI_SECURITY_ALLOWED_QUALITIES` (default 1–100 step 5 plus named levels), and no watermark URLs, security options or `raw`. The exact restricted option set (an allowlist that fails closed on new fields; it also admits DPRs from a router-level list and rejects info requests, chained pipelines, effects, crops, fallback URLs, cache busters and width/DPR client hints) is documented on `router.Restrictions` in `internal/dialect/router`.

## 3. imgproxy (canonical)

**Status:** on `main` (M1, M2, M3): every option below is parsed and applied. Detection options (WP-72/73), video thumbnails (WP-74) and video sprites and animations (`vtt`, `vta`, WP-75) need the `full` image; the slim image answers 422. Detection options also work on a video source, on the frame the request selects (WP-91), and in chained pipelines (WP-92); a sprite or animation is not detected. The advanced watermark options (`wmu`, `wmt`, `wms`, `wmr`, `wmsh`, `ch`) are on `main` (WP-77/78).

Reference: https://docs.imgproxy.net/usage/processing (imgproxy v4.0.x docs, source `imgproxy/imgproxy-docs` `docs/usage/processing.mdx`), https://docs.imgproxy.net/usage/getting_info, https://docs.imgproxy.net/usage/encrypting_source_url, https://docs.imgproxy.net/features/chained_pipelines.

### 3.1 Grammar

```
/%signature/%options.../plain/%source_url[@%extension]
/%signature/%options.../%base64url_source_url[.%extension]      (may be split with "/")
/%signature/%options.../enc/%aes_cbc_source[.%extension]        (may be split with "/")
/info/%signature/%info_options.../{plain/…|%base64…|enc/…}
/video/%signature/%video_options.../{plain/…[@%container]|%base64…|enc/…}   (§3.8)

option      = name ":" arg *(":" arg)        ; separator configurable (XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR)
pipeline    = option *("/" option)
pipelines   = pipeline *("/-/" pipeline)       ; chained pipelines
bool        = "1" | "t" | "true" | "0" | "f" | "false"
```

- Signature segment always present; `unsafe`/`_`/anything when signing is off.
- Plain source: `%`, `?`, `@` must be percent-encoded; everything after `@` is the extension.
- `IMGPROXY_BASE_URL`-style prefixing: `XI_DIALECTS_IMGPROXY_BASE_URL` prepended to relative sources. `XI_DIALECTS_IMGPROXY_URL_REPLACEMENTS` supported (regex → replacement) for migrations.
- Source schemes: `http(s)://`, `s3://`, `gs://`, `abs://`, `local:///`, `ftp://`, `sftp://`, `xi://<source>/`. `swift://` is not supported (open question).
- Named sources, `xi://<source>/<key>`: the key is relative to that source (fs root and prefix, default bucket, ftp/sftp root). For an `http` source (WP-95):
  - without `base_url`, the key must be an absolute `http(s)://` URL, fetched as is through that source's allowlist, headers and limits (`plain/xi://web/https://cdn.example.com/a.jpg`), so the name picks which allowlist and credentials apply; any other key is 403 `source "web" has no base_url: the key must be an absolute http(s) url`, a URL outside the allowlist 403 `source "web": host not in the source allowlist` (or `url scheme, port or path not in the source allowlist`);
  - with `base_url`, a relative key is joined to it (`xi://cdn/a.jpg` → `<base_url>/a.jpg`); an absolute URL under the base is taken as is, any other absolute URL is joined like a relative key (as before WP-95).

  The inner URL survives every source form: `plain/` literal (`plain/xi://web/https://h/a.jpg@webp`), percent-encoded as a whole (`plain/xi%3A%2F%2Fweb%2Fhttps%3A%2F%2Fh%2Fa.jpg`; decoded once, so the inner URL's own escapes such as `%2520` stay one level deep), proxy-collapsed (`xi:/web/https:/h/a.jpg`), base64 and `enc/`. Its query is the query after the key (`plain/xi://web/https://h/a.jpg%3Fv%3D2`). The bare `https://…` form keeps working: it goes to the first http source (the mount's first) whose allowlist matches. An unknown name is 403 `unknown source "<name>"`.
- Presets (`internal/preset`, expanded by the dialect at parse time): `preset:a:b` expands each preset in place, so later options override it argument by argument and it overrides earlier ones; the `default` preset is applied before the URL's options (in the first pipeline). A preset's first pipeline applies to the pipeline it is referenced from and its chained pipelines merge into the following ones (created when missing); a later URL `-` moves into them and URL options there win (imgproxy chained-pipelines docs example: `width:400/-/preset:test/width:500/-/width:600` with `test=width:300/height:300/-/width:200/height:200/-/width:100/height:200` → `width:400/-/width:500/height:300/-/width:600/height:200/-/width:100/height:200`). Presets may reference other presets; undefined references, cycles, expansions beyond 16 pipelines, bad arguments and unknown option names are startup errors (imgproxy skips recursive references at request time with a warning). An undefined preset in a URL is 400. Presets apply to processing URLs only: in `/info` URLs (imgproxy resolves those against separate info presets, not implemented) `preset` is recorded but not expanded, and neither the `default` preset nor only-presets mode applies. `XI_PRESETS_ONLY`: the first path segment is a separator-joined preset list (`/thumbnail:blurry:watermarked/plain/…`), further segments containing the separator must also be preset lists or `pr:`/`preset:` options, and anything else (options, unknown names, `-`, no preset at all) is 403 `forbidden`.
- Chained pipelines: pipeline-scoped options (geometry, effects, watermark, animation) apply to the pipeline they appear in; request-scoped ones (`O.*`, `D.*`, `TR.Limits`, `raw`) apply to the whole request wherever they appear (imgproxy `options.Main()`). Max 16 pipelines; an empty pipeline is 400. Each pipeline runs on the result of the previous one and is planned on its real size, frames and alpha (after a trim too); EXIF orientation, shrink-on-load, `page`/`pages` and `disable_animation` act in the first pipeline only, and every pipeline has its own filters, extend, padding, background and watermarks (WP-58). Detection options work in any pipeline: the faces and objects are detected once on the source and mapped through the geometry of the pipelines before (crops, resizes, rotations, flips, extend, padding, trim, smart crops; WP-92), so `c:300:503:ea/-/rs:fill:100:100/g:obj:face` crops on the face that the first pipeline kept. The same applies to Cloudinary and ImageKit chained components.
- Later occurrences of an option override earlier ones argument by argument (empty arguments keep the previous value); the source extension overrides `format` unless `raw`. An empty boolean is false; any other non-boolean is 400 (imgproxy: false). `sp` is accepted as a `skip_processing` alias because `imgproxy-js-core` emits it.
- Malformed option arguments → 400. Unknown option names are recorded in `TR.Ignored` and return 400 only with `XI_DIALECTS_STRICT=true` (ADR 0002). imgproxy itself returns 404 for both (`options/parser/errors.go`); see ROADMAP open question 3.
- Options tagged M3 below are fully parsed and validated into TR and also recorded in `TR.Ignored` (reason "takes effect in M3") until the executor implements them, so the server can ignore or 501 them; options tagged M2 are implemented and no longer recorded (every dialect). imgix `vib`, Cloudinary `e_vibrance` and ImageKit `e-contrast` parse into fields the planner still lists in `plan.Unplanned` (vibrance, equalize) and are recorded as "not implemented". Strict mode rejects only unknown names.

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
| `gravity` (`g`) | no,so,ea,we,noea,nowe,soea,sowe,ce[:x:y]; `sm`; `fp:x:y`; `obj:classes…` Pro; `objw:class:weight…` Pro | `P.Gravity`; `obj`: the crop centres on the union box of the detections of the classes (`face` = YuNet faces, COCO class names with `_` for spaces or supercategories such as `vehicle`, none = every class of both models; unknown class → 400); `objw`: on their weighted centre (weight × box area); nothing found → `sm`; without the detector (slim) → 422. `sm` crops on the union of the detected faces when the face model is loaded and finds some (`XI_DETECT_SMART_FACES`), else libvips attention | ce:0:0 | M1 (sm, fp), M3 (obj, objw: WP-72) |
| `objects_position` (`obj_pos`, `op`) Pro | type:x:y, `fp:x:y`, `prop` | `P.Gravity.ObjectsPosition` (kept when `gravity` changes): where the detections' box lands in the crop of `obj`/`objw` (and face) gravity: a compass type puts it against that edge, the offsets moving it away from the edge (pixels × DPR, or fractions < 1; for `ce` right/down); `fp:x:y` puts its centre at x, y of the crop; `prop` keeps its relative position (crop left = centre × (width − crop width)) | ce | M3 (WP-72) |
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
| `blur_detections` (`bd`) Pro | sigma:classes… | `P.BlurDetections`: Gaussian blur of each detection box (mapped through crops, rotations and flips like `blur_areas`), last among the filters | off | M3 (WP-73) |
| `draw_detections` (`dd`) Pro | bool:classes… | `P.DrawDetections`: each box outlined in its class colour (line ≈ 1/200 of the shorter side, ≥ 2 px) with a `class NN%` label, after the colour adjustments | off | M3 (WP-73) |
| `crop_objects` (`c_obj`) Pro | scale:classes… | `P.CropObjects`: pre-crop to the union box of the detections scaled by `scale` around its centre (kept inside the image), grown to the result's aspect ratio for fill/fill-down; takes precedence over `crop`; nothing detected → no crop | off | M3 (WP-72) |
| `colorize` (`col`) Pro | opacity:color:keep_alpha | `P.Colorize` | off | M2 |
| `gradient` (`gr`) Pro | opacity:color:direction:start:stop; direction `down`/`up`/`left`/`right` or degrees clockwise like imgproxy (0 = top to bottom like `down`, 90 = right to left like `left`) | `P.Gradient` | 0:000:down:0:1 | M2 |
| `watermark` (`wm`) | opacity:pos:x:y:scale; pos incl. `re` (tile), `ch` (chessboard, Pro) | `P.Watermarks[i]`; `ch` tiles a 2×2 unit (cell, empty, empty, cell) centred on a cell | off | M1 (`re` M1, `ch` M3 WP-77) |
| `watermark_url` (`wmu`) Pro | base64url URL | `Watermark.Source`: resolved against the source allowlist and fetched through the source fetcher (SSRF policy, `XI_LIMITS_MAX_SRC_BYTES`, origin cache), SVG sanitised; first frame, EXIF orientation applied | config image | M3 (WP-77) |
| `watermark_text` (`wmt`) Pro | base64url Pango markup | `Watermark.Text` (replaces `watermark_url`); sanitised to a whitelist (b, big, i, s, small, sub, sup, tt, u, span with font/colour/weight/style/decoration attributes), font sizes ≤ 1000 pt, rendered at 72 × DPR DPI with `XI_PROCESSING_WATERMARK_FONT`, wrapped at the frame width and shrunk to fit the frame | — | M3 (WP-78) |
| `watermark_size` (`wms`) Pro | w:h | `Watermark.Width/Height`: fit into w×h CSS px (DPR applied), enlarging; 0 from the aspect ratio; only when the `wm` scale is 0 | 0:0 | M3 (WP-77) |
| `watermark_rotate` (`wm_rot`, `wmr`) Pro | degrees | `Watermark.Rotate`: clockwise after sizing, onto the transparent bounding box | 0 | M3 (WP-77) |
| `watermark_shadow` (`wmsh`) Pro | sigma | `Watermark.Shadow`: the watermark's alpha in black, blurred with sigma (CSS px, DPR applied), under it | off | M3 (WP-77) |
| `style` (`stl`, `st`) Pro | base64url CSS | `P.SVGStyle`: a `<style>` first child of the root of an SVG source, the document sanitised again with it (external references dropped); skipped for other sources | — | M2 (WP-64) |
| `strip_metadata` (`sm`) | bool | `O.StripMetadata` | config (true) | M1 |
| `keep_copyright` (`kcr`) | bool | `O.KeepCopyright` | config (true) | M1 |
| `dpi` Pro | int | `O.DPI`: output resolution (JPEG JFIF/Exif, PNG pHYs, TIFF), kept when stripping (FORMATS §5) | 0 | M2 (WP-64) |
| `strip_color_profile` (`scp`) | bool | `O.StripColorProfile` | config (true) | M1 |
| `preserve_hdr` (`ph`) | bool | `O.PreserveHDR` | config (false) | M2 |
| `gain_map` (`gm`) | auto, hdr, sdr | `O.GainMap`: an HDR gain map (UltraHDR, ISO 21496-1 JPEG, AVIF or HEIC tmap) is applied for AVIF (PQ), JPEG XL (float) and JPEG (UltraHDR) outputs with `preserve_hdr` (`auto`) or always (`hdr`); `sdr` keeps the SDR base (FORMATS §1.4) | auto | M4 (WP-104) |
| `color_profile` (`cp`, `icc`) Pro | srgb, cmyk, file | `O.ColorProfile` | — | M2 |
| `enforce_thumbnail` (`eth`) | bool | `O.EnforceThumbnail`: HEIF/AVIF embedded thumbnail always; JPEG Exif thumbnail when large enough (FORMATS §5) | config | M2 (WP-64) |
| `quality` (`q`) | 0..100 | `O.Quality` | 0 (config) | M1 |
| `format_quality` (`fq`) | fmt:q… | `O.FormatQuality` | — | M1 |
| `autoquality` (`aq`) Pro | method:target:min:max:allowed_error; method `none`/`size`/`dssim`/`ml` (`ml` parsed, run as no auto-quality and reported skipped); `size` target in bytes (≤ 2³⁰) | `O.AutoQuality` | config | M2 (WP-56, FORMATS §6.4) |
| `max_bytes` (`mb`) | int | `O.MaxBytes` | 0 | M1 |
| `jpeg_options` (`jpgo`) Pro | progressive:no_subsample:trellis_quant:overshoot_deringing:optimize_scans:quant_table | `O.JPEG` (the last four need a mozjpeg build) | config | M2 (WP-64) |
| `png_options` (`pngo`) Pro | interlaced:quantize:quantization_colors | `O.PNG` | config | M2 (WP-64) |
| `webp_options` (`webpo`) Pro | compression:smart_subsample:preset | `O.WebP` | config | M2 (WP-64) |
| `avif_options` (`avifo`) Pro | subsample | `O.AVIF` | config | M2 (WP-64) |
| `format` (`f`, `ext`) | extension or `best` Pro | `O.Format` | source/negotiated | M1 (`best` M2, `mp4`/`webm`/`hevc` M3 WP-76) |
| `page` (`pg`) Pro | int | `P.Page`: page of a multi-page still, first frame of a kept animation, the frame of one that is not (FORMATS §1.2, §3) | 0 | M2 (WP-50, WP-62) |
| `pages` (`pgs`) Pro | int | `P.Pages`: pages or frames stacked vertically; the frame count of a kept animation; `-1` = to the end | 1 | M2 (WP-50, WP-62) |
| `disable_animation` (`da`) Pro | bool | `P.DisableAnimation` | false | M1 |
| `video_thumbnail_second` (`vts`) Pro | float | `P.Video.Second`: the frame of a video source (FORMATS §1.5); negative counts from the end, clamped to the video; unset = `XI_VIDEO_THUMBNAIL_SECOND` (default `auto`: best-frame selection); ignored for images | config | M3 (WP-74) |
| `video_thumbnail_keyframes` (`vtk`) Pro | bool | `P.Video.Keyframes`: the keyframe at or before `vts` | config | M3 (WP-74) |
| `video_thumbnail_tile` (`vtt`) Pro | step:cols:rows:tw:th:extend:trim:fill:fx:fy; step 0 = off, < 0 = from duration (stored as 0) | `P.Video.Tile` | off | M3 (WP-75) |
| `video_thumbnail_animation` (`vta`) Pro | step:delay(ms):frames:fw:fh:extend:trim:fill:fx:fy; step as `vtt` | `P.Video.Animation` (delay in seconds) | off | M3 (WP-75) |
| `fallback_image_url` (`fiu`) Pro | base64url URL | `D.Fallback`; honoured only with `XI_FALLBACK_ALLOW_REQUEST_URL` and when the source allowlist accepts it | config | M1 (WP-34c) |
| `skip_processing` (`skp`, `sp`) | ext… | `D.SkipProcessing` | — | M1 |
| `raw` | bool | `TR.Kind = Raw` | false | M1 |
| `cachebuster` (`cb`) | string | `D.CacheBuster` | — | M1 |
| `bypass_cache` (`bc`) Pro | bool | skips result cache (needs `XI_CACHE_ALLOW_BYPASS`) | false | M2 (WP-52) |
| `cache_tags` (`ct`) Pro | tags… (visible ASCII, no commas; replaces earlier tags) | `D.CacheTags` → `Cache-Tag`/`Surrogate-Key` header (`XI_CACHE_TAGS_HEADER`), on result-cache hits too | — | M2 (WP-34b, WP-64 end-to-end test) |
| `expires` (`exp`) | unix ts | `D.Expires` | — | M1 |
| `filename` (`fn`) | name:encoded | `D.Filename` | source basename | M1 |
| `return_attachment` (`att`) | bool | `D.Attachment` | config | M1 |
| `preset` (`pr`) | names… | expanded in place | — | M1 |
| `hashsum` (`hs`) Pro | none, or md5/sha1/sha256/sha512:hex | `TR.Source.Hashsum` (lowercased): checked on the fetched bytes (processing, raw, `/info`) before anything parses them; mismatch → 422 `hash_mismatch`, never the fallback image; part of the canonical key | none | M2 (WP-64) |
| `pdf_password` (`pdp`) | encrypted password (base64url) | `TR.Source.Password`: `.Secret` (decrypted; never logged, echoed or keyed) and `.Digest` (keyed hash, part of the canonical key): opens a password-protected PDF. Signed URLs only (verified signature, else 403); encrypted like an `enc/` source with `XI_ENCRYPTION_KEYS`; empty removes it ([§3.7](#37-password-protected-pdfs-wp-115)) | none | M4 (WP-115) |
| `max_src_resolution` (`msr`), `max_src_file_size` (`msfs`), `max_animation_frames` (`maf`), `max_animation_frame_resolution` (`mafr`), `max_result_dimension` (`mrd`) | int | `TR.Limits` (only if security options allowed) | config | M1 |
| removed upstream: `gif_options` (`gifo`) | — | accepted and ignored for old URLs | — | — |

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
| `video_meta` (`vm`) | bool | `Info.VideoMeta`: `video_meta` (container tags plus container, codec, coded size, rotation, duration, fps, bit rate, frames) and `video_streams` for video sources (FORMATS §1.5) | true | M3 (WP-74) |
| `detect_objects` (`do`) Pro | bool | `Info.DetectObjects` → `"objects": [{class_id, class_name, confidence, left, top, width, height, landmarks}]` (fractions of the oriented image; faces first with five landmarks, then objects; `[]` when none); 422 without the detector | false | M3 (WP-73) |
| `classify` (`cl`) Pro | top_k:class_names… | `Info.ClassifyTopK`, `Info.ClassifyClasses` → `"classification": [{class_id, name, confidence}]`, best first, top_k capped by `XI_DETECT_CLASSIFY_TOP_K_MAX`, classes below `XI_DETECT_CLASSIFY_THRESHOLD` left out (imgproxy Pro's shape). The `full` and `gpl` images classify with MobileNetV3-Large (ImageNet-1k, CPU, run only when asked; ARCHITECTURE "Classification"); class names are the 1000 ImageNet names (`tabby cat`, `golden retriever`, …; case-insensitive, `_` for a space) and an unknown one is 400. Without a classifier (slim) the key is absent and `X-Xi-Ignored` lists `classify` ("classification needs the full image"). imgproxy Pro's bundled model is a different, multi-label one (560+ Open Images classes), so ids, names and scores differ | 0 | M4 (WP-110) |
| `colorspace` (`cs`) | bool | `Info.Colorspace` | false | M1 |
| `bands` (`b`) | bool | `Info.Bands` | false | M1 |
| `sample_format` (`sf`) | bool | `Info.SampleFormat` | false | M1 |
| `pages_number` (`pn`) | bool | `Info.PagesNumber` | false | M1 |
| `alpha` (`a`) | alpha:check_transparency | `Info.Alpha` (check_transparency: `Ignored`, not in model) | false:false | M1 |
| `crop` (`c`) | w:h:gravity (as processing `crop`) | `Info.Crop` | — | M2 |
| `objects_position` (`obj_pos`, `op`) Pro | as processing | `Info.ObjectsPos` + `P.Gravity.ObjectsPosition`: places an info `crop` with face or object gravity on the detections | ce:0:0 | M3 (WP-73) |
| `palette` (`p`) | 0 or 2..256 | `Info.Palette` | 0 | M2 |
| `average` (`avg`) | enabled:ignore_transparent | `Info.Average` | false:true | M2 |
| `dominant_colors` (`dc`) | enabled:build_missed | `Info.DominantColors` | false:false | M2 |
| `blurhash` (`bh`) | x:y (1..9 each, 0:0 off) | `Info.Blurhash` | 0:0 | M2 |
| `thumbhash` (`thumb_hash`, `th`) | bool | `Info.Thumbhash` | false | M2 |
| `perceptual_hash` (`phash`, `ph`) | bool | `Info.PHash` | false | M2 |
| `calc_hashsums` (`chs`) | md5, sha1, sha256, sha512… | `Info.CalcHashsums` | — | M2 |
| `page` (`pg`), `video_thumbnail_second` (`vts`), `video_thumbnail_keyframes` (`vtk`), `cachebuster` (`cb`), `expires` (`exp`), `preset` (`pr`), `hashsum` (`hs`), `pdf_password` (`pdp`), `max_src_resolution` (`msr`), `max_src_file_size` (`msfs`), `bypass_cache` (`bc`), `cache_tags` (`ct`) | as in §3.2 | as in §3.2 | as in §3.2 | as in §3.2 |

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

A hint is read when the URL opts into it (imgix `ch=Width,DPR,Save-Data`, ImageKit `w-auto`/`dpr-auto`, Cloudinary `w_auto[:step]`/`dpr_auto`, Cloudflare `width=auto` and `slow-connection-quality`; each sets `transform.ClientHints`, which applies to the first pipeline) or, with `XI_CLIENT_HINTS_ENABLED`, whenever the URL leaves the value unset. `negotiate.ApplyClientHints` resolves hints into the request before defaults, so hinted values are part of the canonical key and ETag (Save-Data through `qualityDelta`, ARCHITECTURE §9.1).

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

### 3.6 Watermarks (WP-28, WP-77, WP-78; all dialects)

Every dialect overlay becomes a `Pipeline.Watermarks[i]` entry; the planner turns each into a `WatermarkStep` (`internal/pipeline/plan`) and the executor draws it after the pipeline's steps, once on the joined strip of an animation (`internal/pipeline/exec/watermark.go`, `text.go`).

| Kind | imgproxy | imgix | Cloudinary | ImageKit | thumbor | imaginary | SIH |
|---|---|---|---|---|---|---|---|
| Configured image (`XI_PROCESSING_WATERMARK_SOURCE`) | `wm` without `wmu`/`wmt` | — | — | — | — | — | — |
| Per-request image | `wmu` | `mark` (+`mark-base`) | `l_<id>`, `l_fetch:` | `l-image` | `watermark(url,…)` | `/watermarkimage?image=` | `overlayWith` |
| Text | `wmt` (markup) | `txt` (plain) | `l_text:` (plain) | `l-text` (plain) | — | `/watermark?text=` (markup) | — |
| Size | `wm` scale, `wms` | `mark-scale`, `mark-w/h` | `w_`/`h_`, `fl_relative` | `w`/`h` | `w_ratio` | — | `wRatio` |
| Rotation | `wmr` | `mark-rot` | `a_` | `rt` | — | — | — |
| Shadow | `wmsh` | `txt-shad` | — | — | — | — | — |
| Tiling | `wm` `re`, `ch` (chessboard) | `mark-tile=grid` | `fl_tiled` | — | `repeat` | default (`noreplicate` off) | — |

Execution order: fit into Scale × the frame (both sides, enlarging), else into `Width`×`Height` (CSS px × DPR; 0 from the aspect ratio) → rotate clockwise onto the transparent bounding box (premultiplied) → shadow (black copy of the alpha blurred with `Shadow` sigma, CSS px × DPR, canvas grown by 3σ) → alpha × request opacity × `XI_PROCESSING_WATERMARK_OPACITY` → tile spacing → place by gravity with overflow (imgproxy `calcPosition`), or repeat (`re`, a tile centred) or chessboard (`ch`: a 2×2 unit with the tile top-left and bottom-right, a tile centred) → composite over, back to the base band format.

Per-request images are a source fetch: the server resolves them against the source allowlist (403 otherwise) and fetches them through the same fetcher as the source (SSRF dialer and redirect policy, `XI_LIMITS_MAX_SRC_BYTES`, origin cache, coalescing), in parallel and before admission; an SVG is sanitised; the executor decodes the first frame with EXIF orientation applied under `XI_LIMITS_MAX_SRC_PIXELS`. A watermark URL is part of the URL, so it is covered by the URL signature; imgproxy's `wmu` is url-safe base64 like its source. Unsigned requests on `restricted` mounts cannot carry watermarks at all (`router.Restrictions`). The watermark bytes are not part of the ETag (the URL is, through the canonical key).

Text: markup from the URL (`wmt`, imaginary `text`) is untrusted; dialects whose text is plain escape it. The executor sanitises every text again (`internal/security/pango`): a strict XML token stream re-serialised with only `b`, `big`, `i`, `s`, `small`, `sub`, `sup`, `tt`, `u` and `span` with font, size, colour, alpha, weight, style, variant, stretch and decoration attributes (each value checked against its grammar); no DTDs, entities beyond XML's five, comments, processing instructions, namespaces or other attributes (400); at most 4096 bytes, nesting 8, every font size (with `big`/`larger` compounding) ≤ 1000 pt (422). Pango markup cannot reference files. Font descriptions allow letters, digits, spaces and `,-_.` only; a size in `px` is read as points. Text is rendered by `vips_text` at 72 × DPR DPI (one point per CSS pixel) in `XI_PROCESSING_WATERMARK_FONT` (default `sans 16`, imgproxy's; a request font lacking a family or size takes it from there), wrapped at `TextWidth` or the frame width, and must fit the frame: when the markup's size bound does not prove it fits, it is laid out to fit the frame (at most `XI_LIMITS_MAX_WATERMARK_TEXT_PIXELS`) first and shrunk when larger; text that does not fit even at 1 DPI is 422. Fonts: the image ships DejaVu Sans and DejaVu Sans Mono (`sans`, `monospace`, Bitstream Vera licence) through fontconfig; `XI_PROCESSING_WATERMARK_FONT_FILE` registers one more file.

### 3.7 Password-protected PDFs (WP-115)

A PDF that needs a user (open) password answers 422 `password-protected documents are not supported` unless the request brings the password. Each document has its own, so it travels in the URL, encrypted:

```
/<signature>/pdp:<token>/rs:fit:800:0/f:webp/plain/s3://docs/contract.pdf
/q/?url=s3://docs/contract.pdf&w=800&pdf_password=<token>&xi_sig=<sig>
token = base64url_unpadded( IV[16] ‖ AES-CBC(key, IV, PKCS#7(password)) )
```

- The token is encrypted exactly like an `enc/` source ([§3.1](#31-grammar)), with the first key of `XI_ENCRYPTION_KEYS` (every key decrypts, for rotation). Without encryption keys the option is 403. `@xtandard/image-url` encrypts it client-side (`encryptPdfPassword`, or `pdfPassword` on a request); the playground's password field is encrypted by the server in `api/sign`.
- **Signed URLs only.** The option is refused with 403 (`pdf_password requires a signed url`) unless the URL's signature was verified (an unsigned URL, or one on the `XI_SIGNING_TRUSTED` list, never qualifies), and the check runs before the token is decrypted: CBC has no authentication, so an unsigned URL must not reach the decryption (padding oracle). The server repeats the check after routing.
- The query dialect takes `pdf_password=<token>` (or `pdp=`) in both native and endpoint mode, and `o=pdp:<token>`; the same signature rule applies (`sign` or `xi_sig`). Other dialects refuse it inside their imgproxy passthroughs (403).
- The decrypted password goes to PDFium when the document is opened (probe, render, `/info`, samples) and nowhere else: it is not logged, not in errors, `/info`, `X-Xi-*` headers or the playground's `api/meta`; libvips' copy is wiped once the loader is built and the request's copies when it is served. The access log shows the segment as `pdp:REDACTED`. The canonical key (result cache, ETag) holds `Password.Digest`, an HMAC-SHA256 of the password under a key derived from the encryption key, never the password or an unkeyed hash of it.
- Wrong password → 422 `wrong document password`, never replaced by the fallback image (a client error, like a hashsum mismatch); no password → the existing 422, which a configured fallback image answers like any unreadable source. A password on a document that needs none, or on another format, is ignored (only the digest changes the key).
- A source may carry a default password (`XI_SOURCES_<NAME>_PDF_PASSWORD`, or `_FILE` / `{file: …}`; [CONFIG.md](CONFIG.md)), used when the request has none; the request's own password wins.

### 3.8 Video transformations (WP-122, WP-123)

**Status:** M5 (WP-122 video options, WP-123 audio), `full` and `gpl` images with `XI_VIDEO_TRANSFORM_ENABLED=true` (off by default; the `slim` image refuses to start with it). Design: [ADR 0012](adr/0012-video-transformations.md). imgproxy has no video transcoding: this endpoint borrows its URL shape, its signing and the names and argument rules of its geometry options, and adds time, codec and audio options. Parsed by `imgproxy.Parser.ParseVideo` (the path after `/video`) into `TR.Kind = Video` and `TR.Video` (`transform.VideoOp`).

```
/video/%signature/%video_options.../plain/%source_url[@%container]
/video/%signature/%video_options.../%base64url_source_url[.%container]
/video/%signature/%video_options.../enc/%aes_cbc_source[.%container]
```

- **Signature:** the HMAC covers `/video/` + the path after the signature segment (`/video/rs:fit:640:0/plain/…@webm` is signed as that string), so a signed image URL (a poster of the same source, for example) is never a valid video transformation. Trusted signatures (`XI_SIGNING_TRUSTED`) pass as for images. **Signed only by default:** an unsigned video URL is 403 before anything is fetched unless `XI_VIDEO_TRANSFORM_UNSIGNED_POLICY=allow`, and even then a `restricted` mount refuses it ([CONFIG.md](CONFIG.md)).
- **Output:** the source extension (`@webm`, `.m4a`) or `format` picks the container, `mp4` by default; [§3.9](#39-video-outputs-and-limits) lists what each container holds on each image. `png` and `webp` render a waveform picture of the audio instead of a media file.
- **Options:** the table below; each later occurrence overrides the previous one argument by argument, like processing options. Video options have their own namespace: image-only options (`bl`, `wm`, `pd`, …) are unknown here (recorded in `TR.Ignored`, 400 with `XI_DIALECTS_STRICT=true`). No chained pipelines (400), no presets (`pr` is unknown; in only-presets mode every `/video` URL is 403). Values are validated while parsing: an out-of-range one (`crf:64`, `sr:44000`, `vol:2` with `mu:1`, `vc:vp9` in MP4) is a 400 naming the field (`invalid video.crf: …`).
- **Geometry** follows ADR 0012 §2 and the image pipeline: the display matrix is applied first, then `rot` and `fl`, then `c` (a rectangle of the rotated frame, 0 for a whole side, placed by its own gravity, else by `g`, centred by default; the gravity's offsets move it towards the inside), then the resize. Resizing types map to the video fits: `fit` is scale-down (contain with `el:1`, padded with `bg` to the box with `ex:1`, the frame placed at `g`), `fill` and `fill-down` cover the box and crop it at `g`, `force` stretches; gravities are compass points only (`VideoOp.Gravity`, shared with the WP-125 dialects): `fp` and content-aware gravities are 400; a fill without both sides is a fit; `auto` is 400 (it needs the probe). Sizes are pixels (no fractions) and are rounded down to even for 4:2:0 video.
- **Time:** `start`/`end`/`duration` in seconds (microsecond precision). The cut is exact: the demuxer seeks to the keyframe before `start` and frames before it are dropped. `end` and `duration` are exclusive. A cut past the end of the source, an output longer than `XI_VIDEO_TRANSFORM_MAX_OUTPUT_DURATION` and a `fade` out of a source of unknown duration are 422.
- **Audio** (WP-123): `mute` drops the stream; audio-only containers drop the video, and a source without audio gives 422 `the source has no audio stream`. The audio chain is the cut, `volume`, `loudnorm` (EBU R128, single-pass FFmpeg `loudnorm`; the target is per request within FFmpeg's bounds: -70..-5 LUFS, -9..0 dBTP, 1..50 LU), the fades (`afade`), then resampling to `sample_rate` and the `audio_channels` layout (a source with more than two channels is downmixed to stereo). Audio sources (MP3, AAC/ADTS, AC-3, E-AC-3, Ogg, FLAC, WAV) are accepted as well as video containers with audio.
- **Waveform** (WP-123, like Cloudinary `fl_waveform`): `waveform:1` (or the `png`/`webp` container) draws the cut of the audio with FFmpeg `showwavespic` into a `width` × `height` RGBA picture (default 1024×256) in `color` (default black) on `background` (empty: transparent), mono or one band per channel; libvips encodes it as lossless PNG or WebP. A video source's audio track works too.
- **Overlays** (WP-124): imgproxy's watermark options (`wm`, `wmu`, `wmt`, `wms`, `wmr`, `wmsh`) parse exactly as in §3.2 and become one image or text overlay (`TR.Video.Overlays`), drawn on every frame of its window: `ow:start:end` (seconds of the output; 0 end = the end) and `of:in:out` fades. `wm` with an opacity above 0 is required, as on images (`ow`/`of` alone are recorded in `TR.Ignored`); `wm` without `wmu`/`wmt` is the configured watermark image. `tc:hms|seconds[:position:x:y:size:color:opacity]` adds a running timecode (FFmpeg `drawtext` with a fixed expansion; no request text). Overlay images are fetched like sources (allowlists, signing). Audio-only outputs and waveforms refuse overlays (400).
- **Query dialect:** `/q/video?url=…` takes the same options as query keys ([§4](#4-query-dialect-native--imaginary)).
- **Streams** (WP-126, [§3.10](#310-hls-and-dash-streams-wp-126-every-dialect)): the `m3u8` or `mpd` extension (or `format`), or `st:hls|dash[:height…]`, answers an HLS master playlist or a DASH MPD instead of a file; `sgd` sets the segment length. Everything else applies to every rendition, except a size (`w`, `h`, `s`, `rs` sizes), `vb` and watermarks, which are 400 on a stream (the renditions decide sizes and bit rates); `ki` sets keyframes within segments (default: one per segment).
- **Raw** (WP-128): `raw:1` streams the media source unmodified (the playground plays it as the "before" of a comparison). It stays a video request: the `/video` signature, the unsigned policy, `XI_ROLE`, the allowlists, the SSRF-safe fetcher and `XI_VIDEO_TRANSFORM_MAX_INPUT_BYTES` apply, and the source must be a video or audio file (sniffed from its first bytes; anything else is 422). Every other video option is ignored, nothing is transcoded or cached, and the answer supports `Range` (206, 416, `If-Range`) with the source's own `Content-Type` (`video/mp4`, `video/webm`, `audio/mpeg`, …).
- **Not yet:** keyframe-only ("fast") cuts; `/info` reports the source's streams, not the planned output.

| Option (aliases) | Args | Meaning (`TR.Video` field) | Default | Milestone |
|---|---|---|---|---|
| `start` (`vs`) | seconds | Start the output at this time of the source (exact, from the keyframe before it). | 0 | M5 (WP-122) |
| `end` (`ve`) | seconds | End the output at this time of the source (exclusive with duration). | end of the source | M5 (WP-122) |
| `duration` (`vd`) | seconds | Length of the output from start (exclusive with end). | to the end | M5 (WP-122) |
| `resize` (`rs`) | type:w:h:enlarge:extend | Resize like imgproxy: fit (scale-down, or contain with enlarge; extend pads), fill and fill-down (cover, cropped by gravity), force (stretch). | fit:0:0:false:false | M5 (WP-122) |
| `size` (`s`) | w:h:enlarge:extend | Resize without the type. | 0:0:false:false | M5 (WP-122) |
| `resizing_type` (`rt`) | fit, fill, fill-down, force | Resizing type (auto is not available for video). | fit | M5 (WP-122) |
| `width` (`w`) | px | Output width (rounded down to even). | 0 | M5 (WP-122) |
| `height` (`h`) | px | Output height (rounded down to even). | 0 | M5 (WP-122) |
| `enlarge` (`el`) | bool | Let fit enlarge a smaller source. | false | M5 (WP-122) |
| `extend` (`ex`) | bool | Pad a fit to the whole box with the background colour. | false | M5 (WP-122) |
| `gravity` (`g`) | type:x:y | Anchor of fill crops, pads and crops (a compass point; the pixel offsets move crops). | ce:0:0 | M5 (WP-122) |
| `crop` (`c`) | w:h:gravity | Crop a rectangle (pixels) of the rotated frame before the resize. | — | M5 (WP-122) |
| `rotate` (`rot`) | multiple of 90 | Rotate clockwise after the display matrix. | 0 | M5 (WP-122) |
| `flip` (`fl`) | h:v | Mirror after the rotation. | false:false | M5 (WP-122) |
| `background` (`bg`) | hex | Colour of the bars of extend. | 000000 | M5 (WP-122) |
| `fps` | rate | Output frame rate (capped by XI_VIDEO_TRANSFORM_MAX_FPS). | source | M5 (WP-122) |
| `format` (`f`, `ext`) | mp4, webm, m4a, mp3, opus, ogg, flac, wav, png, webp, m3u8, mpd | Output container (the source extension sets it too); png and webp are waveform pictures, m3u8 and mpd streams (WP-126). | mp4 | M5 (WP-122) |
| `video_codec` (`vc`) | auto, h264, av1, vp9, hevc | Video codec (h264 and hevc need the gpl image). | auto | M5 (WP-122) |
| `audio_codec` (`ac`) | auto, aac, opus, mp3, flac, pcm | Audio codec (must suit the container). | auto | M5 (WP-122) |
| `quality` (`q`) | 1..100 | Video quality, mapped onto the codec's CRF scale. | 0 | M5 (WP-122) |
| `crf` | 0..63 | Constant rate factor (wins over quality). | 0 | M5 (WP-122) |
| `video_bitrate` (`vb`) | bit/s (k, M) | Cap the video bit rate (50k..200M). | uncapped | M5 (WP-122) |
| `keyframe_interval` (`ki`) | seconds | Time between keyframes (GOP length, 0.1..60 s). | 2 | M5 (WP-122) |
| `mute` (`mu`) | bool | Drop the audio stream. | false | M5 (WP-123) |
| `audio_bitrate` (`ab`) | bit/s (k) | Audio bit rate (8k..512k; ignored by flac and wav). | codec default | M5 (WP-123) |
| `sample_rate` (`sr`) | Hz | Output sample rate (the codec must support it: Opus 8/12/16/24/48 kHz). | source | M5 (WP-123) |
| `audio_channels` (`ach`) | 1 or 2 | Downmix to mono or stereo. | source | M5 (WP-123) |
| `volume` (`vol`) | factor or NdB | Change the audio level (-60 dB..+20 dB). | 1 | M5 (WP-123) |
| `loudnorm` (`ln`) | enabled:I:TP:LRA | EBU R128 loudness normalisation (single-pass loudnorm). | false:-23:-1:7 | M5 (WP-123) |
| `fade` (`fd`) | in:out | Fade in from black and silence, and out to them (seconds). | 0:0 | M5 (WP-123) |
| `waveform` (`wf`) | enabled:color:background:split | Render the audio as a width × height (default 1024×256) PNG or WebP picture. | false:000000::false | M5 (WP-123) |
| `overlay_window` (`ow`) | start:end | Show the watermark overlay (wm, wmu, wmt) from start to end seconds of the output. | 0:0 | M5 (WP-124) |
| `overlay_fade` (`of`) | in:out | Fade the watermark overlay in and out (seconds, within its window). | 0:0 | M5 (WP-124) |
| `timecode` (`tc`) | format:position:x:y:size:color:opacity | Draw the output's running time (FFmpeg drawtext with a fixed expansion). | none:sowe:0:0:0:ffffff:1 | M5 (WP-124) |
| `stream` (`st`) | protocol:height… | Deliver an HLS or DASH stream (as the m3u8 or mpd extension does) with these renditions (at most 8; none: the configured ladder). Rungs above the source are dropped. | none (a file) | M5 (WP-126) |
| `segment_duration` (`sgd`) | seconds | Length of a stream's segments (1..30 s). | XI_VIDEO_TRANSFORM_SEGMENT_DURATION (6) | M5 (WP-126) |
| `watermark` (`wm`), `watermark_url` (`wmu`), `watermark_text` (`wmt`), `watermark_size` (`wms`), `watermark_rotate` (`wm_rot`, `wmr`), `watermark_shadow` (`wmsh`), `cachebuster` (`cb`), `bypass_cache` (`bc`), `cache_tags` (`ct`), `expires` (`exp`), `filename` (`fn`), `return_attachment` (`att`), `hashsum` (`hs`), `raw` | as in §3.2 | as in §3.2 | as in §3.2 | as in §3.2 |

### 3.9 Video outputs and limits

| Container | Content type | Video | Audio | Notes |
|---|---|---|---|---|
| mp4 (default) | `video/mp4` | H.264 (`gpl`), else AV1; `vc:hevc` on `gpl` | AAC | `moov` first (faststart) |
| webm | `video/webm` | VP9; `vc:av1` | Opus | Cues first |
| m4a | `audio/mp4` | — | AAC | |
| mp3 | `audio/mpeg` | — | MP3 (LAME) | |
| opus, ogg | `audio/ogg` | — | Opus | same file, `.opus` or `.ogg` name |
| flac | `audio/flac` | — | FLAC | `ab` ignored |
| wav | `audio/wav` | — | 16-bit PCM | `ab` ignored |
| png, webp | `image/png`, `image/webp` | — | — | waveform picture (lossless) |

Codec choices (`vc`, `ac`) must suit the container (400 otherwise); one the image cannot encode (H.264 or HEVC on `full`) is 422 before the source is fetched. Sample rates: AAC 8–96 kHz, MP3 8–48 kHz, Opus 8, 12, 16, 24 or 48 kHz (the stream always declares 48 kHz), FLAC and WAV any listed rate; an unsupported pair is 422. Bit rates: video 50 kbit/s–200 Mbit/s (`vb` caps it; with `crf`/`q` it is constrained quality), audio 8–512 kbit/s. The server limits (`XI_VIDEO_TRANSFORM_MAX_*`: input bytes, duration and pixels; output bytes, duration, pixels and frame rate) answer 422; queue overflow 429, queue timeout 503, a transcode over `XI_VIDEO_TRANSFORM_TIMEOUT` 504. Responses support `Range` (206, 416, `If-Range`) and are cached with the resolved output (`<container>:<video codec>:<audio codec>`) in the key ([FORMATS.md §5.2](FORMATS.md)).

Examples:

```
/video/<sig>/rs:fill:640:360/g:soea/vs:12/vd:8/plain/s3://media/talk.mov@webm
/video/<sig>/w:1280/q:70/ki:1/fd:0.5:1/ln:1/plain/https://example.com/clip.mp4@mp4
/video/<sig>/vs:30/vd:60/sr:44100/ach:1/ab:96k/plain/https://example.com/clip.mp4@mp3
/video/<sig>/wf:1:2266cc:ffffff/w:800/h:160/plain/https://example.com/podcast.mp3@png
/q/video?url=https://example.com/clip.mp4&start=1&duration=5&width=640&format=webm&mute=1&xi_sig=<sig>
```

### 3.10 HLS and DASH streams (WP-126; every dialect)

**Status:** M5 (WP-126), `full` and `gpl` images with `XI_VIDEO_TRANSFORM_ENABLED=true`. Design: [ADR 0012 §6](adr/0012-video-transformations.md). A video URL that asks for a stream (imgproxy `/video` with `@m3u8`/`@mpd` or `st`, query `/q/video?format=m3u8`, Cloudinary `.m3u8`/`.mpd`/`f_m3u8` with `sp_`, ImageKit `ik-master.m3u8`/`.mpd` with `sr-`) answers an HLS master playlist (`application/vnd.apple.mpegurl`) or a DASH MPD (`application/dash+xml`) instead of a file. It is a video transformation for signing (signed only by default), roles, limits and the unsigned policy.

```
/video/<sig>/st:hls:360:720/plain/https://example.com/talk.mp4@m3u8       (imgproxy)
/video/<sig>/vs:60/vd:300/plain/s3://media/talk.mov@mpd                   (a 5-minute DASH stream of a cut)
/<cloud>/video/upload/s--SIG--/sp_hd/talk.m3u8                            (Cloudinary)
/imagekit/<mount>/talk.mp4/ik-master.m3u8?tr=sr-240_360_720&ik-s=…       (ImageKit)
```

- **Renditions** are rungs of the ABR ladder by the short side of the frame (`720` is 1280×720 for 16:9 landscape, 720×1280 for portrait): the URL's (`st:hls:360:720`, `sr-240_360_720`, a Cloudinary profile), else `XI_VIDEO_TRANSFORM_ABR_LADDER`, at the ladder's bit rates (or the standard rate of a height the ladder lacks). Rungs larger than the source are dropped; a source smaller than every rung gets one rendition of its own size. Every rendition has the stream's frame rate (the source's, `fps`, capped by `XI_VIDEO_TRANSFORM_MAX_FPS`). Everything else of the URL (cut, crop, rotation, flips, quality, codec, keyframe interval, fades, audio options) applies to every rendition; sizes, video bit rates and overlays do not (Cloudinary and ImageKit record them as not applied, 400 in strict mode; imgproxy and query answer 400).
- **Codecs:** CMAF (fragmented MP4) segments: H.264 High in `gpl`, AV1 in `full` (`vc` may pick `av1` or `vp9`; HEVC is not streamed), AAC-LC 48 kHz audio (muted streams have none). The manifests' `CODECS`/`codecs` strings (`avc1.64001f`, `av01.0.05M.08`, `vp09.00.31.08`, `mp4a.40.2`) are known before encoding and match the encoders' init segments (tested).
- **Segments** are `XI_VIDEO_TRANSFORM_SEGMENT_DURATION` long (whole seconds; `sgd` per URL), the last one shorter; each video segment is transcoded when a player asks for it, starting with a keyframe (one per segment unless `ki` asks for more), and the next `XI_VIDEO_TRANSFORM_PREFETCH_SEGMENTS` are transcoded ahead on idle video workers. The audio track is transcoded once and cut into segments on the same grid.
- **Child URLs:** the manifest lists its media playlists, init segments and media segments under `<XI_SERVER_PATH_PREFIX>/_stream/<ticket>/`: `master.m3u8`, `manifest.mpd`, `v<i>.m3u8` and `a.m3u8` (media playlists), `v<i>/init.mp4`, `a/init.mp4`, `v<i>/<n>.m4s`, `a/<n>.m4s` (`v0` is the largest rendition). The ticket seals the stream's source and options with an expiry (`XI_VIDEO_TRANSFORM_STREAM_TTL`, capped by the URL's own `expires`) under `XI_VIDEO_TRANSFORM_STREAM_KEYS` (AES-256-GCM): a player needs no signing key, the source stays hidden (an `enc/` source too), and an altered ticket is 403, an expired one 404 (`url expired`), a rendition or segment the stream does not have 404. A ticket only opens its own stream's parts.
- **Responses:** playlists and segments support `Range`, `If-None-Match` and `HEAD`; they may be cached until their ticket expires (manifests a quarter of the TTL). Parts are cached in memory (`XI_VIDEO_TRANSFORM_STREAM_CACHE_BYTES`) and in the result cache (a persistent tier keeps them across instances and restarts). CORS for browser players comes from `XI_SERVER_CORS_ORIGINS`.
- **Not supported:** live streams, DRM, MPEG-TS segments, subtitles, I-frame playlists, byte-range (single-file) playlists, Cloudflare Stream's `/manifest/video.m3u8` URLs (another product: stored videos with an upload API).

## 4. Query dialect (native + imaginary)

**Status:** on `main` (WP-23, M1; `pipeline` M2). imaginary's `/watermark` (text) and `/watermarkimage` are on `main` (WP-77/78, M3).

Reference: https://github.com/h2non/imaginary (API section). Mount `/q/`.

Two shapes:

```
/q/?url=<source>&w=300&h=200&fit=cover&g=smart&f=webp&q=80&dpr=2&sign=…
/q/resize?url=<source>&width=300&height=200&type=webp          (imaginary endpoint)
```

Video sources (WP-74): `vts`/`video_thumbnail_second` and `vtk`/`video_thumbnail_keyframes` are accepted as query keys like every imgproxy option (`/q/?url=<video>&w=320&vts=1.5`).

Video transformations (WP-122, WP-123): `/q/video?url=<source>&start=1&duration=5&width=640&format=webm&mute=1`. Every key is the [§3.8](#38-video-transformations-wp-122-wp-123) option of the same name or alias, with `,` or `:` between arguments (`ln=1,-16`, `fade=1,2`, `waveform=1,2266cc`); `fit` (`fit|contain|inside`, `fill|cover`, `fill-down`, `force|stretch`), `gravity|g` (compass words), `crop` (`w,h` placed by `g`, or `w,h,x,y` from the top-left), `flip` (`h|v|hv`), `cb` and `attachment` take their native spellings, and `o=` applies a `/video` option path. Signing: `xi_sig` covers `/video` + the query (or `sign`, imaginary's scheme, over the decoded path `/video` + the sorted query), so an image signature is not valid here; unsigned requests follow `XI_VIDEO_TRANSFORM_UNSIGNED_POLICY`. `pdf_password` is not a video option.

Native keys (long and short): `url`, `width|w`, `height|h`, `fit` (`fit|contain|inside` → Fit, `fill|cover` → Fill, `fill-down`, `force|stretch` → Force, `outside`), `gravity|g` (compass words, `smart`, `entropy`, `fp:x:y`), `crop` (`w,h[,x,y]`), `dpr`, `enlarge`, `format|f`, `quality|q`, `lossless`, `progressive`, `blur`, `sharpen`, `pixelate`, `rotate|rot`, `flip` (`h|v|hv`), `bg`, `strip`, `trim`, `pad`, `preset`, `page`, `pages`, `cb`, `filename`, `attachment`, `expires`, `pdf_password|pdp` (signed URLs only, [§3.7](#37-password-protected-pdfs-wp-115)), `sign`. Semantics are those of the equivalent imgproxy option; anything imgproxy can do is reachable via `o=<imgproxy option path>` (for example `o=rs:fill:300:200/g:sm`) so the query dialect is complete by construction.

imaginary endpoints: `resize`, `enlarge`, `extract`, `crop`, `smartcrop`, `rotate`, `autorotate`, `flip`, `flop`, `thumbnail`, `zoom`, `convert`, `fit`, `blur`, `watermark` (text, WP-78), `watermarkimage` (WP-77), `info`, `pipeline` (JSON `operations` → chained pipelines, M2). imaginary params: `width`, `height`, `areawidth`, `areaheight`, `quality`, `compression`, `type`, `force`, `rotate`, `embed`, `norotation`, `noprofile`, `stripmeta`, `flip`, `flop`, `extend`, `background`, `gravity`, `top`, `left`, `factor`, `dpi`, `interlace`, `aspectratio`, `palette`, `sigma`, `minampl`, `url`. Watermark params: `text` (Pango markup, as bimg passes it to `vips_text`), `font` (default `sans 10`), `dpi` (default 150: the font size is scaled by dpi/72, xi renders at 72 DPI per CSS pixel), `textwidth` (wrap width; bimg's default of a sixth of the image width is approximated by the image width), `opacity` (0 = 0.25 for text, 1 for images), `margin` (tile spacing, default `textwidth`), `color` (text colour), `noreplicate` (one watermark at the top-left instead of tiles), `image` (watermark image URL, placed at `left`/`top`). Also accepted: `nocrop`, `colorspace`, `operations` (JSON, `pipeline` endpoint). Unsupported: POST/multipart uploads (form field `file`), the `file` query param for local paths (use an `fs` source with `xi://` instead), `speed`. Params verified against `.refs/imaginary/params.go` and `server.go`.

Implementation notes (WP-23, `internal/dialect/query`): native keys are applied in query order through the imgproxy option parser, so a later key overrides an earlier one and keys after an `o=` that chains pipelines (`-`) apply to the last pipeline; `o=` must contain options only (a source in it is a 400). Multi-argument native values (`crop`, `pad`, `trim`, `bg` as `r,g,b`, `preset`, `g=fp:x:y`) accept `,` or `:` as separator, and `crop=w,h,x,y` places the top-left corner. Signing: `sign` covers the decoded path after the mount (`/resize`, or `/` for native mode) plus the sorted query; `xi_sig` covers the escaped path after the mount plus the query; both present is a 403. imaginary params are stricter than upstream (malformed or negative values are a 400 instead of silently falling back); `extend=mirror` is approximated as `copy`, `/extract` crops before resizing, `/zoom` scales by `factor+1` like bimg, `/info` uses the imgproxy info schema, and text/image watermarks are applied (WP-77/78).

## 5. imgix

**Status:** served on `main`: parser and `s`/`expires` signing (WP-40, M2), the mount and `XI_DIALECTS_IMGIX_KEYS` (WP-88, M3), and `mark*`/`txt*` watermarks (WP-77/78, M3).

Reference: https://docs.imgix.com/apis/rendering; unpic transformer `ascorbic/unpic` `src/providers/imgix.ts` (defaults `fit=min`, `auto=format`).

```
https://images.example.com/photos/a.jpg?w=400&h=300&fit=crop&crop=faces,entropy&auto=format,compress&q=60&dpr=2&s=…
/imgix/<mount>/photos/a.jpg?w=400                     (prefix mode)
/imgix/<mount>/https%3A%2F%2Forigin.com%2Fa.jpg?w=400  (web-proxy source, must be signed upstream; here: allowlist + policy)
```

Grammar: path = object key relative to mount source (escaped, `SourceRelative`, `Source.Mount` = the mount's source); a path whose decoded form starts with `http:`/`https:` is a web proxy source (plain absolute URL, subject to the allowlist and the unsigned policy). Query params are applied by name, the last occurrence wins; any param may be base64url-encoded (padding optional) by appending `64` (`txt64`, `mark64`, `bg64`, …). Every alias of imgix's parameter list (imgix-url-params 11.36 `parameters.json`: legacy `markalign`, `txtclr`, `trimcolor`, … and short `f`, `m`, `t`, `ma`, `tsz`, `inv`, `mono`, `or`, `width`, …) is accepted. `ixlib`/`ixid`/`ixjsv` (SDK and analytics tags) are accepted silently, also in strict mode. Unknown params, the "Unsupported" rows below and the not implemented params (`loop`, `reverse`, `fps`, `skip`, `interval`, `transparency`, `grid-colors`, `grid-size`) are recorded in `Ignored` (strict → 400); unsupported *values* of known params (`auto=enhance|redeye|true`, `fill=gen|generative|gradient`, `cs=adobergb1998`, `trim=alpha`, image masks) are recorded in `Ignored` in both modes. Parser in `internal/dialect/imgix` (WP-40).

Mount (WP-88): `XI_DIALECTS_IMGIX_ENABLED=true` mounts it under `XI_DIALECTS_IMGIX_PREFIX` (default `/imgix/`) and on `XI_DIALECTS_IMGIX_HOSTS` (with an empty prefix, the unpic setup), with `XI_DIALECTS_IMGIX_SOURCE` resolving object keys; extra mounts (another imgix "source") are `dialects.imgix.mounts[]` in YAML, each with its own prefix, hosts, source, tokens and unsigned policy (dialect key `imgix[i]`). `XI_DIALECTS_IMGIX_KEYS` are the secure URL tokens: with tokens `s` is verified (any token matches; `XI_SIGNING_REQUIRED` makes it mandatory) and a verified URL bypasses the unsigned policy; without tokens `s` is not checked and every URL follows the mount's unsigned policy (`restricted` by default: unpic's `w` from the allowlist, `auto=format,compress`, `fit`, but no effects, watermarks or info). `XI_DIALECTS_DEFAULT=imgix` serves unmatched paths with the primary mount's source and policy. The playground signs imgix URLs (scheme `imgix`) with the mount's first token and builds them from its imgix catalogue; OpenAPI documents the parameters on `getImgixImage`.

| imgix | TR | Notes |
|---|---|---|
| `w`, `h` | Resize.Width/Height (value in (0,1) → relative; fractions of a pixel rounded) | M1-level semantics in M2 |
| `max-w`, `max-h`, `min-w`, `min-h` | Resize.MaxWidth/MaxHeight/MinWidth/MinHeight | |
| `fit=clip` (default once `w` or `h` is set) | Fit + Enlarge | imgix clip upscales; `fit=max` does not |
| `fit=max` / `fit=min` | Fit, no enlarge / FillDown (gravity from `crop`) | `min` = crop without upscaling |
| `fit=crop` | Fill + Enlarge; gravity from `crop` | |
| `fit=smart-crop` | as `fit=crop` with `crop=auto` (Smart) unless `crop` is set | |
| `fit=scale` | Force + Enlarge | |
| `fit=fill`, `fill=solid|blur|gen|generative|gradient`, `fill-color` | Fit + Enlarge + Extend; `fill-color` → Background (wins over `bg`); `fill=blur` → Extend.Mode Blur (M3); `fill=gen|generative|gradient` unsupported (solid), `fill-gradient-*` unsupported | `fill`/`fill-color` only with `fit=fill|fillmax` |
| `fit=fillmax` | as `fill` without Enlarge | |
| `fit=facearea`, `facepad`, `faceindex` | Fill + Enlarge, Gravity Face with FacePad (default 1), FaceIndex (1-based → 0-based, best score first): the source is cut to the faces' box × facepad (grown to the output's aspect ratio) and filled into w×h (WP-72) | no face (or `faceindex` past the faces) → smart crop; slim → 422 |
| `fit=clamp` | Fit + Enlarge + Extend with edge replication (Extend.Mode Copy, vips `embed extend=copy`) | |
| `crop=top,bottom,left,right` | compass Gravity | combinations → corner |
| `crop` fallback chain | the first detector listed wins (imgix falls back left to right); compass values only apply without a detector | |
| `crop=faces` | Face gravity: the crop centres on the faces' union box (WP-72); no face → Smart | slim → 422 |
| `crop=entropy` / `edges` / `auto` | Entropy / Smart (attention) / Smart | |
| `crop=<object class>` (COCO class or supercategory, e.g. `car,vehicle`) | Gravity Object with Classes (WP-72); none found → Smart | a class the COCO model does not know → 400 (`transform.ResolveClass`); slim → 422 |
| `crop=focalpoint`, `fp-x`, `fp-y`, `fp-z` | FocusPoint (fractions, default 0.5, 0.5; not WP-86a pixel focus) + FocusZoom (1–100) | `fp-*` only with `crop=focalpoint`; `fp-z` > 1 not implemented (no planner zoom) |
| `ar=16:9` (also `1.5:1`, `2`) | Resize.AspectRatio | only with `fit=crop|smart-crop`, else ignored as in imgix |
| `rect=x,y,w,h` | Crop (pre-resize, px or relative (0,1)), anchored north-west with offsets x, y | |
| `dpr` | Resize.DPR (0 < dpr ≤ 10) | |
| `q` | Quality (`q=0` → 1) | |
| `fm=jpg|pjpg|png|png8|png32|webp|avif|jxl|gif|json|blurhash|mp4|webm` | Format; `pjpg` → Progressive; `png8` → PNG quantize 256; `json` → Info; `blurhash` → Info blurhash 4:3 (JSON, not imgix's bare string: approximated; WP-63); `mp4`/`webm` → video output of an animated source (imgix's GIF-to-video; FORMATS §5.1: VP9 WebM, MP4 with H.264 in the gpl image or AV1 in full; WP-76) | `jp2`, `jxr`, `pdf`, `spritesheet` unsupported → 422 |
| `jpg-progressive` | Progressive (wins over `fm=pjpg`) | |
| `iptc=allow|block` | not implemented (xi strips or keeps metadata as a whole) | |
| `auto=format` | negotiate (Accept); wins over `fm` except `json`, `blurhash`, `mp4`, `webm` (imgix uses `fm` only as the fallback; xi records `fm` as ignored) | |
| `auto=compress` | Quality 45 when `q` absent; StripMetadata; PreferJPEGIfOpaque when neither `fm` nor `auto=format` decides the format (imgix: PNG if transparent, else JPEG) | |
| `auto=enhance`, `auto=redeye`, `auto=true` | Unsupported | recorded in `Ignored` even in strict mode |
| `lossless` | Lossless | |
| `chromasub=420|422|444` | ChromaSubsampling | |
| `cs=srgb|tinysrgb|strip|origin|adobergb1998` | ColorProfile `srgb` / StripColorProfile true / false; `adobergb1998` not implemented | |
| `dpi` | DPI (output resolution, applied) | M2 (WP-64) |
| `bg` (RGB, ARGB, RRGGBB, AARRGGBB (alpha first), `#` optional, CSS named, `transparent`) | Background | |
| `pad`, `pad-top/right/bottom/left` | Padding | |
| `border=w,color`, `border-top/right/bottom/left`, `border-radius` (`r` or `tl,tr,br,bl`), `border-radius-inner` | Border, Radius; `border-radius-inner` not implemented | M2 |
| `blur` (0–2000) | Blur sigma = v/20 | |
| `px` | Pixelate | |
| `sharp` (0–100) | Sharpen sigma = 0.5 + v/50 | |
| `usm` (−100..100), `usmrad` (0–500, default 2.5) | Unsharp{Amount: usm/100, Sigma: usmrad}: classic unsharp mask v + amount × (v − blur) | M2 (WP-61) |
| `bri`, `con`, `exp`, `gam`, `sat`, `hue`, `vib` | Adjust: bri → Brightness v·255/100; con, exp, sat, vib → 1 + v/100; gam → 2^(v/100); hue → degrees; 0 = unchanged | `vib` is recorded "not implemented" (no planner step, `plan.Unplanned`) |
| `high`, `shad` | Unsupported | |
| `sepia`, `monochrome`, `duotone`, `duotone-alpha`, `invert` | Sepia (any amount > 0; partial amounts approximated) / Monochrome (colour alpha = intensity) / Duotone (intensity `duotone-alpha`/100) / Negate | |
| `bri`, `con`, `exp`, `gam`, `sat`, `hue`, `vib` | Adjust: bri → Brightness v·255/100; con, exp, sat, vib → 1 + v/100; gam → 2^(v/100); hue → degrees; 0 = unchanged | M2; `vib` not implemented (no planner step) |
| `high`, `shad` | Unsupported | |
| `sepia`, `monochrome`, `duotone`, `duotone-alpha`, `invert` | Sepia (any amount > 0; partial amounts approximated) / Monochrome (colour alpha = intensity) / Duotone (intensity `duotone-alpha`/100) / Negate | M2 |
| `face-blur`, `face-pixel` (0–100) | BlurDetections on class `face`: Sigma = v/5 / Pixelate = v px blocks inside each face box (`face-pixel` wins) (WP-73) | slim → 422 |
| `faces=1` (with `fm=json`) | Info DetectObjects on class `face`: `"objects"` with the faces' boxes and landmarks (imgproxy's shape, not imgix's `Faces` JSON) (WP-73) | without `fm=json`: `Ignored` |
| `orient` (1–8, 90, 180, 270) | AutoRotate off + the Rotate/Flip of that EXIF orientation; 90/180/270 = EXIF 6/3/8 | overrides the EXIF orientation |
| `rot` (0–359, counter-clockwise), `rot-type=pivot|straighten` | RotateAny = −rot (multiples of 90 fold into Rotate); `rot-type` approximated | imgix `straighten` (default) zooms to hide the corners; xi fills them with Background for both types. M2 |
| `flip=h|v|hv` | Flip (combined with `orient`) | |
| `trim=auto|color|colorunlessalpha|alpha`, `trim-color`, `trim-tol`, `trim-md`, `trim-sd`, `trim-pad`, `trim-alpha` | Trim: `auto` → Threshold `trim-md` (default 11), `trim-sd` approximated; `color` → Color `trim-color` (default corner pixel), Threshold `trim-tol` (default 0); `colorunlessalpha` → as `color` (approximated: imgix trims transparent images by alpha); `alpha`/`trim-alpha` and `trim-pad` not implemented | |
| `mask=ellipse|corners`, `mask-bg`, `corner-radius` | Mask ellipse (+ Background) / Radius; image masks (a URL) unsupported | M2 |
| `mark`, `mark-alpha`, `mark-align` (default bottom,right), `mark-pad` (default 5), `mark-scale`, `mark-w`, `mark-h`, `mark-x`, `mark-y`, `mark-tile=grid`, `mark-rot`, `mark-fit`, `mark-base` | Watermark Source: an http(s) URL, a path on the mount's source, or `mark-base` + path; Opacity = alpha/100; `mark-align`/`mark-pad` → compass + offsets; `mark-x`/`mark-y` → north-west offsets; Scale = `mark-scale`/100 (fit into that fraction of the image, both sides); `mark-w`/`mark-h` px → Width/Height (fractions approximated); `mark-tile=grid` → repeat; `mark-rot` → Rotate = −rot (counter-clockwise); `mark-fit` other than `clip`/`max` approximated; `mark-if-min-width`/`-height` not implemented (the watermark is always drawn). Parameters without `mark` are recorded ("only with its base parameter") | M3 (WP-77) |
| `txt`, `txt-font`, `txt-size` (default 12), `txt-color`, `txt-align`, `txt-pad` (default 10), `txt-x`, `txt-y`, `txt-shad`, `txt-width`, `txt-fit`, `txt-clip`, `txt-lead`, `txt-track`, `txt-line`, `txt-line-color` | Text watermark: `txt` is plain text (escaped for Pango); Font = `txt-font` (commas become spaces: `Avenir Next,Bold` → `Avenir Next Bold`) + size; Color; `txt-x`/`txt-y` → north-west offsets (like `mark-x`/`mark-y`); Shadow = `txt-shad` sigma; TextWidth = `txt-width` (wrap width). `txt-fit` approximated (xi wraps, it does not shrink to `txt-width`); `txt-clip`, `txt-lead`, `txt-track`, `txt-line`, `txt-line-color` unsupported | M3 (WP-78) |
| `blend*` | Unsupported (only `blend-mode=normal` via watermark) | |
| `page`, `frame` (1-based) | Page; `frame` also Pages = 1 (one frame of an animation) | M2 (WP-62) |
| `dl` | Filename + Attachment | |
| `ch=Width,DPR,Save-Data` | enable client hints for this URL | |
| `expires` | Expires (unix seconds; 0 = never); checked after the signature, 404 once passed | |
| `s` | signature: hex(MD5(token ‖ path ‖ "?" ‖ query-without-s)), path = the escaped path after the mount prefix; `s` may appear anywhere, the message is built without it | tokens per mount (`XI_DIALECTS_IMGIX_KEYS`) |
| AI/ML: `bg-remove*`, `bg-replace*`, `object-removal-*`, `upscale`, `nr`, `nrs`, `fill=gen`, `fill-gen-*`, `lp-blur`, `palette`, `colorquant`, `fp-debug`, `htn` halftone | Unsupported | |
| `video-*`, `border-stroke-*`, `svg-sanitize`, `rasterize-bypass`, `pdf-annotation` | Unsupported (xi always sanitizes SVG and never bypasses processing through a URL parameter) | |
| `s` | signature: hex(MD5(token ‖ path ‖ "?" ‖ query-without-s)); `s` may appear anywhere, the message is built without it | |
| AI/ML: `bg-remove`, `bg-replace`, `upscale`, `nr`, `nrs`, `fill=gen`, `palette`, `colorquant`, `faces` debug, `fp-debug`, `htn` halftone | Unsupported | |
| (video sources) | imgix has no frame parameter: a video source (full image, FORMATS §1.5) is thumbnailed with `XI_VIDEO_THUMBNAIL_*` (best frame by default) | WP-74 |

## 6. Cloudinary

**Status:** served on `main` (WP-41, M2; `internal/dialect/cloudinary`, `s--…--` signing). `l_` image and `l_text` overlays (WP-77/78) and face/object gravity and `e_blur_faces` (WP-72/73, `full` image) are on `main` (M3).

Reference: https://cloudinary.com/documentation/transformation_reference, https://cloudinary.com/documentation/delivery_url_signatures; unpic `cloudinary.ts` (defaults `f_auto,c_lfill`).

```
/<cloud>/image/upload/c_fill,g_auto,w_400,h_300/q_auto/f_auto/v1712345678/folder/sample.jpg
/<cloud>/image/upload/s--iDy_JeBq--/c_fill,w_300,h_250/e_grayscale/sample.png
/<cloud>/image/fetch/w_400,f_auto/https://example.com/a.jpg
```

Grammar: `/<cloud>/<asset_type>/<delivery_type>/[s--SIG--/][<tx>/]*[v<digits>/]<public_id>[.<ext>]`. When host-bound as a private CDN or custom domain (`<cloud>-res.cloudinary.com`-style, unpic `privateRegex`), the `<cloud>/` segment is absent and the source comes from the mount: the first segment is read as the asset type when it is `image`/`video`/`raw` and the second is a delivery type, otherwise it is the cloud name. `asset_type` = `image`, or `video`: a video transformation (§6.1, WP-125) unless the output is an image format (`f_jpg`, or an image extension on the public ID), which is a thumbnail of the video (WP-74; `so_` picks the frame); `raw` is 400. `delivery_type` = `upload` (public ID = key under the cloud's source, extension included, as written) or `fetch` (absolute http(s) URL, percent-decoded, a proxy-collapsed `https:/` repaired; the source allowlist applies); `private`/`authenticated` are `upload` that require a verified signature (403 `signature required` otherwise, also when signing is off); other delivery types are 400. `<cloud>` maps to a named source via `dialects.cloudinary.clouds`; with a non-empty map an unmapped cloud is 403, with an empty map the mount's source applies to every cloud. Public IDs, layer IDs and `d_` IDs whose first segment contains `:` (`https:`, `s3:`) are 400, so an `upload` URL can never name an absolute source. The query string (SDK `_a=` analytics) is ignored.

Components: a path segment is a transformation component when every comma-separated part is `key_value` with a lower-case key (or `$variable`) and at least one key is a Cloudinary parameter; the first segment that is not, or a `v<digits>` segment, starts the public ID (the SDKs add `v1` to foldered IDs for exactly this ambiguity). Components apply in URL order. A component with pipeline parameters (`w h c g x y ar z a bo e o r t`) starts a chained pipeline once the current one holds anything; components with only `f_`, `q_`, `fl_`, `dpr_`, `b_`, `cs_`, `d_`, `pg_` modify the current pipeline / the request, so `c_fill,w_400/q_auto/f_auto` is one pipeline and `c_fill,w_400/e_blur` two. The extension sets `O.Format` unless `f_` is given. In `XI_DIALECTS_STRICT` mode unknown and unsupported parameters are 400.

Signature: `s--SIG--` right after the delivery type; the signed message is the rest of the path with the version segment removed (`<tx>/…/<public_id>.<ext>`, or `<tx>/…/<escaped fetch URL>`), exactly as the SDKs build `to_sign` (cloudinary-npm `utils.url`). 8 characters = SHA-1 (SHA-256 for accounts whose signature algorithm is sha256, `sign.CloudinaryOptions.ShortSHA256`), 32 characters = SHA-256 (`long_url_signature`); both are accepted, `LongOnly` rejects the 48-bit short form. Keys are the api secrets of the mount (`dialects.cloudinary.keys`); with no keys the segment is skipped unchecked. Unsigned requests fall to the router's unsigned policy.

| Cloudinary | TR | Notes |
|---|---|---|
| `w_`, `h_` (int px; a decimal is relative: `w_0.5`, `w_1.0`; `w_auto[:step[:fallback]]` client hint) | Width/Height; `w_auto` → `ClientHints.Width` (`WidthStep` = step; the fallback is the Width until a hint resolves it) | expressions (`iw_div_2`), `$vars` unsupported |
| `c_scale` (default when `w_`/`h_`/`ar_` without `c_`) | Force if both dims or `ar_`, else Fit + Enlarge | after a `t_` preset that set a mode, the preset's mode is kept |
| `c_fit` / `c_limit` / `c_mfit` | Fit+Enlarge / Fit / Outside+Enlarge | |
| `c_fill` / `c_lfill` | Fill+Enlarge / FillDown | |
| `c_fill_pad` | Fill + gravity + Extend | approximated |
| `c_pad` / `c_lpad` / `c_mpad` | Fit+Enlarge+Extend / Fit+Extend / Fit+Extend | `c_mpad` approximated (never scales upstream); extend anchor = compass `g_` else centre |
| `c_crop` | Crop (w, h, `ar_`), no scaling; `x_`/`y_` without `g_` = top-left corner (NorthWest + offsets); `g_xy_center` = FocusPoint (relative `x_`/`y_`, or integer source pixels → `FocusPx`, WP-86) | a relative axis mixed with an absolute one is approximated (centre) |
| `c_thumb` | Fill + gravity; with face gravity the faces' box × 1.5/`z_` (default z 1) is cut first (FacePad), with object gravity the objects' box (CropObjects), so the thumbnail frames them (WP-72); other gravities keep `z_` as FocusZoom (not applied) | the 1.5 margin approximates Cloudinary's head-and-shoulders thumb |
| `c_auto`, `c_auto_pad` | Fill + Smart / Fit + Enlarge + Extend | |
| `c_imagga_*` | recorded unsupported, applied as `c_scale` | |
| `g_` compass (`north_east`…), `center` | Gravity | |
| `g_auto`, `g_auto:subject`, `g_auto:classic`, `g_auto:no_faces`, `g_auto:<x>_avoid` | Smart (faces first, `XI_DETECT_SMART_FACES`) | |
| `g_auto:face(s)`, `g_auto:<object>` (`_<weight>` suffix dropped) | Face / Object gravity: priority to the detections, Smart when none (WP-72) | an object the COCO model does not know → Smart |
| `g_face`, `g_adv_face`, `g_adv_eyes` / `g_faces`, `g_adv_faces` (`:…` modifiers dropped) | Face gravity on the best-scoring face (FaceIndex 0; Cloudinary: the largest) / on every face (WP-72); no face → Smart | slim → 422 |
| `g_<object>` (e.g. `g_dog`, `g_traffic_light`, `g_vehicle`) | Object gravity with the COCO class or supercategory (WP-72); none found → Smart | a category the COCO model does not know → Smart, `Ignored` "unsupported object"; slim → 422 |
| `g_xy_center` + `x_`,`y_` | FocusPoint: relative `x_`/`y_` (0..1) → `FocusX/FocusY`; absolute (integer pixels on both axes) → `FocusPx` via `transform.FocusFromPixels(x, y, x, y)` (WP-86a); a relative axis mixed with an absolute one is approximated to the centre | |
| `g_custom`, `g_body`, `g_ocr_text`, `g_liquid`, … | unsupported (centre) | |
| `ar_16:9` / `ar_1.5` | Resize.AspectRatio (Crop.AspectRatio with `c_crop`) | |
| `x_`, `y_` | gravity offsets | |
| `z_` | FocusZoom (`c_thumb`, `c_crop`) | other modes approximated |
| `q_<1-100>[:420\|422\|444]`, `q_auto[:best\|good\|eco\|low]` | Quality (+ChromaSubsampling) / AutoQuality DSSIM targets 0.005 / 0.02 / 0.035 / 0.05 (`q_auto` = good) | `q_jpegmini` unsupported |
| `f_auto[:…]`, `f_<fmt>` (jpg/jpeg/jpe, png, webp, avif, jxl, gif, heic/heif, tif/tiff, bmp, ico, svg, mp4, webm) | Format unset (negotiated) / Format | `mp4`/`webm` (and the `.mp4`/`.webm` extension) on the `image` asset type encode an animated source as video (FORMATS §5.1, WP-76; `fl_animated` is the default); other formats (pdf, jp2, …) 400 |
| `dpr_<float>`, `dpr_auto` | DPR of the current pipeline / `ClientHints.DPR` | the hint resolves into the first pipeline (§3.5) |
| `a_<deg>` (`.`-joined list), `a_hflip`, `a_vflip`, `a_exif`, `a_ignore` | Rotate (multiples of 90) / RotateAny / Flip / AutoRotate | `a_auto_right`, `a_auto_left` unsupported |
| `b_<color>` (`rgb:` 3/4/6/8 hex digits, `%23` hex, CSS names, `transparent`) | Background | `b_auto`, `b_blurred`, `b_gen_fill` unsupported |
| `bo_<n>px_solid_<color>` | Border | |
| `r_<n>[:<n>…]` (1–4 values, Cloudinary corner order), `r_max` | Radius | |
| `o_<0-100>` | Adjust.Opacity | |
| `e_blur[:1-2000]` | Blur σ = n/20 (default 100 → 5, like imgix `blur`) | |
| `e_sharpen[:1-2000]`, `e_unsharp_mask[:1-2000]` | Sharpen σ = n/100 / Unsharp σ 1, amount n/100 | |
| `e_pixelate[:1-200]` | Pixelate (default 10) | |
| `e_grayscale`, `e_blackwhite` (approximated), `e_sepia`, `e_negate` | corresponding Pipeline fields | |
| `e_brightness[:-99..100]` (default 80), `e_hue[:-100..100]` (80) | Brightness n × 2.55 / Hue n × 1.8° | |
| `e_contrast`, `e_saturation`, `e_gamma`, `e_vibrance`, `e_red`/`e_green`/`e_blue` | multiplier 1 + n/100 | `e_vibrance` is recorded "not implemented" (no planner step, `plan.Unplanned`) |
| `e_colorize[:0-100]` + `co_<color>` | Colorize (opacity n/100, default grey) | |
| `e_trim[:tolerance[:color]]` | Trim (threshold = tolerance, default 10) | |
| `e_blur_faces[:n]`, `e_pixelate_faces[:n]` | BlurDetections(face) σ n/20 (default 500 → 25) / pixelate n px blocks (default 10) inside each face box (WP-73) | slim → 422 |
| `e_blur_region[:n]` + relative `x_`,`y_`,`w_`,`h_` | BlurAreas (no region: whole-image blur) | pixel regions approximated |
| other `e_*` (AI, artistic, `background_removal`, `gen_*`, `upscale`, `vectorize`, …) | Unsupported | |
| `fl_lossy`, `fl_progressive[:none]`, `fl_strip_profile`, `fl_keep_iptc`, `fl_keep_attribution`, `fl_force_strip`, `fl_attachment[:name]`, `fl_immutable_cache`, `fl_png8`, `fl_getinfo` | Lossless=false, Progressive, StripColorProfile, StripMetadata=false, KeepCopyright, StripMetadata, Attachment/Filename, Immutable, PNG quantize 256, Kind=Info (size, format, dimensions) | `fl_animated`, `fl_awebp`, `fl_apng`, `fl_preserve_transparency`, `fl_any_format` are the default behaviour; `fl_rasterize`, `fl_clip`, … unsupported |
| `cs_srgb`/`cs_no_cmyk`/`cs_tinysrgb`, `cs_cmyk` | ColorProfile srgb / cmyk | `cs_keep_cmyk`, `cs_icc:` unsupported |
| `pg_<n>` (1-based) | Page n−1 (WP-62) | Ranges and names unsupported |
| `t_<name>[.<name>…]` | presets expanded in place (imgproxy option syntax); only-presets mode allows nothing but `t_` | |
| `d_<public_id>` (`:` = folder) | Fallback (same cloud source) | honoured with `XI_FALLBACK_ALLOW_REQUEST_URL` |
| `l_<id>` (`:` = folder), `l_fetch:<base64url>`, `l_text:<family>_<size>[_<style>…]:<text>` with `g_`, `x_`, `y_`, `o_`, `a_`, `co_` (text), `w_`/`h_` px, relative `w_` + `fl_relative` (scale), `fl_tiled`; legacy single component or `l_…/…/fl_layer_apply,…` group | Watermark on the current pipeline: `l_<id>` is an image on the cloud's source (the base image's storage), `l_fetch:` a remote URL; `a_` → Rotate (clockwise); a text layer's `w_` is its wrap width (`c_fit`), its `h_` approximated. `l_text`: Font = family + weight/style tokens (`bold`, `italic`, …) + size, `underline`/`strikethrough` → markup, the text (double-escaped commas decoded) escaped for Pango; alignment, `letter_spacing_N`, `line_spacing_N`, `stroke`, antialias and hinting approximated | M3 (WP-77/78); `l_video`, `l_subtitles`, `l_lut`, `l_audio` unsupported; other parameters in a layer group (`co_` on an image layer, `e_`, `b_`, …) approximated |
| `u_` underlays, `if_` (skips its component, or the block up to `if_end`), `$var`, `fn_`, `dn_`, video/audio `vc_`/`ac_`/`du_`/`eo_`/`fps_`/… on the `image` asset type | Unsupported (on the `video` asset type: §6.1) | |
| `d_<public_id>` (`:` = folder) | Fallback (same cloud source) | M2 |
| `l_<id>` (`:` = folder), `l_fetch:<base64url>` with `g_`, `x_`, `y_`, `o_`, `a_`, `w_`/`h_` px, relative `w_` + `fl_relative` (scale), `fl_tiled`; legacy single component or `l_…/…/fl_layer_apply,…` group | Watermark (image / URL) on the current pipeline | M3; `l_text`, `l_video`, `l_subtitles`, `l_lut` unsupported; other parameters in a layer group approximated |
| `so_<seconds>`, `so_auto` | Video.Second (the frame of a video source, WP-74); `so_auto` keeps the configured selection (best frame by default) | percentages (`so_10p`) unsupported; the `video` asset type delivers a thumbnail when the output is an image format: `/video/upload/so_2,f_jpg/dog.mp4`, `/video/upload/so_2/dog.jpg`, `/video/fetch/so_2,f_jpg/<url>`; otherwise it transcodes (§6.1) |
| `u_` underlays, `if_` (skips its component, or the block up to `if_end`), `$var`, `fn_`, `dn_`, video/audio `vc_`/`ac_`/`du_`/`eo_`/`fps_`/… on the `image` asset type | Unsupported (on the `video` asset type: §6.1) | |

### 6.1 Video transformations (`/video/upload/`, WP-125)

**Status:** M5 (WP-125; `internal/dialect/cloudinary/video.go`). Needs `XI_VIDEO_TRANSFORM_ENABLED=true` (full and gpl images) and, by default, a signed URL (`XI_VIDEO_TRANSFORM_UNSIGNED_POLICY=deny`).

Reference: https://cloudinary.com/documentation/video_manipulation_and_delivery, https://cloudinary.com/documentation/video_transformation_reference.

```
/<cloud>/video/upload/so_6.5,eo_10/dog.mp4
/<cloud>/video/upload/s--SIG--/c_fill,g_north,w_640,h_360/q_auto,vc_h264,br_1m/dog.mp4
/<cloud>/video/upload/vc_none,so_2,du_5/dog.m4a            (audio only)
/<cloud>/video/fetch/w_480,f_auto/https://example.com/a.mp4
```

A `video` asset URL is a `KindVideo` request (`transform.VideoOp`) when its output is a video or audio format: the last `f_`, else the public ID's extension (for `fetch`, the URL path's), else MP4; an image output format keeps the thumbnail path (§6, `so_`). The public ID is the source key as written, extension included, like the image asset type. Components apply in URL order onto one VideoOp: trimming, codecs and audio accumulate (later values win); `l_` layer groups become overlays; geometry is at most one `c_crop` followed by one resize (a later resize replaces an earlier one, recorded "approximated"). Signatures (`s--…--`), `private`/`authenticated`, clouds and `fetch` work as for images. Parameters not applied are recorded in `Ignored` with their reason, and are 400 in strict mode (approximations excepted).

| Cloudinary | VideoOp | Notes |
|---|---|---|
| `so_<s>`, `eo_<s>`, `du_<s>` (seconds) | Start, End, Duration | `eo_` and `du_` together: `eo_` wins (`du_` approximated); `eo_` ≤ `so_` is 400; percentages (`so_10p`, `du_30%`) recorded "needs the source size or duration"; `so_auto` approximated (start 0) |
| `w_`, `h_` (px) with `c_scale` (default) | Width/Height; both → Fill (stretch), one → Contain | relative (`w_0.5`) and expressions recorded "needs the source size or duration" |
| `c_fit`, `c_limit`, `c_mfit` | Contain, ScaleDown, Contain (approximated) | |
| `c_fill`, `c_lfill`/`c_thumb`/`c_auto` | Cover (+ Gravity); the last three approximated | one side only → Contain |
| `c_pad` (+ `b_<colour>`), `c_lpad`/`c_mpad`/`c_fill_pad`/`c_auto_pad` | Pad + Background; the others approximated | `b_auto`, `b_blurred`, `b_gen_fill` unsupported; `b_` alone in a later component sets the pad colour |
| `c_crop` with `w_`,`h_` and `g_` and/or `x_`,`y_` | Crop (in the rotated frame, before the resize); `x_`/`y_` alone = from the top-left corner, with `g_` = offsets from that anchor, neither = centred | a crop after a resize, or a second crop, unsupported; one side only recorded "needs the source size" |
| `g_<compass>`, `g_center` | Gravity (cover crop, pad position) or the crop anchor | `g_auto`, `g_face(s)`, objects, `xy_center`: approximated (centre) |
| `ar_<w>:<h>` / `ar_<n>` | derives the missing side from `w_` or `h_` | alone: needs the source size |
| `dpr_<n>` | multiplies the sizes | `dpr_auto` approximated (1) |
| `a_<90k>`, `a_-90`, `a_hflip`, `a_vflip` | Rotate, FlipH, FlipV | other angles, `a_auto_right/left`, `a_exif`, `a_ignore` unsupported |
| `q_<1..100>`, `q_auto[:best\|good\|eco\|low]` | Quality (80/65/50/35 for the `q_auto` levels), mapped onto the codec's CRF scale | `q_70:qmax_80` approximated |
| `br_<n>[k\|m][:constant]` | MaxBitrate (bits/s) | `:constant` approximated (capped); outside 50 kb/s – 200 Mb/s is 400 |
| `fps_<n>`, `fps_<min>-<max>` | FPS (a range takes its maximum, approximated) | `fps_<min>-` approximated (source rate) |
| `ac_none`, `ac_aac`, `ac_mp3`, `ac_opus`, `ac_vorbis` | Mute, AudioCodec | `ac_vorbis` → Opus (approximated); a codec the container cannot hold is 400 |
| `vc_auto`, `vc_h264[:profile[:level]]`, `vc_h265`, `vc_vp9`, `vc_av1`, `vc_none` | VideoCodec; `vc_none` = audio only (M4A, Ogg Opus or MP3 by the audio codec) | profile/level approximated; `vc_vp8`, `vc_prores`, `vc_theora` unsupported; a codec the container cannot hold (`vc_vp9` in `.mp4`) is 400; H.264/HEVC encode only in gpl images (422 elsewhere) |
| `f_mp4`, `f_webm`, `f_m4a`, `f_mp3`, `f_opus`, `f_ogg` (and the same extensions) | Container | `f_ogg` → Ogg Opus audio (approximated); `f_mov`/`mkv`/`flv`/`avi`/… recorded unsupported (MP4 is delivered); `f_m3u8`/`f_mpd` (and the `.m3u8`/`.mpd` extensions) are streams (row below); `f_wav`/`flac`/`aac` "M5 (WP-123)"; unknown formats 400 |
| `f_auto` | AutoContainer: WebM when the client names `video/webm` in `Accept`, else MP4; `Vary: Accept` | an explicit codec decides the container instead (`vc_vp9`/`ac_opus` → WebM, `vc_h264`/`ac_aac` → MP4) |
| `e_volume:mute` / `e_volume:-100` | Mute | other volumes, `e_fade`, `e_noise_reduction`, `af_`, `fl_waveform`, `fl_mono`: "M5 (WP-123)" |
| `fl_attachment[:name]`, `fl_streaming_attachment`, `fl_immutable_cache` | Delivery | `fl_keep_dar`, `fl_truncate_ts`, `fl_no_stream`, `fl_animated` are the default; `fl_lossy` and other image flags "not applicable to video"; `fl_splice`, `fl_hlsv3` unsupported |
| `l_<public_id>` (`:` = folder), `l_fetch:<base64url>`, `l_text:<style>:<text>` and their `fl_layer_apply` group (or the legacy single component) | Overlays (WP-124, FORMATS §5.2): the layer and its placement as on images (§6: `g_`, `x_`, `y_`, `w_`/`h_` px, relative `w_` + `fl_relative`, `fl_tiled`, `o_`, `a_`, `co_`, text style); the group's `so_`/`eo_`/`du_` → Start/End on the output's timeline, `e_fade[:ms]` → FadeIn (positive, default 2000) / FadeOut (negative) | at most 8; an overlay on an audio-only output is 400; `eo_` with `du_`: `eo_` wins; `l_video`, `l_subtitles`, `l_lut`, `l_audio`, `fl_splice` and `u_` underlays unsupported |
| `.m3u8` / `.mpd` (or `f_m3u8` / `f_mpd`) with `sp_<profile>` | Stream (HLS / DASH, §3.10): `sp_auto` and no `sp_` = the configured ladder; `sp_4k` = 2160/1440/1080/720/480/360/240, `sp_full_hd` = 1080/720/480/360/240, `sp_hd` = 720/480/360/240, `sp_sd` = 480/360/240, `sp_full_hd_wifi` = 1080/720/480, `sp_full_hd_lean` = 1080/720/360, `sp_hd_lean` = 720/360/240 (heights, at this server's bit rates; never above the source) | custom (account) profiles recorded unsupported (the ladder is used); `sp_` with another output recorded "applies to m3u8 and mpd output only"; resizes, `br_`, layers and non-AAC audio recorded "not applied to streams" (400 in strict mode); `vc_none` is 400 |
| `e_<effect>` (accelerate, reverse, boomerang, loop, deshake, …), `t_`, `d_`, `o_`, `r_`, `bo_`, `co_`, `z_`, `if_`, `$var`, `ki_`, `vs_`, `dl_`, `fn_`, `p_` | — | unsupported |

Fixtures: `internal/dialect/cloudinary/testdata/urlgen/generate.ts` builds the corpus with `@cloudinary/url-gen` 1.22.0 and the `cloudinary` Node SDK 2.11.0 (including signed URLs, i.e. the signature vectors); unpic's `cloudinary.test.ts` URLs are asserted in `compat_test.go`.

## 7. ImageKit

**Status:** served on `main` (WP-42, M2). `l-image`/`l-text` overlays (WP-77/78), `XI_DIALECTS_IMAGEKIT_ENDPOINT_PATH` for host-bound `ik.imagekit.io/<id>/…` URLs (WP-87) and face/object focus (WP-72, `full` image) are on `main` (M3).

Reference: https://imagekit.io/docs/transformations, https://imagekit.io/docs/image-resize-and-crop, https://imagekit.io/docs/effects-and-enhancements, https://imagekit.io/docs/media-delivery-basic-security; SDKs `imagekit-javascript` `src/url.ts` (URL builder) and `imagekit-nodejs` `src/resources/helper.ts` (signing); unpic `imagekit.ts` (defaults `c-maintain_ratio,fo-auto`). Implemented in `internal/dialect/imagekit` (WP-42).

```
https://ik.example.com/tr:w-400,h-300,fo-auto/path/a.jpg
https://ik.example.com/path/tr:w-400/a.jpg
https://ik.example.com/path/a.jpg?tr=w-400,h-300:rt-90&ik-t=1767225600&ik-s=…
/imagekit/<mount>/tr:w-400/path/a.jpg
```

Grammar: the path after the mount is the file path, relative to the mount's bound source. When a host-bound mount keeps the ImageKit URL-endpoint id in the path (`https://ik.imagekit.io/<id>/…`), `XI_DIALECTS_IMAGEKIT_ENDPOINT_PATH` (`/<id>`, YAML `dialects.imagekit.endpoint_path`) is stripped first. Transformations: a `tr:` path segment anywhere before the file name (`:` may be written `%3A`; the segment is percent-decoded), or the `tr` query parameter (query-decoded, so unpic's `%2C` works). Both present: the path wins and the query is recorded in `TR.Ignored` (400 in strict mode); the SDKs never emit both and ImageKit documents no order. More than one `tr:` segment or `tr` parameter is 400. `:` separates chained steps (→ chained pipelines, max 16), `,` separates `key-value` parameters (split on the first `-`; `e-<effect>[-value]` and `l-<layer>` keep their two-part names). Empty steps and parameters are skipped. Request-scoped parameters (`q`, `f`, `pr`, `lo`, `md`, `cp`, `orig`) apply to the whole request, the last occurrence winning. Other query parameters (SDK `queryParameters`, e.g. `?v=2`) become `D.CacheBuster`; `ik-attachment=true` → `D.Attachment`. ImageKit arithmetic expressions (`w-iw_div_2`, `lx-bw_mul_0.5`) are recorded as ignored (strict: 400).

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
| `fo-face`, `fo-faces` | Face gravity (WP-72); no face → Smart; slim → 422 |
| `fo-<object>` (`car`, `traffic_light`, `truck_car_train`) | Object gravity: one class with `_` for spaces, or several joined by `_` (WP-72); none found → Smart; an object the COCO model does not know → Smart, `Ignored` "unsupported object" |
| `fo-custom` | Unsupported (per-file crop area stored in ImageKit) |
| `z` (with `fo-face` / `fo-<object>`) | Zoom on the detections: their box × 1/z is cut before the resize (FacePad / CropObjects, WP-72); other focus: `Ignored` |
| `q` (1–100) | Quality |
| `f-auto`, `f-orig` | Format unset (negotiated / source) |
| `f-jpg|jpeg|png|webp|avif|gif|jxl` | Format; `f-mp4|webm` video output of an animated source (FORMATS §5.1, WP-76); `f-svg` unsupported |
| `<gif>/ik-gif-video.mp4` | The animated image `<gif>` as MP4 (ImageKit's GIF-to-video; Format = mp4, WP-76) |
| `pr-true|false`, `lo-true|false` | Progressive, Lossless |
| `md-true|false` | StripMetadata = false / true (keep metadata) |
| `cp-true|false` | StripColorProfile = false / true (colour profile; not copyright) |
| `orig-true` | Kind raw |
| `dpr` (0.1–5), `dpr-auto` | Resize.DPR / ClientHints.DPR |
| `bl-<1..100>` (also unpic's `blur-`) | Blur σ = v/2 |
| `e-sharpen[-n]` | Sharpen σ = 1 (bare) or 0.5 + n/50 (approximation) |
| `e-usm-<radius>-<sigma>-<amount>-<threshold>` | Unsharp{Sigma, Amount} (WP-61, classic unsharp mask); radius, threshold dropped |
| `e-grayscale` | Grayscale |
| `e-contrast` (auto contrast stretch) | Equalize; recorded "not implemented" (no planner step, `plan.Unplanned`) |
| `r-<n>`, `r-<tl>_<tr>_<br>_<bl>`, `r-max` | Radius |
| `b-<w>_<color>` | Border |
| `bg-<RRGGBB|RRGGBBAA|RGB|css name>` | Background |
| `bg-blurred[_…]` | Extend.Mode = blur (M3); `bg-dominant`, `bg-gradient…`, `bg-genfill…` unsupported |
| `rt-<deg>`, `rt-N<deg>` | Rotate (multiples of 90), else RotateAny |
| `rt-auto` | AutoRotate |
| `fl-h|v|h_v` | Flip |
| `t-true|<1..99>` | Trim threshold 10 / n |
| `o-<0..100>` | Adjust.Opacity |
| `n-<name>` | preset `<name>` expanded in place (named transformation) |
| `di-<path>` (`@@` = `/`) | Fallback, relative to the mount source (served with `XI_FALLBACK_ALLOW_REQUEST_URL`, WP-34c) |
| `pg-<n>`, `pg-<a>_<b>` | Page (1-based) / Pages (WP-62) |
| `l-image,i-<path>|ie-<b64>,lx,ly,lfo,w,h,o,rt,…,l-end` | Watermark from the mount source: `lx`/`ly` offsets from top-left (`N` = from right/bottom), else `lfo` compass, else centre; `w`/`h` px size (fit); `o` opacity; `rt` rotation (clockwise, `N` prefix counter-clockwise) (WP-77) |
| `l-text,i-<text>|ie-<b64>,fs,ff,co,tg,al,w,rt,…,l-end` | Text watermark (escaped for Pango): Font = `ff` + `tg` (`b` bold, `i` italic) + `fs`, `tg` `strikethrough` → markup, Color = `co`, Opacity = `al`/10, TextWidth = `w` (wrap width; `h` approximated), `rt` rotation; `ff` naming a font file on the media library (`fonts@@x.ttf`) approximated (configured font) (WP-78) |
| `l-image,i-ik_canvas`, `l-video`, `l-subtitles`, nested layers, in-layer transformations, `lxc`, `lyc`, `lap`, `lm` | Unsupported |
| `<video>/ik-thumbnail.jpg`, `so-<seconds>` | A thumbnail of the video `<video>` (JPEG unless `f-` says otherwise); `so-` = Video.Second (WP-74), arithmetic expressions unsupported |
| `e-bgremove`, `e-removedotbg`, `e-upscale`, `e-retouch`, `e-genvar`, `e-dropshadow`, `e-shadow`, `e-gradient`, `e-changebg`, `e-edit`, `e-distort`, `e-colorize`, `cr`, `dn` | Unsupported |
| a file with a video extension, `<video>/ik-video.mp4` | A video transformation (§7.1) |
| `ik-s`, `ik-t` | signature, Expires |

### 7.1 Video transformations (WP-125)

**Status:** M5 (WP-125; `internal/dialect/imagekit/video.go`). Needs `XI_VIDEO_TRANSFORM_ENABLED=true` (full and gpl images) and, by default, a signed URL.

Reference: https://imagekit.io/docs/video-transformation, video-resize-and-crop, trim-videos, video-optimization.

```
/imagekit/<mount>/sample-video.mp4?tr=so-5,du-5,w-640,h-360,fo-top
/imagekit/<mount>/tr:w-300,h-300/sample-video.mp4
/imagekit/<mount>/clips/a.mov/ik-video.mp4?tr=vc-none          (audio only)
```

A file whose extension is a video one (`mp4`, `webm`, `mov`, `m4v`, `mkv`, `avi`, `ogv`, `3gp`, `mpeg`/`mpg`, `ts`, `flv`, `wmv`, `qt`), or any file followed by `/ik-video.mp4`, is a `KindVideo` request unless `f-` asks for an image format; `<video>/ik-thumbnail.jpg` stays the thumbnail path, and `<video>/ik-master.m3u8` / `.mpd` is an HLS / DASH stream (§3.10) with the renditions of `sr-`. Steps (`:`) apply in order onto one VideoOp, like Cloudinary's components (§6.1); signing, `ik-t`, `ik-attachment` and the cache-busting query work as for images. Without `f-` the output follows ImageKit's default `f-auto`.

| ImageKit | VideoOp | Notes |
|---|---|---|
| `so-`, `eo-`, `du-` (seconds, up to 2 decimals) | Start, End, Duration | `eo-` with `du-`: `eo-` wins; arithmetic expressions (`idu_mul_0.4`) recorded unsupported |
| `w-`, `h-` (px), `ar-<w>-<h>` (or `_`) | Width/Height | fractions, `auto` and expressions: "needs the source size or duration" |
| `c-maintain_ratio` (default with both sides) / `c-force` / `c-at_max` / `c-at_max_enlarge` | Cover / Fill / ScaleDown / Contain | `c-maintain_ratio_no_enlarge`, `c-at_least` approximated (Cover); one side → Contain |
| `fo-<compass>` | Gravity (cover crop, pad position, extract anchor) | `fo-auto`, `fo-face`, objects approximated (centre) |
| `cm-pad_resize` (+ `bg-<colour>`) | Pad + Background | `cm-pad_resize_no_enlarge` approximated; `bg-blurred`/`dominant`/`gradient` unsupported; `cm-pad_extract` unsupported |
| `cm-extract` with `w-`,`h-` and `fo-` or `x-`,`y-` | Crop (before the resize) | `xc-`/`yc-` unsupported; an extract after a resize unsupported |
| `f-auto` (default), `f-mp4`, `f-webm`, `ik-video.mp4` | AutoContainer (WebM for `Accept: video/webm`, else MP4; `Vary: Accept`), MP4, WebM | `f-orig` approximated (auto); an image format (`f-jpg`, `f-png`, …, the last `f-` of the string) keeps the frame path, as `ik-thumbnail.jpg` does |
| `vc-h264`, `vc-vp9`, `vc-av1`, `vc-none` | VideoCodec; `vc-none` = audio only (M4A, or Ogg Opus with `ac-opus`) | a codec the container cannot hold is 400 |
| `ac-aac`, `ac-opus`, `ac-none` | AudioCodec, Mute | |
| `q-<1..100>` | Quality | |
| `fps-<1..240>` | FPS | |
| `rt-<90k>`, `rt-N<deg>` | Rotate | other angles and `rt-auto` unsupported |
| `dpr-<0.1..5>` | multiplies the sizes | `dpr-auto` approximated (1) |
| `l-image`, `l-text` … `l-end` | Overlays (WP-124, FORMATS §5.2): the layer as on images (§7: `i`/`ie`, `lx`/`ly` with `N`, `lfo`, `w`/`h`, `o`, `rt`, and for text `ff`, `fs`, `tg`, `co`, `al`); `lso`/`leo`/`ldu` (seconds) → Start/End on the output's timeline | at most 8; an overlay on an audio-only output (`vc-none`) is 400; `leo` with `ldu`: `leo` wins; `l-video`, `l-subtitles` and nested layers unsupported |
| `sr-<h>_<h>…` with `ik-master.m3u8` / `.mpd` | Stream renditions (heights 64–4320, at most 8; without `sr-`, the configured ladder) | other `ik-master.*` suffixes are 400; `sr-` on another output recorded "applies to ik-master.m3u8 and ik-master.mpd only"; `w-`, `h-`, layers, `ac-opus` and `vc-h265` recorded "not applied to streams" (400 in strict mode); `vc-none` is 400 |
| `br-` | — | unsupported (the unit is not documented) |
| image-only parameters (`b-`, `r-`, `bl-`, `o-`, `t-`, `z-`, `fl-`, `e-*`, `pr-`, `lo-`, `md-`, `cp-`, `orig-`, `di-`, `pg-`), `n-` | — | "not applicable to video" / unsupported |

Signing (imagekit-nodejs `getSignature`): `ik-s` = hex(HMAC-SHA1(private key, path-relative-to-endpoint ‖ ["?" query without `ik-s`/`ik-t`] ‖ expiry)), expiry = the `ik-t` value or `9999999999` when absent (the SDK signs but does not write it). The private key is used as written (`XI_DIALECTS_IMAGEKIT_KEYS`, one list per mount). An `ik-t` in the past is 404 (`KindExpired`), checked after the signature.

## 8. Cloudflare Images (`/cdn-cgi/image/`)

**Status:** served on `main` (WP-43, M2), including `onerror=redirect`; face gravity is on `main` (WP-72, M3, `full` image).

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
| `gravity`/`g` = `auto` / `face` / `left` / `right` / `top` / `bottom` / `XxY` (0–1) | Smart (faces first) / Face (WP-72; no face → Smart; slim → 422) / West / East / North / South / FocusPoint |
| `zoom`/`face-zoom` (0–1) with `gravity=face` | How closely the crop frames the faces: 0 (default) = face gravity only places the crop, z > 0 cuts the faces' box × 1/z first (FacePad, WP-72); without `gravity=face` → `Ignored` |
| `quality`/`q` = 1–100 or `high` / `medium-high` / `medium-low` / `low` | Quality (90 / 80 / 65 / 50) |
| `slow-connection-quality`/`scq` (same values) | SlowConnectionQuality + `ClientHints.SaveData` (used when Save-Data/ECT hints say slow) |
| `format`/`f` = `auto` / `avif` / `webp` / `jpeg` / `baseline-jpeg` / `png` / `gif` | negotiate (FormatUnset) / Format / JPEG+Progressive / JPEG+Progressive=false / … (Cloudflare Images has no video output value; video is its separate Media Transformations product, §8.2) |
| `format=json` | Ignored "not implemented" (Cloudflare's JSON is not the /info schema; the image is served) |
| `dpr` (> 0) | DPR |
| `anim=false` / `true` | DisableAnimation |
| `background` (`%23RGB[A]`, `%23RRGGBB[AA]`, CSS names, `transparent`, `rgb()`/`rgba()` with commas or spaces and `/ alpha`) | Background |
| `blur` (0–250) | Blur σ = v/2 |
| `sharpen` (0–10) | Sharpen σ = v/4 |
| `brightness` (multiplier) | Adjust.Exposure (Adjust.Brightness is additive) |
| `contrast`, `saturation` (multipliers), `gamma` (0 and 1 = unchanged) | Adjust |
| `rotate=0|90|180|270`, `flip=h|v|hv` | Rotate, Flip |
| `trim=border`, `trim.border.color`, `trim.border.tolerance` (0–255, 255 = exact) | Trim (Threshold = 255 − tolerance, default 10); `trim.border.keep` → Ignored |
| `trim=top;right;bottom;left`, `trim.top/right/bottom/left/width/height` (px, or a fraction < 1) | Crop anchored north-west: left/top are gravity offsets, width/height the size; right/bottom fractions fold into a relative size when left/top are fractions, right/bottom pixels → Ignored "not implemented" |
| `border=color;width`, `border=color;top;right;bottom;left`, `border.color/width/top/right/bottom/left` (Workers-only upstream) | Border |
| `metadata=keep` / `copyright` / `none` | StripMetadata=false / StripMetadata+KeepCopyright / StripMetadata, KeepCopyright=false |
| `onerror=redirect` | Delivery.OnErrorRedirect: when the fetch or decode fails with a fallback-eligible error, the server answers 302 to the source URL (http sources only; ahead of the fallback image; the error Cache-Control; same path as wsrv `default=`, ARCHITECTURE §12) |
| `compression=fast` | Output.Effort = 1 (the format is not forced to JPEG) |
| `segment=foreground`, `upscale=generate` | Ignored "unsupported" (400 in strict mode); `upscale=interpolate` is the default |
| anything else | Ignored "unknown option" (400 in strict mode) |

### 8.1 Cloudflare Images delivery (`/cdn-cgi/imagedelivery/`, `imagedelivery.net`)

**Status:** served on `main` (WP-79, M2; dialect `cloudflare_images`, `sig`/`exp` signing).

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

### 8.2 Media Transformations (`/cdn-cgi/media/`, WP-125)

**Status:** M5 (WP-125; dialect `cloudflare_media`, `internal/dialect/cloudflare/media.go`). `mode=video` and `mode=audio` need `XI_VIDEO_TRANSFORM_ENABLED=true` (full and gpl images) and, by default, a signed URL (`xi_sig`); `frame` and `spritesheet` are images (full image) and follow the mount's unsigned policy.

Reference: https://developers.cloudflare.com/stream/transform-videos/.

```
/cdn-cgi/media/mode=video,time=5s,duration=5s,width=500,height=500,fit=crop,audio=false/https://example.com/a.mp4
/cdn-cgi/media/mode=frame,time=3s,width=640,format=png/videos/a.mp4
/cdn-cgi/media/mode=spritesheet,duration=10s,width=160/videos/a.mp4
/cdn-cgi/media/mode=audio,time=1m,duration=30s/videos/a.mp4
```

Mounted at `/cdn-cgi/media/` next to every Cloudflare mount whose prefix ends in `/cdn-cgi/image/` (the default), with that mount's source and unsigned policy; on a host-bound or differently prefixed Cloudflare mount, a path continuing with `/cdn-cgi/media/` is handed to the media parser. Sources are those of `/cdn-cgi/image/` (an absolute URL, possibly percent-encoded, or a path on the mount's source). Options are a comma-separated `key=value` list; a later value of a key wins.

| Option | Mapping | Notes |
|---|---|---|
| `mode=video` | `KindVideo`, MP4 (H.264 in gpl images, AV1 in full; Cloudflare: H.264/AAC) | |
| `mode=audio` | `KindVideo`, M4A (AAC) | `audio=false` is 400; `format` other than `m4a` is 400 |
| `mode=frame` | the video thumbnail path: `Video.Second` = `time`, JPEG (`format=jpg`) or PNG (`format=png`) | other formats 400 |
| `mode=spritesheet` | the `video_thumbnail_tile` path: `imageCount` frames (default 10, 1–100; an xtandard extension) spread over `duration` (or the whole video) in a near-square grid, JPEG, cells of `width`×`height` (`fit=cover` fills them) | `time` > 0 recorded "sprite sheets start at the beginning of the video" |
| `time` (`5s`, `1.5s`, `2m`; 0–10m) | Start (frame: the frame) | other forms and ranges 400 |
| `duration` (1s–60s) | Duration; default 60 s, clamped to the source as Cloudflare does | |
| `width`, `height` (10–2000) | Width/Height (frame: the resize) | |
| `fit=contain` / `scale-down` / `cover` (`crop`, as in Cloudflare's example) | Contain / ScaleDown / Cover; one side only → Contain | other values 400 |
| `audio=false` (video mode) | Mute | |
| `filename` (letters, digits, `-`, `_`; ≤ 120) | Delivery.Filename (Content-Disposition) | |
| an option of another mode, an unknown option | `Ignored` ("not applicable in this mode" / "unknown option"); 400 in strict mode | |

Limits beyond the URL ranges are the video subsystem's (`XI_VIDEO_TRANSFORM_MAX_*`; Cloudflare caps inputs at 100 MB and 10 minutes, here `XI_VIDEO_TRANSFORM_MAX_INPUT_*` decide).

Cloudflare Stream's manifests (`customer-<code>.cloudflarestream.com/<video id>/manifest/video.m3u8`) belong to a different product (videos uploaded to Cloudflare and addressed by id) and are not mapped; HLS and DASH of a source URL are the other dialects' streams (§3.10).

## 9. Bunny Optimizer

**Status:** served on `main` (WP-44, M2), with token authentication.

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
| `aspect_ratio=w:h` (`auto` = none) | Crop.AspectRatio anchored by `crop_gravity`. With `width`+`height` of the same ratio (what unpic emits) it is Resize fill-down (fill with `upscaling=resampling`) instead |
| `crop=w,h` / `crop=w,h,x,y` (px, x,y = top-left) | Crop with gravity from `crop_gravity` / Crop at north-west + offsets |
| `crop_gravity=center|north|south|east|west|northeast|northwest|southeast|southwest` (+ unpic aliases `top|bottom|left|right|top_left|…`) | Gravity |
| `focus_crop=w,h,x,y` | Crop w×h: relative x,y (0–1 with a decimal point) → focus-point gravity; pixel x,y → north-west offsets (x−w/2, y−h/2), clamped by the planner |
| `face_crop=true` / `face_crop=w,h` | Face gravity for the resize crop / a w×h crop of the source centred on the faces (WP-72); no face → Smart | slim → 422 |
| `quality` (0–100) | Quality |
| `format` / `output` = `webp|jpeg|png|gif|avif` | Format |
| `auto_optimize=low|medium|high` | Quality 85/75/65 unless `quality` is set (format negotiation is the default) |
| `sharpen=true|false` (Bunny's examples also use `sharpen=<n>`) | Sharpen σ 1 (numeric: same, recorded as approximated) |
| `blur` (0–100) | Blur σ = v/4 |
| `flip`, `flop` | Flip V / H |
| `rotate` (−270…270, multiples of 90) | Rotate (normalised to 0/90/180/270) |
| `brightness` (−100..100) | Adjust.Brightness = v × 2.55 |
| `contrast`, `saturation` (−100..100) | multiplier 1 + v/100; `saturation=-100` is grayscale |
| `gamma` (−100..100) | multiplier 2^(v/100) |
| `hue` (0–100, 33 ≈ one primary) | Adjust.Hue = v × 3.6° |
| `tint=rrggbb` (`ffffff` = none) | Tint |
| `sepia` (0–100) | Sepia on when > 0 (intensity not modelled) |
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

**Status:** served on `main` (WP-45, M2), with HMAC-SHA1 signing, pixel focal points (WP-86a) and the `meta` JSON response (WP-86b); `smart` uses faces in the `full` image (WP-72, M3).

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
| `smart` | Gravity Smart (overrides halign/valign): the union of the detected faces when the face model finds some (WP-72, `XI_DETECT_SMART_FACES`), else libvips attention (thumbor's feature detector is not reproduced); `meta` lists the faces as `Face Detection` focal points |
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

**Status:** served on `main` (WP-46, M2), including root `?url=` routing, `default` redirects and `maxage`.

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
| `output=jpg|png|gif|tiff|webp|avif|jxl|origin|json` | Format (origin = unset) / Info (imgproxy info schema, recorded as ignored); weserv has no video output value (`output=mp4` is 400, like upstream) |
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

**Status:** served on `main` (WP-47/48, M2): all 21 providers below, and all 28 unpic providers have a passing fixture file.

unpic 4.2 (https://unpic.pics/providers/, source `ascorbic/unpic/src/providers/*.ts`) supports 28 providers. Seven are full dialects above; the other 21 only emit width, height, quality, format and a fit/crop hint, so they are implemented as **data** in `internal/dialect/simple`: one parser engine plus a `Spec` table per provider (native prefixes, upstream domains, an extractor that cuts source and parameters out of the URL, and a `Param` per name). Adding a provider is a `<name>.go` table (listed in `Providers()`) plus its fixture file `tools/compat/unpic/<name>.json`.

**Coverage of the 28 unpic providers**

| unpic provider | xi | Fixture file |
|---|---|---|
| imgix, imagekit, cloudinary, cloudflare, cloudflare_images, bunny, wsrv | full dialects (§5–§9, §11) | `<name>.json` (run through the full dialects by `simple.TestUnpicDedicated`) |
| appwrite, astro, builder.io, cloudimage, contentful, contentstack, directus, hygraph, imageengine, ipx, keycdn, kontent.ai, netlify, nextjs, scene7, shopify, storyblok, supabase, uploadcare, vercel, wordpress | `simple:<name>` (`builder.io` → `builderio`, `kontent.ai` → `kontentai`) | `<name>.json` (run by `simple.TestUnpicFixtures`) |

**Mounting** (`XI_DIALECTS_SIMPLE_ENABLED=nextjs,ipx,…`): each enabled provider is mounted at its native prefixes (below) and at `/simple/<name>/`, under which the provider-native path follows (`/simple/contentful/<space>/<asset>/<token>/a.jpg?w=400`). The parser puts a stripped native prefix back, so every table matches the URL exactly as unpic writes it. `XI_DIALECTS_SIMPLE_HOSTS` binds the whole family to hosts through one dispatcher that picks, per request, the provider with the longest native prefix matching the path, else the provider whose upstream domain equals the request host, else the first enabled provider without native prefixes (the CDN-style ones: contentful, shopify, …). Dialect names are `simple:<name>` (`simple` for the dispatcher).

**Engine rules.** Parameters apply in URL order (last wins). Unknown parameters go to `TR.Ignored` ("unknown option"), documented-but-unapplied ones as "unsupported"; both are 400 with `XI_DIALECTS_STRICT`. Malformed values are always 400 naming the parameter. Width/height/fit/enlarge are collected and applied last: *cover* → `fill`, *inside* → `fit`, *scale-down* → `fit` without enlarging, *fill* (stretch) → `force`, *outside* → `outside`, *pad* (letterbox) → `fit` + `extend` at the gravity. Formats `auto`/`origin`/`preserve` leave the format to negotiation; formats xi does not encode (blurhash, jp2, …) are ignored. Sources: an embedded absolute http(s) URL is a plain source; an embedded relative path, and the path of a CDN-style URL, is a key relative to `XI_DIALECTS_SIMPLE_SOURCE` (parameters marked "source" stay in its query). No provider signs URLs: xi_sig (§2) is verified over the provider-native path and query; unsigned requests follow `XI_DIALECTS_SIMPLE_UNSIGNED_POLICY` (default: the global policy, `restricted`), which suits client-generated unpic URLs.

**Per provider** (fit default in bold; "enl." = enlarges by default; everything not listed is recorded as unknown):

| Provider | Native prefix / upstream host | Source | Mapping |
|---|---|---|---|
| nextjs, vercel | `/_next/image`, `/_vercel/image` | `url` | `w` width, `q` quality; **inside**, no enlarge; format by Accept |
| netlify | `/.netlify/images` | `url` | `w`, `h`, `q`, `fm` (blurhash ignored), `fit` contain→inside / cover / fill→stretch, `position` gravity; **inside** |
| astro | `/_image` | `href` | `w`, `h`, `q`, `f`, `fit` cover / contain→inside / fill / inside / outside / none→outside / scale-down, `position`, `background`; **cover**, no enlarge |
| ipx | `/_ipx/<mods>/<src>` (`_` = none; `k_v,k_v`) | path after modifiers | `w`/`width`, `h`/`height`, `s`/`resize` WxH, `q`, `f`, `fit` (sharp: contain→pad), `pos`/`position`, `b`/`background`, `blur`, `sharpen` (σ), `rotate`, `flip` (vertical), `flop`, `grayscale`, `negate`, `enlarge`; unsupported: extract, crop, trim, extend, kernel, gamma, tint, …; **cover** |
| directus | `/assets/<id>` | path (+`access_token`) | `width`, `height`, `quality`, `format`, `fit` cover / contain→pad / inside / outside, `withoutEnlargement`, `download`; unsupported: `transforms`, `key`; **cover**, enl. |
| contentful | `images.ctfassets.net` | path | `w`, `h`, `q`, `fm`, `fit` pad / fill→cover / crop, thumb→cover / scale→stretch, `f` focus (incl. face/faces), `bg`, `fl` progressive/png8; unsupported: `r`; **inside** |
| builderio | `cdn.builder.io` | path | `width`, `height`, `quality`, `format`, `fit` (sharp), `position`, `sharp` (silent); **cover** |
| contentstack | `images.contentstack.io`, `*-images.contentstack.com` | path | `width`, `height`, `quality`, `format` (pjpg, webpll…), `fit` bounds→inside / crop→cover, `disable=upscale`, `dpr`, `blur`, `bg-color`; `auto` silent (negotiation); overlays, trim, pad, canvas, colour ops unsupported; **inside**, enl. |
| keycdn | `ip.keycdn.com`, `*.kxcdn.com` | path | `width`, `height`, `quality`, `format`, `fit` (sharp), `position`, `enlarge`, `crop=smart`, `bg`, `rotate`, `flip`/`flop`, `sharpen`, `blur`, `negate`, `grayscale`, `progressive`, `lossless`, `metadata`; overlays (`ol*`), trim, extend, tone ops unsupported; **cover** |
| kontentai | `*.kc-usercontent.com` | path | `w`, `h`, `q`, `fm`, `fit` clip→inside / scale→stretch / crop→cover, `fp-x`/`fp-y` focus, `dpr`, `bg` (ARGB), `lossless`, `auto` silent; unsupported: `rect`, `fp-z`; **inside** |
| scene7 | `/is/image/`, `*.scene7.com` | path | `wid`, `hei`, `qlt` (q[,chroma]), `fmt` (fmt[,…]), `fit` crop→cover / stretch / wrap→outside / fit, constrain, hfit, vfit→inside with `,1` = enlarge, `dpr`, `bgColor`/`bgc`, `rotate`, `flip` lr/ud/lrud, `op_blur`; layers, text, masks, ICC, crop unsupported; **pad** (Image Serving's default), no enlarge |
| shopify | `cdn.shopify.com` | path without the size suffix | `width`, `height`, `crop` position→cover, `pad_color`→pad, `format`, `quality`; legacy `_WxH`/`_crop_<pos>` file suffix when the query has no size; `v` dropped; **inside** |
| storyblok | `a.storyblok.com`, `img2.storyblok.com` | `f/<space>/…/<file>` | `/m/[crop/][fit-in/][-W x -H/][smart/][filters:…]` (legacy: before `/f/`): size (minus flips), `fit-in`→inside (+`fill` → pad), `smart`; filters `format`, `quality`, `fill`, `grayscale`, `blur`, `rotate`, `no_upscale`; unsupported: crop rectangle, `focal`, `brightness`, `round_corner`; **cover**, enl. |
| supabase | `/storage/v1/render/image/public/`, `/storage/v1/object/public/`, `*.supabase.co` | the public object path | `width`, `height`, `quality`, `format` (origin), `resize` cover / contain→inside / fill→stretch; **cover** |
| uploadcare | `ucarecdn.com` | `<uuid>/` | `/-/<op>/<args>/`: `resize` WxH (both sides stretch), `preview` (inside, no enlarge), `scale_crop` WxH[/pos], `smart_resize`, `format`, `quality` (number or lightest…best), `progressive`, `stretch`, `setfill`, `rotate` (counter-clockwise), `flip`, `mirror`, `grayscale`, `autorotate`, `strip_meta`; crop, border_radius, blur, sharp unsupported; **inside**, enl. |
| hygraph | `*.graphassets.com` | `<env>/<handle>` | tasks `resize=width:,height:,fit:clip→inside/crop→cover/scale→stretch/max→scale-down,align:`, `output=format:`, `quality=value:`, `blur=amount:`, `sharpen=amount:`, `rotate=deg:`; `auto_image`, `compress` silent; **inside** |
| imageengine | `*.imgeng.in` | path + other query parameters | `imgeng=/k_v/…`: `w`, `h`, `f`, `cmpr` (quality = 100−cmpr), `quality` (unpic), `m` cropbox→cover / box→inside / letterbox→pad / stretch / outside, `s` sharpen, `r` rotate, `meta`, `dl`; pass, pc, cr, maxdpr unsupported; **inside** |
| cloudimage | `*.cloudimg.io` | path as `https://` URL (`ci_url_encoded`, `v7/`) | `w`/`width`, `h`/`height`, `q`, `force_format`, `func` crop, cropfit→cover / fit→pad / bound→inside / boundmin→outside / cover→stretch / face, `gravity` (compass, auto, face, `Xp,Yp`), `org_if_sml` (no enlarge), `bg_color`, `r` (counter-clockwise), `flip`, `blur`; pixel crops, trim, radius unsupported; **inside**, enl. |
| wordpress | `*.wp.com`, `files.wordpress.com` | path (`<origin host>/<path>`) | `w`, `h`, `crop=1`→cover, `resize=W,H`→cover, `fit=W,H`→inside, `lb=W,H`→pad, `quality`, `format`, `zoom` dpr, `strip`; `ssl` silent; filters unsupported; **inside** |
| appwrite | `/v1/storage/buckets/` | path (+`project`) | `width`, `height`, `quality`, `output`, `gravity`, `rotation`, `background`; borders, radius, opacity unsupported; **cover** |

Storyblok's thumbor-style filter list (`name(args):…`) is parsed with `internal/dialect/thumbor/filters`, the thumbor dialect's grammar.

## 13. AWS Serverless Image Handler ("Dynamic Image Transformation for Amazon CloudFront")

**Status:** served on `main` (WP-85, M2), with `signature`/`expires` verification and thumbor forwarding.

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
| `overlayWith: {bucket, key, wRatio, hRatio, alpha, options: {left, top, gravity}}` | `P.Watermarks` with a per-request `Source` (bucket must be listed, else 403), `Opacity` = 1 − alpha/100, `Scale` = wRatio/100 (`hRatio` I), position from `gravity` or `left`/`top` (px or `"<n>p"`; negative = from the right/bottom edge) (WP-77) |
| `smartCrop: true \| {faceIndex, padding}` | `CropObjects` on class face: face `faceIndex` (default 0, best score first like Rekognition's confidence order) + `padding` px on every side, cut from the source (WP-72) | with `resize` SIH cuts from the resized image, xi before resizing: the result has the requested size (I "approximated"); `faceIndex` past the faces → uncut (SIH 400); slim → 422 |
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

Each dialect WP ships (a) a table of URLs → expected TR JSON, (b) URLs produced by the upstream SDK/transformer (`@imgix/js-core`, `cloudinary` npm, `imagekit-javascript`, `unpic` transformers, imgproxy URL examples) generated by a small Bun script (`generate.ts`) and committed as fixtures, (c) signature test vectors from upstream docs or code, (d) a fuzz target that must never panic.

What exists (every dialect below is on `main` except where noted; paths under `internal/dialect/<name>/testdata/` unless stated):

| Dialect | (a) golden tables | (b) upstream-generated URLs | (c) signature vectors | (d) fuzz |
|---|---|---|---|---|
| imgproxy | `golden/` | `jscore/` (`imgproxy-js-core`); TS builder cross-check `testdata/urls/imgproxy-builder.json` (WP-84) | imgproxy docs and `.refs` vectors (`internal/security/sign`) | yes |
| query | `golden/` | — (imaginary has no URL builder) | imaginary `sign` vector | yes |
| imgix | `golden/` | `jscore/` (`@imgix/js-core` 3.8.0, signed) and unpic `imgix.json` | blueprint vectors | yes |
| cloudinary | `golden/` | `urlgen/` (`cloudinary-url-gen`) and unpic `cloudinary.json` | SHA-1/SHA-256 vectors | yes |
| imagekit | `golden/` | `sdk/` (`imagekit-javascript`, `imagekit-nodejs`) and unpic `imagekit.json` | published dummy-key vectors | yes |
| cloudflare, cloudflare_images | `golden/` (`image`, `delivery`, `source`) | unpic `cloudflare.json`, `cloudflare_images.json` | Images `sig`/`exp` vectors | yes |
| bunny | `golden/` (`options`, `tokens`) | unpic `bunny.json` | token vectors from Bunny's docs | yes |
| thumbor | `golden/` (`grammar`, `filters`, `signed`, `corpus`: thumbor docs and test-suite URLs) | — | HMAC-SHA1 vectors | yes |
| wsrv | `golden/` | `unpic/` and unpic `wsrv.json` | — (unsigned; `xi_sig`) | yes |
| simple (21 providers) | per-provider tests | unpic `<name>.json` in `tools/compat/unpic/` | — (unsigned; `xi_sig`) | yes |
| awssih | `golden/` (ported from the solution's own tests) | — | `internal/security/sign/testdata/awssih/vectors.json` (generated by the solution's algorithm under Bun) | yes |
