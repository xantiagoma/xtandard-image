# jxrlib patches

`build-deps.sh` applies these in order (`git apply`) to jxrlib at `JXRLIB_COMMIT`
(`versions.sh`). They are Milian Wolff's (KDAB) fixes to jxrlib, BSD-2-Clause like jxrlib
itself, as Debian ships them in its `jxrlib` package (1.2~git20170615.f752187-5.3,
`debian/patches/<commit>.patch`):

| Patch | Upstream commit | Fix |
|---|---|---|
| `0001-use-mkstemp-instead-of-tmpnam.patch` | 082bb032be1f6c75173bf603252e4f37bfded9fa | the encoder's spill files use `mkstemp` instead of `tmpnam` |
| `0002-fix-left-shift-of-negative.patch` | a684f95783f2f81bd13bf1f8b03ceb12aa87d661 | undefined behaviour (`-1 << 31`) in the Huffman decoder |
| `0003-fix-unaligned-loads.patch` | ab9c6b78b7ad3205bdb91ef725b09ddbe3c8945d | unaligned 32-bit loads in the bit reader go through `memcpy` |
| `0004-do-not-print-from-the-library.patch` | (ours, WP-97) | the container parser no longer writes unknown tags to stderr, nor the decoder an out-of-memory note to stdout: the library runs inside the server, whose standard streams carry its JSON logs |
| `0005-validate-stream-headers.patch` | (ours, WP-97, found by fuzzing) | a colour format outside the known ones left the channel count at 0: the decoder then sized its buffers with `cNumChannels - 1` and wrote through a NULL macroblock row; the two header asserts of `PKImageDecode_Initialize_WMP` become checks (the build defines `NDEBUG`) |
| `0006-declare-byteswap-ulong.patch` | Debian `0013-jxrlib-ftbfs-with-GCC-14.patch` | declares `_byteswap_ulong` before its use (an implicit declaration, an error in GCC 14 and clang) |
| `0007-include-wchar.patch` | Debian `0015-FTBFS-fix-for-function-prototype-error.patch` | includes `<wchar.h>` for the metadata code's `wcslen` |
| `0008-fix-overreads-found-by-fuzzing.patch` | (ours, WP-97, found by fuzzing) | `DecodeSignificantRun` read past `gSignificantRunBin` and its remap tables in a corrupt block; `ReadWMIHeader` ran `strstr` over the 8-byte signature, which is not NUL-terminated |
| `0009-free-decoder-contexts-on-failure.patch` | (ours, WP-97, found by fuzzing) | with `REENTRANT_MODE` (jxrlib's default) the codec contexts were freed only after the last row: every failed decode leaked them; they are now freed when `Copy` fails and when the decoder is released |
| `0010-free-alpha-context-and-failed-inits.patch` | (ours, WP-97, found by fuzzing) | `ImageStrDecTerm` never freed the block `ImageStrDecInit` allocates for an interleaved alpha plane, and `ImageStrDecInit` returned without freeing its blocks when it failed after allocating them (LeakSanitizer) |
| `0011-lookup-tables-bounds-and-leak.patch` | (ours, WP-97, found by fuzzing) | the primary and the interleaved alpha plane share the offset lookup tables and both built them, leaking the first set; the plane header's bit depth indexed an 11-entry table unchecked |
| `0012-cap-codec-context-allocations.patch` | (ours, WP-97, found by fuzzing) | a corrupt alpha plane header made `ImageStrDecInit` ask for 117 GB; its per-plane buffers (two macroblock rows) are capped at 1 GiB |
| `0013-free-repeated-and-byte-metadata.patch` | (ours, WP-97, found by fuzzing) | a descriptive metadata tag repeated in the container leaked its first value, and values stored as BYTE/UNDEFINED arrays were never freed |
| `0014-bound-metadata-reads.patch` | (ours, WP-97, found by fuzzing) | `ReadBinaryData` allocated `uCount + 2` bytes for a count read from the file (3.6 GB requests; `0xFFFFFFFF` wraps to a 1-byte buffer the read then overflows); counts above 16 MiB are refused |
| `0015-alpha-plane-uses-the-image-lookup-tables.patch` | (ours, WP-97, found by fuzzing) | the shared offset tables were rebuilt from the interleaved alpha plane's header; a plane claiming a subsampled format halved them and the image read past their end (heap over-read); they are built once, from the image |
| `0016-pad-the-lookup-tables.patch` | (ours, WP-97, found by fuzzing) | the output loops index the offset tables by macroblock position, which a corrupt stream pushes past the image (heap over-read); the tables cover whole macroblocks plus a spare, zeroed |
| `0017-refuse-rows-past-the-plane.patch` | (ours, WP-97, found by fuzzing) | `Copy` asked a planar alpha plane for as many macroblock rows as the image has; one whose header declares fewer was decoded past its last row (heap over-read); the decode now fails |
| `0018-output-needs-enough-channels.patch` | (ours, WP-97, found by fuzzing) | an RGB/RGBE/YUV (or CMYK) output of a codestream coding fewer channels read the missing planes through NULL; such streams are refused, as are packed depths (5, 10, 5-6-5 bits) outside RGB and 1-bit outside grey (a planar alpha plane of a packed RGB image took the RGB path with one channel) |
| `0019-planar-alpha-must-match-the-image.patch` | (ours, WP-97, found by fuzzing) | the planar alpha plane's codestream could change its output to 32-bit floats (or another size) while being written into the image's 8-bit pixels, past the caller's buffer (heap overflow); the decode fails when its sample type or size differs from the image's |
| `0020-caption-must-be-a-byte-array.patch` | (ours, WP-97, found by fuzzing) | a caption tag of a numeric type was terminated as a UTF-16 string through its number used as a pointer (wild write); only byte-array captions are kept |

0001-0003 and 0006-0007 are Debian's (the latter two by YOKOTA Hiroshi); 0004, 0005 and 0008-0020 are
ours, under the same BSD-2-Clause licence as jxrlib.
