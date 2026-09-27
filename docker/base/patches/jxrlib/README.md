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

0001-0003 and 0006-0007 are Debian's (the latter two by YOKOTA Hiroshi); 0004, 0005 and 0008-0013 are
ours, under the same BSD-2-Clause licence as jxrlib.
