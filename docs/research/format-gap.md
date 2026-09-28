# Format gap research (WP-100)

Status: research, 2026-09-27. Owner request: "check which image and RAW formats we might be missing (Wikipedia's *Image file format* and *Raw image format* pages; e.g. do we have .cr3?) ... ideally examples of each format with its different configurations, as photographers and designers produce them in Affinity, Photoshop, Lightroom, Illustrator, etc."

This document does not change a decoder or a base image. It records what the current images decode, measured against a corpus of about 300 public sample files, and proposes implementation work packages (WP-101 to WP-108) in priority order.

## 1. Summary

- **CR3: yes.** Canon CR3 (CRX codec, lossless RAW and lossy C-RAW, every aspect-ratio crop, EOS M50 to EOS R6 Mark III and PowerShot V1) decodes on both paths: the embedded preview and LibRaw's demosaic. LibRaw has had a native CR3 decoder since its 2019 snapshot (0.20). The DNG SDK, the GPR SDK and X3F tools are not needed for it.
- **Camera RAW overall:** 65 of 72 RAW samples render through the default path. 66 of 72 render with the demosaic forced. The samples cover 24 makers, 24 containers, phones, drones and DNG encodings. The remaining failures are:
  - **Nikon High Efficiency (HE/HE\*).** LibRaw has no decoder for it (it is intoPIX TicoRAW). On a Z50 II HE file the demosaic path answers **200 with garbage pixels**, because LibRaw does not flag that body. On the Z8 and Z f it answers 422 "unsupported". The embedded preview is correct for all three. This is the one correctness bug found (WP-101).
  - **A body newer than LibRaw 0.22.2** (Sony A7 V) is refused. So are Sigma X3F (LibRaw was built without `USE_X3FTOOLS`), GoPro GPR (no GPR SDK) and one Leaf MOS file.
  - **The default `XI_LIMITS_MAX_SRC_PIXELS` of 50 MP** refuses 50 to 102 MP sensors: Fujifilm GFX100, Hasselblad X1D/X2D and the Google Pixel 8 Pro's 50 MP DNG. They decode once the limit is raised.
- **Designer files:** Photoshop decodes in every colour mode Photoshop writes: RGB, CMYK and Lab at 8 and 16 bits, 32-bit float, greyscale, duotone, indexed, bitmap and multichannel. Smart objects, artboards and adjustment layers decode through the stored composite, for PSD and PSB (18 of 18). PDF-compatible Illustrator files render through PDFium. GIMP XCF, OpenRaster/Krita, Paint.NET, Procreate, Affinity, Sketch and Figma are not decoded. OpenRaster/Krita and Procreate are cheap to add because they ship a flattened PNG inside a ZIP.
- **Everything else:** 247 of 290 gallery samples render on the `full` image. There are 39 expected failures (tagged `gap`/`error`) and 4 failures by design (EPS needs the `gpl` image, and multi-part OpenEXR is refused at probe). Variants that fail today:
  - 12- and 16-bit JPEG. These answer **500** instead of 422 (WP-101).
  - AVIF image sequences (`avis`).
  - CMYK JPEG XL.
  - CIE Lab TIFF.
  - ZSTD-compressed TIFF. The base libtiff is built without zstd.
  - DDS with DX10 headers and BC6H.
  - SVGZ.
- **Per flavour:** `slim`, `full` and `gpl` decode the same still-image formats. Video needs `full` or `gpl`, and EPS/PS needs `gpl` with `XI_FORMATS_EPS_ENABLED=true`. Measured: slim 243 ok, full 247 ok, gpl 250 ok (gpl adds the 3 EPS samples).

## 2. Method

1. **Inventory:** every raster, vector, document and camera-RAW format on the two Wikipedia pages ([Image file format](https://en.wikipedia.org/wiki/Image_file_format), [Raw image format](https://en.wikipedia.org/wiki/Raw_image_format)). To that we added what photographers and designers actually produce in Photoshop, Lightroom, Illustrator, InDesign, Affinity, Figma, Sketch, GIMP, Krita, Procreate and on phones and drones (§3, §4).
2. **Code and build review:**
   - `internal/codec` (sniffing, format table, `codec/raw`).
   - `docker/base/build-deps.sh`. LibRaw 0.22.2 is configured with `--enable-jpeg --enable-zlib --enable-lcms`. It has no `USE_X3FTOOLS`, no `USE_DNGSDK`, no `USE_GPRSDK`, no RawSpeed and no OpenMP.
   - `versions*.sh`.
   - LibRaw's `Changelog.txt` in `.refs/libraw`.
3. **Corpus:** `internal/playground/samples.json`, 290 public URLs with licence, `variant` and `tags` (§7).
4. **Matrix:** the release image built from this branch was used.
   - Build: `XI_APP_BASE=xtandard-base:dev-full XI_IMAGE=xtandard-image:wp100 task release:build`.
   - `main-gpl` and `wp99-slim` were also built, for the other flavours.
   - Run: `XI_DEV_MODE=true XI_SECURITY_UNSIGNED_POLICY=allow` with an `fs` source over local copies of every URL (downloaded once, 1.9 GB, so fetch timeouts do not count as format failures). `XI_LIMITS_MAX_SRC_BYTES=256MiB`, default pixel limits.
   - Each sample got `/info/unsafe/plain/<src>` and `/unsafe/rs:fit:160:160/f:webp/plain/<src>`.
   - A second run forced the RAW demosaic (`XI_FORMATS_RAW_PREVIEW_MIN_RATIO=100`, pixel limits 250 MP, `XI_OBSERVABILITY_DEBUG_HEADERS=true` to read `X-Xi-Raw-Path`). Suspicious results were rendered at 800 px and inspected by eye.
   - The playground's Format matrix tool (`#tools/matrix`) runs the same probe from the browser: filter by format, variant column, large files opt-in.

## 3. Inventory and status

Legend:
- ✅ decodes (verified on a sample)
- ⚠️ partial (some variants fail; details in the row)
- ❌ not decoded (422)
- — not an image format

"Flavour" is where it works: all = slim, full and gpl.

### 3.1 Photographic and web raster

| Format / variant | Status | Verified samples | Notes |
|---|---|---|---|
| JPEG baseline, progressive, arithmetic, greyscale | ✅ all | 6 | Arithmetic coding through libjpeg-turbo 3 |
| JPEG CMYK / YCCK (Adobe APP14), with and without ICC | ✅ all | 2 | Converted to sRGB |
| JPEG EXIF orientation 1–8, EXIF/XMP/IPTC/ICC | ✅ all | 5 | |
| JPEG lossless (SOF3), 8-bit | ✅ all | 1 | libjpeg-turbo 3; since WP-106 every lossless frame goes through the shim's `jpeghpload_buffer` |
| JPEG 12-bit, lossless 16-bit | ✅ since WP-106 | 5 | libvips uses the 8-bit libjpeg API (500 before WP-101, 422 until WP-106); the shim's `jpeghpload_buffer` reads them with libjpeg-turbo 3's 12/16-bit APIs as 16-bit images (FORMATS 1.6) |
| UltraHDR / gain-map JPEG (Apple, ISO 21496-1, Android) | ✅ SDR base, HDR with `preserve_hdr` | 2 | Gain map applied for AVIF (PQ), JPEG XL and UltraHDR JPEG outputs (WP-104, FORMATS 1.4); Adobe's JPEGs that libultrahdr refuses too |
| MPO (stereo, multi-picture) | ✅ first image | 1 | |
| JPEG-LS | ❌ | 1 | Sniffed as JPEG, refused at probe with 422 "JPEG-LS is not supported". Needs CharLS (BSD-3-Clause) in the base image; left out of WP-106 (base revision, ADR line) |
| JPEG XT | ❌ untested | 0 | Base layer is plain JPEG, so it decodes as SDR JPEG. No public sample found |
| JPEG XS | ❌ | 1 | Broadcast mezzanine codec. Low demand, no plan |
| PNG 1/2/4/8/16-bit, grey, palette+tRNS, alpha, Adam7, gAMA, iCCP, eXIf, XMP orientation | ✅ all | 12 | 16-bit kept only with `preserve_hdr` |
| APNG (rgba, palette+alpha, 16-bit) | ✅ first frame | 3 | libvips limitation (FORMATS 1) |
| GIF static, transparent, interlaced, local palettes, mixed disposal | ✅ all | 5 | |
| GIF with more than 256 frames | ⚠️ by limit | 1 | `XI_LIMITS_MAX_ANIMATION_FRAMES` (256) |
| WebP lossy, lossless, alpha, animated, EXIF orientation | ✅ all | 6 | |
| AVIF 8/10/12-bit, grey, alpha, grid, irot/imir, ICC/EXIF/XMP, PQ HDR (P3, Rec. 2020), wide-gamut SDR, gain map (tmap) | ✅ all | 11 | HDR is tone-mapped to SDR. For gain maps, the SDR base is used, and HDR is rendered with `preserve_hdr` (WP-104) |
| AVIF image sequence (`avis`, `.avifs`) | ✅ (WP-104) | 2 | The shim's libheif sequence loader: animations, the first frame for stills |
| HEIC (iPhone, grid, burst, sequence, alpha, grey, 10/12-bit) | ✅ all | 7 | Sequence tracks (and bursts stored as one) animate since WP-104; image items by `page`. Depth and auxiliary images are ignored |
| HEIC gain map (iOS 18 adaptive HDR) | ✅ SDR base, HDR with `preserve_hdr` (expected) | 0 | The `tmap` path is the AVIF one (WP-104); no permissive sample found |
| Live Photo | — | — | A HEIC plus a separate MOV; each decodes on its own |
| JPEG XL (8/16-bit, float, grey, alpha, premultiplied alpha, lossless, recompressed JPEG, animation, spot channel, PQ) | ✅ all | 10 | |
| JPEG XL CMYK (black extra channel) | ✅ since WP-105 | 1 | libvips `jxlload` has no CMYK output; the shim's `jxlcmykload_buffer` (libjxl) reads it (FORMATS 1.6) |
| JPEG 2000 JP2/J2K/JPX, 8/16-bit, alpha, ICC | ✅ all | 6 | |
| HTJ2K (`.jph`) | ✅ | 1 | OpenJPEG 2.5 |
| JPEG XR (8-bit, alpha, float, CMYK, bilevel) | ✅ all | 5 | jxrlib (WP-97) |
| TIFF 1-bit (CCITT G4), grey, RGB 8/16-bit, float 16/32, CMYK(+alpha), multi-page, tiled, planar, BigTIFF, LZW/deflate/JPEG/WebP/LZMA/packbits, YCbCr JPEG, layered (Photoshop-style), pyramidal SVS | ✅ all | 15 | |
| TIFF CIE Lab | ✅ since WP-105 | 1 | libvips "bad labq format" on 8-bit CIELab; read through a copy relabelled RGB and converted with lcms (FORMATS 1.6) |
| TIFF ZSTD | ❌ | 1 | The base libtiff is built without zstd (WP-102) |
| TIFF JPEG XL (compression 50002) | ❌ | 1 | GDAL extension, not in libtiff. No plan |
| TIFF 12-bit packed, old-style JPEG (OJPEG) | ❌ | 2 | libvips limitation / obsolete. No plan |
| BMP 1/4/8-bit, RLE4/8, 16-bit 565, 32-bit alpha, v5 with ICC, OS/2 | ✅ all | 8 | Own Go loader |
| ICO/CUR (PNG and BMP entries, AND masks, v5 ICC), ICNS (PNG/JP2 and classic RLE), WBMP | ✅ all | 8 | Own Go loaders |
| OpenEXR half/float, DWAA, PIZ, HTJ2K, Y+A | ✅ all | 6 | OpenEXR 3.4 |
| OpenEXR multi-part / deep | ❌ by design | 1 | Refused at probe (FORMATS 1.1) |
| Radiance HDR (RLE, old RLE, `#?RGBE`), PFM (LE/BE) | ✅ all | 5 | |
| PNM (P1–P6, 16-bit), QOI, TGA (RLE, colour-mapped, 32-bit alpha), PCX (planar), SGI (8/16-bit), Sun raster, XBM, XPM | ✅ all | 21 | ImageMagick coders (WP-97) |
| DDS DXT1/3/5 (legacy header) | ✅ | 3 | Pure-Go decoder since WP-105 (ImageMagick before) |
| DDS DX10 header (BC1/BC4/BC7), BC6H | ✅ since WP-105 | 3 | ImageMagick answered "image type not supported"; `codec/dds` decodes BC1–BC7 and BC6H (tone-mapped like OpenEXR) |
| KTX2 / Basis Universal | ❌ | 1 | GPU texture container (WP-108) |
| DICOM | ❌ | 1 | Medical. libvips has no DICOM loader; its `magickload` is blocked (WP-108) |
| FITS | ❌ | 2 | Astronomy. libvips `fitsload` (cfitsio) is not built (WP-108) |
| MNG, JNG, PICT, Kodak Photo CD | ❌ | 4 | ImageMagick has coders. PICT was skipped by WP-97 (sniffer collision) (WP-108) |
| FlashPix, XWD, MIFF, WPG, IFF/ILBM, Pixar, BLP, Farbfeld, HEIF-in-TIFF, CALS, Cineon/DPX, PSP | ❌ | 0 | Long tail; listed on Wikipedia. No demand signal; ImageMagick coders exist for most (WP-108 candidate list) |

### 3.2 Vector, documents and designer files

| Format | Status | Notes |
|---|---|---|
| SVG | ✅ all | Sanitised. A DOCTYPE is refused by policy (the "Large SVG map" sample is 422 on purpose) |
| SVGZ (gzip) | ✅ (WP-107) | Gunzipped with a byte and ratio cap, then the SVG path (sanitiser, librsvg) |
| PDF (multi-page, CMYK images, transparency groups, DeviceN, JPX images, `/Rotate`) | ✅ all | PDFium, 8/8 |
| Illustrator `.ai`, PDF-compatible (AI 9+, the default "Create PDF Compatible File") | ✅ all | It is a PDF, rendered by PDFium |
| Illustrator `.ai` saved without PDF compatibility | ⚠️ | PDFium renders the placeholder page Illustrator stores ("saved without PDF content"). Nothing better exists without Adobe code |
| Illustrator ≤ 8 / EPS / PS | ✅ gpl | Ghostscript, `XI_FORMATS_EPS_ENABLED=true` (WP-99). Elsewhere a DOS-EPS TIFF preview or 422 |
| EMF / WMF | ❌ | Office and Visio clip art. The only open renderers are libwmf (GPL-2) and libemf2svg (GPL-2), so it could only go in the gpl flavour (WP-108) |
| CGM, HPGL, XFig | ❌ | Legacy technical drawing. No plan |
| Photoshop PSD/PSB: RGB/CMYK/Lab 8/16-bit, 32-bit float, grey, duotone, indexed, bitmap, multichannel, smart objects, artboards, adjustment/fill layers, spot channels | ✅ all | ImageMagick PSD coder reads the stored composite (18/18). A file saved with "Maximize compatibility" off falls back to ImageMagick's layer flattening (FORMATS 1.3) |
| Layered TIFF (multi-layer, Photoshop-style) | ✅ | Composite IFD first; verified on a LayeredImage sample, not a Photoshop-written file |
| GIMP `.xcf` | ✅ (WP-107) | No stored composite: ImageMagick's XCF coder decodes each visible layer, flattened in Go (8-bit, RLE; FORMATS 1.7) |
| OpenRaster `.ora` (MyPaint, Krita export) and Krita `.kra` | ✅ (WP-107) | `mergedimage.png` (full-size composite), else the thumbnail, through a bounded pure-Go ZIP reader |
| Paint.NET `.pdn` | ✅ thumbnail (WP-107) | The header XML's base64 PNG thumbnail (≤ 256 px). The body is a .NET serialisation, not read |
| Procreate `.procreate` | ✅ thumbnail (WP-107) | `QuickLook/Thumbnail.png` (screen size). The full composite needs its LZO tiles and `Document.archive` (NSKeyedArchiver), as axaril/silicate (MIT) does; not done |
| Affinity `.afphoto` / `.afdesign` / `.af` (Affinity 2 / 3) | ❌ | Proprietary, undocumented and compressed. Reports of an embedded preview are unverified, and no permissively licensed sample exists. Recommendation: users export PSD/TIFF/PDF. No WP |
| Sketch `.sketch` | ✅ preview (WP-107) | `previews/preview.png` (first page, ≤ 2048 px) through the same ZIP reader |
| Figma `.fig` | ❌ | Proprietary binary (Kiwi). Figma users export PNG/SVG/PDF, all supported. No WP |
| InDesign `.indd` / `.idml` | — | Page layout, not images. `.indd` embeds JPEG page thumbnails in XMP (`xmpGImg`). Exported PDFs are supported |
| Office documents (DOCX, PPTX) | — | Not images. Out of scope |

### 3.3 Camera RAW

LibRaw 0.22.2 against the market. "Default path" is the planner's choice: the embedded preview when it is large enough, else the demosaic. "Demosaic" is the run with the preview disabled. The ARW/NEF/RAF/… "container" is what `codec/raw.Sniff` reports.

| Brand / container | Variants verified | Default path | Demosaic | Notes |
|---|---|---|---|---|
| **Canon CR3** (CRX) | RAW and C-RAW (lossy), 1:1/3:2 crops; M50, 250D, R, R5, R6 III, PowerShot V1 | ✅ 6/6 | ✅ 6/6 | Native LibRaw decoder since 0.20 (2019). H.265 (HEIF) previews are skipped; the JPEG preview or demosaic is used (FORMATS 2) |
| Canon CR2 | RAW, sRAW, mRAW (YCbCr) | ✅ 4/4 | ✅ | |
| Canon CRW (CIFF) | 300D | ✅ | ✅ | |
| **Nikon NEF** lossless / uncompressed / lossy type 1–2 / small RAW, NRW, Coolscan NEF | Z6, D3, D5300, D810 sRAW, D2H, P1100, Coolscan V | ✅ 7/7 | ✅ 7/7 | |
| **Nikon NEF High Efficiency (HE, HE\*)** | Z50 II HE, Z8, Z f | ✅ preview | ❌ Z8/Z f 422; **Z50 II: 200 with garbage** | intoPIX TicoRAW. No open decoder exists (LibRaw 0.22 lists Nikon bodies as "standard compression only"). WP-101 guards it and falls back to the preview |
| **Sony ARW** v1/v2, lossless compressed (S/M/L), lossy, SR2 | A100, ZV-E10 II, A1 II (lossless S, lossy), R1 SR2 | ✅ 4/4 | ✅ | Sony's "YCC" M/S pseudo-RAW is new in 0.22 |
| Sony ARW, A7 V (2025) | 1 | ❌ | ❌ | Body newer than LibRaw 0.22.2 (WP-101: preview fallback). **WP-103: demosaics** with the LibRaw snapshot (ARW 6) |
| **Fujifilm RAF** X-Trans compressed, lossy, lossless, GFX 16-bit, SuperCCD, Bayer X100 | X-T30 III, X-E5, GFX100RF, S9600, X100 | ✅ 5/6 | ✅ 6/6 | GFX100RF (102 MP) is over the default 50 MP pixel limit (WP-102). X-Trans demosaic is the slowest: 1.3–2 s at half size, 4.9 s for 102 MP |
| **OM System / Olympus ORF, ORI** | OM-3, OM-5 II ORI | ✅ 2/2 | ✅ | 14-bit high-resolution decoder new in 0.22 |
| **Panasonic RW2**, Leica RWL | G100D, S1 II, D-LUX 6 | ✅ 3/3 | ✅ | Panasonic encoding 8 new in 0.22 |
| **Pentax PEF / DNG**, Ricoh DNG | K-3 III Monochrome, K-1 II pixel shift (176 MB), K-3 III DNG, GR IIIx | ✅ 4/4 | ✅ | Pixel shift: first frame |
| **Leica DNG** | M10, M9 8-bit, Monochrom (no CFA), M-series | ✅ 4/4 | ✅ | |
| Samsung SRW | NX500 14-bit lossless | ✅ | ✅ | |
| **Hasselblad 3FR / FFF**, Phase One IIQ (S, L), Leaf MOS | H3D, X1D II, P40+, P20+, Aptus 22 | ✅ 3/5 | ✅ 4/5 | X1D II (51 MP) is over the pixel limit. The Aptus 22 MOS file fails in LibRaw |
| Sigma X3F (Foveon) | SD14 | ❌ | ❌ | Needs `-DUSE_X3FTOOLS`. **WP-103: decodes** (X3F tools) |
| Sigma DNG (fp) | 1 | ✅ | ✅ | |
| Minolta MRW, Kodak DCR, Epson ERF | 3 | ✅ | ✅ | |
| **Apple ProRAW** (linear DNG, lossless JPEG, iPhone 12 Pro) and iPhone Bayer DNG | 2 | ✅ | ✅ | Newer iPhones can write ProRAW with JPEG XL compression (not in the corpus; see JXL DNG below) |
| **Samsung Expert RAW** (linear DNG, JPEG tiles), Google Pixel, Raspberry Pi HQ | 4 | ✅ 3/4 | ✅ 4/4 | Pixel 8 Pro 50.1 MP: over the pixel limit |
| **Drones**: DJI Mini 2, Phantom 4 Pro, Mavic 3 (Hasselblad L2D-20c) | 3 | ✅ | ✅ | |
| **GoPro GPR** (VC-5 in DNG) | HERO11 | ❌ (probe ok) | ❌ | Needs the GoPro GPR SDK (Apache-2.0 OR MIT). **WP-103: decodes in the gpl image** (GPR SDK VC-5 decoder through the DNG SDK; slim/full: 422 naming the gpl image) |
| **Lossy DNG** (Adobe, 8-bit JPEG tiles), **linear DNG** (lossy and lossless) | 3 | ✅ | ✅ | `--enable-jpeg` covers them |
| **DNG 1.7 JPEG XL** | 1 (Canon 7D II through a DNG converter) | ✅ | ✅ | That sample keeps the Bayer data in lossless JPEG. Only its "enhanced" image and previews are JPEG XL, and LibRaw decodes the former. A DNG whose main raw IFD is JPEG XL (Lightroom's JPEG XL option, ProRAW "JPEG-XL" on recent iPhones) needs LibRaw built with the Adobe DNG SDK 1.7 and libjxl. **WP-103: decodes in the gpl image** (Adobe's JPEG XL samples and `dng/jxl-landscape.dng`); slim/full serve the embedded preview or 422 |
| HDRMerge floating-point DNG (16-bit float, deflate) | 1 | ✅ | ✅ | |
| CinemaDNG (Blackmagic 3:1 lossy) | 1 | ✅ | ✅ | |
| ARRI ARI, RED R3D, Canon CRM, Blackmagic BRAW, Apple ProRes RAW | 0 | ❌ | ❌ | Cinema RAW. LibRaw dropped RED/ARRI in 0.22. BRAW and ProRes RAW need proprietary SDKs. Out of scope |

Speed (half-size demosaic, arm64 Docker, 3 concurrent requests, indicative only): 0.1–0.6 s for most 12–45 MP files, 1.3–2 s for X-Trans, 4.9 s for 102 MP GFX. The preview path answers in 5–40 ms (FORMATS 2 has the benchmarked numbers).

### 3.4 Other RAW decoders

| Project | Licence | Coverage vs LibRaw 0.22 | Usable here? |
|---|---|---|---|
| **LibRaw** (current) | LGPL-2.1 or CDDL-1.0 | Reference: 1284 cameras with all options. CR3 native. X3F, GPR and JPEG XL DNG only with optional code/SDKs | Yes (in use) |
| **rawspeed** (darktable) | LGPL-2.1, separately buildable C++ library | Decoders: ARW, CR2, CRW, DCR/DCS, DNG (LJPEG, lossy JPEG, deflate float), ERF, IIQ, KDC, MEF, MOS, MRW, NEF, ORF, PEF, RAF (incl. compressed), RW2 (Panasonic V4–V8), SRW, STI, 3FR, **VC-5 (GoPro GPR)**. **No CR3** (darktable uses LibRaw for CR3 since 3.8), no X3F, no Nikon HE, **no JPEG XL DNG** (rawspeed issue #516 open). Unpacks only; demosaic is darktable's | Not worth a second RAW stack. It would only add GPR, which the GPR SDK also gives LibRaw. LibRaw can even call rawspeed (`USE_RAWSPEED3`) for its fast paths. Any speed claim needs a benchmark (CLAUDE rule 13) |
| **RawTherapee** (rtengine, dcraw fork) | GPL-3.0 | Similar camera list; CR3 via LibRaw-derived code. 5.11+ reads JPEG XL images; DNG 1.7 JPEG XL was requested in issue #7131 | GPL: gpl flavour only, and a whole application, not a library. No |
| **dcraw** | Mostly public domain plus GPL parts; unmaintained since 2018 | No CR3, no modern compressions | No |
| **Adobe DNG SDK 1.7** | Adobe DNG SDK License Agreement (permissive, but with an indemnity clause for commercial redistribution; ADR 0006 amendment WP-103); bundles XMP SDK (BSD) and libjxl (BSD), neither used | DNG 1.7 incl. JPEG XL raw and Enhanced images, opcode lists | Yes, as a LibRaw option (`USE_DNGSDK`) in WP-103, in the gpl flavour only (owner decision 2026-09-28) |
| **GoPro GPR SDK** | Apache-2.0 OR MIT | VC-5 decode for `.gpr` | Yes, as a LibRaw option (`USE_GPRSDK`) in WP-103; gpl only, since LibRaw reaches it through the DNG SDK |
| **X3F tools** (Kalpanika, inside LibRaw) | BSD-style, shipped in LibRaw's tree | Foveon X3F incl. Quattro | Yes, a compile flag (`-DUSE_X3FTOOLS`) in WP-103 |
| **ExifTool** | GPL-1.0+ or Artistic | Extracts embedded previews from every RAW, HE included | Perl, GPL/Artistic. The app can already take LibRaw's thumbnail list (`codec/raw`). No |
| libraw-contrib / LibRaw snapshots | same as LibRaw | New bodies arrive in snapshots months before a release | Pin a snapshot per base release (WP-103) |

## 4. Ranked gaps

Ranked by real-world demand (photographers and designers first), then correctness risk. Effort: S < 1 day, M 1–3 days, L > 3 days. Risk is the security risk of the new code on untrusted input.

| # | Gap | Who hits it | Today | Candidate | Licence | Effort | Risk | Flavour | WP |
|---|---|---|---|---|---|---|---|---|---|
| 1 | Nikon HE/HE\* demosaic returns garbage (Z50 II) or 422 (Z8, Z f; the option also exists on the Z9 and Z6 III) | Nikon Z shooters | Preview-sized requests ok; larger ones garbage/422 | Detect HE (`NEFCompression` 13/14, LibRaw decoder flags) → force the embedded preview; 422 only without one | — | S | none (less decoding) | all | 101 |
| 2 | Brand-new bodies unknown to LibRaw (Sony A7 V) and failing files (Leaf MOS) | Early adopters | 422 | Preview fallback when LibRaw refuses to unpack; LibRaw snapshot per base release | LGPL-2.1 | S + M | low | all | 101, 103 |
| 3 | Sensors > 50 MP refused by the default pixel limit (GFX100, X2D, A7R V, Pixel 50 MP, Phase One) | Pros, phones | 422 limit | RAW-aware limit: count the preview / half-size demosaic that is actually decoded, with its own ceiling (`XI_LIMITS_MAX_RAW_DEMOSAIC_PIXELS` already exists) | — | S | low | all | 102 |
| 4 | JPEG XL DNG (Lightroom "JPEG XL", ProRAW JPEG-XL on recent iPhones) | Lightroom and iPhone users | 422 expected (untested: no public sample) | LibRaw + Adobe DNG SDK 1.7 + libjxl (already in base) | Adobe DNG SDK (permissive) | L | medium (large C++ SDK) | all | 103 |
| 5 | AVIF image sequences (`avis`) | Web (animated AVIF) | 422 | Shim: libheif sequence API, first frame or all frames | LGPL-3 (libheif, already in base) | M | low | all | 104 |
| 6 | HDR gain maps flattened to SDR (UltraHDR JPEG, AVIF/HEIC `tmap`) | iPhone/Pixel HDR photos | SDR base | Keep the gain map (libultrahdr, libheif) and pass it through or apply it for HDR outputs | Apache-2.0 / LGPL-3 | L | low | all | 104 |
| 7 | 12/16-bit JPEG → **500** | Medical/scientific, some scanners | 500 | Map lazy-decode errors to 422 (WP-101). Decode with libjpeg-turbo 3's `j12`/`j16` API in a shim loader (WP-106) | IJG/BSD | S + M | low | all | 101, 106 |
| 8 | SVGZ | Illustrator "SVG Compressed", icon sets | 422 | Gunzip with a byte cap, then the SVG sanitiser | — | S | low (zip bomb capped) | all | 107 |
| 9 | OpenRaster / Krita `.kra` | Krita, MyPaint | 422 | ZIP reader, `mergedimage.png` | — | S | low (bounded ZIP) | all | 107 |
| 10 | Procreate `.procreate` | iPad illustrators | 422 | ZIP `QuickLook/Thumbnail.png` (screen size); full composite later | — | S (thumb) / L | low | all | 107 |
| 11 | GIMP `.xcf` | GIMP users | 422 | ImageMagick XCF coder (policy, like WP-97) | ImageMagick | S | medium (old coder, fuzz it) | all | 107 |
| 12 | CIE Lab TIFF, CMYK JPEG XL | Prepress | 422 | Shim: tell libvips the 8-bit CIELab interpretation; `jxlload` with the black extra channel | LGPL-2.1 / BSD | M | low | all | 105 |
| 13 | TIFF ZSTD | GIS, scientific | 422 | Build libtiff with zstd (BSD) | BSD | S | low | all | 102 |
| 14 | Sigma X3F, GoPro GPR | Sigma and GoPro owners | 422 | `USE_X3FTOOLS`, GPR SDK | BSD-style, Apache-2.0/MIT | M | medium | all | 103 |
| 15 | DDS DX10 (BC1/4/5/7), BC6H; KTX2 | Game art pipelines | 422 | Own Go BCn decoder (DX10 header) or ImageMagick update; basisu transcoder for KTX2 | — / Apache-2.0 | M | low (Go) | all | 105, 108 |
| 16 | EMF / WMF | Office clip art | 422 | libwmf (GPL-2) | GPL-2 | M | medium | gpl only | 108 |
| 17 | DICOM, FITS, JPEG-LS | Medical, astronomy | 422 | DCMTK (BSD-3) or GDCM (BSD-3); cfitsio (NASA, permissive) through libvips `fitsload`; CharLS (BSD-3) | permissive | M each | medium | all | 106, 108 |
| 18 | MNG/JNG, PICT, Photo CD, Paint.NET thumbnail, Sketch preview | Legacy archives | 422 | ImageMagick coders; ZIP/XML thumbnail readers | ImageMagick | S each | medium | all | 107, 108 |
| — | Affinity, Figma, InDesign, cinema RAW (BRAW, ProRes RAW, R3D, ARI) | — | 422 | Proprietary, no open decoder. Recommend export | — | — | — | — | none |

## 5. Proposed work packages

In priority order. Each needs an ADR line for any new dependency (ADR 0006) and follows AGENT-WORKFLOW.

- **WP-101 Decode correctness: Nikon HE guard, RAW preview fallback, lazy-decode error mapping** (P0).
  - Scope:
    1. `codec/raw`: read LibRaw's decoder info and Nikon `NEFCompression` in the probe. For HE/HE\* (and any file LibRaw marks unsupported or fails to unpack, like the A7 V and the Aptus MOS), plan the embedded preview whatever `XI_FORMATS_RAW_PREVIEW_MIN_RATIO` says. Answer 422 "RAW compression not supported (embedded preview too small)" only when there is no usable preview.
    2. Map libvips decode errors that surface inside the save (`VipsJpeg: Unsupported JPEG data precision`, and every `*load` error in a lazy pipeline) to `corrupt`/`unsupported` (422), never `internal` (500).
  - Done when:
    - The Z50 II HE, Z8, Z f and A7 V gallery samples render from their previews at 160 and 2000 px with correct pixels (DSSIM against the preview).
    - The 12-bit JPEG samples answer 422.
    - The playground matrix shows no 500.
    - Unit tests use synthetic headers; app tests use gallery samples behind `-tags network`.
- **WP-102 Limits and base codecs for modern cameras** (P1).
  - Scope:
    1. A RAW-aware pixel check. For the preview path, count the preview's pixels. For the demosaic path, count the half-size output when the plan uses `half_size`. The sensor keeps `XI_LIMITS_MAX_RAW_DEMOSAIC_PIXELS`, whose default is raised to 120 MP so GFX100, X2D and Phase One IQ4 files work, bounded by the memory budget.
    2. Build libtiff with zstd (BSD-3-Clause) and check that LZMA, WebP and deflate stay on.
  - Done when:
    - The GFX100RF, X1D II and Pixel 8 Pro samples render with default limits.
    - The ZSTD TIFF sample renders.
    - The limits documentation in CONFIG.md and FORMATS §2 is updated.
- **WP-103 LibRaw coverage: snapshots, X3F, GPR, DNG SDK** (P1).
  - Scope: LibRaw pinned to the latest snapshot at each base release, with a camera-list diff in the base README. Build options (base image, all flavours):
    - `-DUSE_X3FTOOLS`.
    - GoPro GPR SDK (Apache-2.0 OR MIT).
    - Adobe DNG SDK 1.7 with the libjxl already in the base.
  - Each option comes with a size, fuzzing and licence note (ADR 0006 amendment).
  - Done when:
    - The X3F and GPR gallery samples render.
    - A JPEG XL raw DNG (to be added to the gallery once a CC0 sample exists; raw.pixls.us has none yet) renders.
    - A LibRaw fuzz run (ASan, like WP-97's jxrlib campaign) is clean.
    - The image size delta is reported.
- **WP-104 AVIF/HEIF sequences and HDR gain maps** (P1). This WP is split if it grows past L.
  - Scope:
    1. `avis` sequences: first frame, and frames for animated outputs, via libheif's sequence API in the shim (libvips 8.18 `heifload` refuses the brand).
    2. Gain maps: keep UltraHDR/ISO 21496-1 gain maps from JPEG and `tmap` from AVIF/HEIC. With `preserve_hdr` and an HDR-capable output (AVIF, JPEG XL, UltraHDR JPEG), write HDR; otherwise use the SDR base as today.
  - Done when:
    - The two sequence samples render and animate to WebP.
    - The gain-map samples produce HDR AVIF with `preserve_hdr`, with goldens.
- **WP-105 Colour-model and texture fixes** (P2).
  - Scope:
    - CIE Lab TIFF (8/16-bit).
    - CMYK JPEG XL (black extra channel → libvips CMYK).
    - DDS with DX10 headers and BC6H: a pure-Go BCn decoder (BC1–BC7, fuzzed) replaces ImageMagick for DDS.
  - Done when: the Lab TIFF, CMYK JXL and four DDS gallery samples render, and fuzz and leak tests pass.
  - Done (2026-09-28): all six samples render (FORMATS 1.4 and 1.6).
- **WP-106 High-precision JPEG family** (P2).
  - Scope: a shim loader over libjpeg-turbo 3's 12- and 16-bit APIs (`jpeg12_*`/`jpeg16_*`) for 12-bit DCT and lossless 16-bit JPEG, output 16-bit. Optional JPEG-LS through CharLS (BSD-3-Clause), after an ADR line.
  - Done when: the three high-precision JPEG samples and the JPEG-LS sample render, and `FuzzJPEG12` runs clean.
  - Done (2026-09-28): 12-bit DCT and 2–16-bit lossless JPEG decode through the shim's `jpeghpload_buffer` (FORMATS 1.6), sample-exact against djpeg; the 12-bit and lossless gallery samples render. JPEG-LS was left out (CharLS needs a base revision) and answers a clear 422.
- **WP-107 Designer containers** (P2).
  - Scope (pure Go, bounded `archive/zip`: entry count, uncompressed size and ratio caps, no path traversal):
    - OpenRaster/Krita `mergedimage.png`.
    - Procreate `QuickLook/Thumbnail.png`.
    - Sketch `previews/preview.png`.
    - SVGZ (gunzip with a cap, then the SVG path).
    - Paint.NET header thumbnail.
    - GIMP XCF through the ImageMagick XCF coder (policy widening, with the same Go header probe as WP-97).
  - Done when:
    - The ORA, XCF, PDN and SVGZ gallery samples render.
    - Procreate and Kra fixtures are generated in `testdata/gen` (no public permissive samples exist).
    - Zip-bomb and path tests pass.
    - Fuzz targets per reader.
- **WP-108 Scientific and legacy long tail** (P3, only on demand). Delivered 2026-09-28, with FITS, DICOM and MNG/JNG read in Go instead of the libraries named below (`docs/FORMATS.md` §1.8, ROADMAP).
  - Scope, one item per request:
    - FITS through libvips `fitsload` (cfitsio).
    - DICOM through DCMTK or GDCM (BSD-3-Clause), first frame.
    - KTX2 through the Basis Universal transcoder (Apache-2.0).
    - MNG/JNG, PICT and PCD through ImageMagick coders (PICT needs the WP-97 sniffer collision solved).
    - EMF/WMF through libwmf in the gpl flavour only.
  - Done when: each added format's gallery sample renders and its coder is fuzzed.

## 6. Answers to specific questions

- **Do we have .cr3?** Yes, in every flavour, including C-RAW (lossy CR3), cropped aspect ratios and 2025 bodies (EOS R6 Mark III, PowerShot V1). Both preview and demosaic paths were verified.
- **Lossy DNG (Lightroom "Lossy Compressed DNG", Adobe DNG Converter)?** Yes, Bayer and linear. LibRaw is built with libjpeg.
- **Linear DNG, Apple ProRAW, Samsung Expert RAW?** Yes (demosaic of the linear data, JPEG tiles).
- **JPEG XL DNG (DNG 1.7)?** Yes since WP-103 in the `gpl` image (Adobe DNG SDK 1.7.1 inside LibRaw); `slim`/`full` serve its embedded preview or answer 422. A DNG that only carries JXL previews or an enhanced image decodes from its lossless JPEG raw, as before.
- **Nikon HE/HE\*?** Preview only, and today only when the request fits the preview; a larger request is a correctness bug (WP-101). No open-source HE decoder exists.
- **Sony lossless compressed ARW, Fujifilm compressed/lossy RAF, OM ORI, Panasonic encoding 8?** Yes.
- **X3F, GPR?** Yes since WP-103: X3F in every flavour (X3F tools), GPR in `gpl` only (GoPro GPR SDK through the DNG SDK).
- **rawspeed instead of LibRaw?** No: no CR3, no HE, no JXL DNG. It would add only GPR, which the GPR SDK also gives.
- **Photoshop 16/32-bit, CMYK, Lab, duotone, smart objects?** Yes, through the composite. Lab and CMYK are converted to sRGB.
- **Illustrator?** PDF-compatible `.ai` renders through PDFium in every flavour; PostScript `.ai` renders through Ghostscript in `gpl`.
- **Affinity, Figma, InDesign?** No; proprietary. Export PSD/TIFF/PDF/PNG/SVG.

## 7. Sample corpus

`internal/playground/samples.json`: 290 public URLs, linked and never vendored or downloaded by the build.

**Fields.** Each entry has `format`, `name`, `variant` (the configuration, such as `"cmyk 16-bit"` or `"Canon CR3 C-RAW (lossy), 2025 body"`), `url`, `bytes`, `licence` (SPDX), `source` (the attribution; CC-BY entries name the author), `notes` (the expected outcome) and `tags`.

**Tags.**
- Colour: `rgb`, `cmyk`, `lab`, `grey`, `1-bit`, `palette`, `duotone`, `multichannel`, `ycbcr`.
- Depth: `8-bit`, `10-bit`, `12-bit`, `14-bit`, `16-bit`, `16-bit-float`, `32-bit-float`.
- Other properties: `alpha`, `animated`, `multi-page`, `layered`, `orientation`, `icc`, `wide-gamut`, `hdr`, `gain-map`, `lossless`, `lossy`, `tiled`.
- Camera: the brand and the container (`cr3`, `nef`, `he`, …), `phone`, `drone`, `linear-dng`.
- `large`: over 20 MB (38 files, mostly RAW). The gallery loads no automatic thumbnail for these, and the matrix skips them unless asked.
- `gap`: 37 samples expected to fail today; the notes name the WP.
- `error`: deliberately malformed files.

**Sources and licences:**

| Source | Licence |
|---|---|
| raw.pixls.us | CC0-1.0 |
| cgohlke/imagecodecs-samples | CC-BY-4.0, by Christoph Gohlke |
| ImageGlass/sample-images | MIT |
| python-pillow/Pillow test images | MIT-CMU |
| image-rs/image test images | MIT OR Apache-2.0; PngSuite and bmpsuite originals |
| AOMediaCodec/libavif test data | BSD-2-Clause; files marked NC in its README excluded |
| libjxl/conformance | CC0 cases only |
| libjxl/testdata | CC-BY-4.0 |
| psd-tools test files | MIT |
| FHPythonUtils/LayeredImage | MIT |
| ImageMagick PerlMagick test images | ImageMagick |
| libjpeg-turbo test images | BSD-3-Clause / IJG |
| mozilla/pdf.js test PDFs | Apache-2.0 |
| resvg | MIT |
| KTX-Software / basis_universal | Apache-2.0 |
| OpenSlide test data | CC0 |
| NASA FITS samples | public domain |
| Kodak suite | "unrestricted usage" |
| alexschiller/file-format-commons | CC0 |

`TestGallery` checks the licence allowlist, variant, tags and the `large` flag. `go test -tags network ./internal/playground -run TestGalleryURLsAlive` HEADs every URL and checks its size.

**Not found under a permissive licence:** Affinity, Sketch and Figma files (Procreate, Krita, XCF, SVGZ and Paint.NET samples were found later, WP-107, and are in the gallery); Apple ProRAW with JPEG XL; Nikon HE\* explicitly labelled; HEIC with an ISO gain map; JPEG XT. These are candidates for synthetic fixtures in the WPs above.
