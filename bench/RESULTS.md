# Benchmark results: xtandard-image vs imgproxy OSS

One run of `bench/run.sh` on 2026-09-26 on a developer laptop (Apple M1 Pro, arm64). The
methodology, the cases and the matched settings are in [docs/BENCHMARKS.md](../docs/BENCHMARKS.md).
Every number below comes from that run. The raw per-run data (156 k6 runs, with CPU, memory and
host load for each) is in [results-2026-09-26-m1pro.jsonl](results-2026-09-26-m1pro.jsonl).

**Read this with the noise in mind.** The laptop was not idle: other development work (Docker
builds, Go test runs) shared the machine, and the host's 1-minute load average before each run
had a median of 34.6 and a range of 12 to 127 on 8 cores. The runs are interleaved (each point
is measured on both servers back to back, 3 times) so the noise hits both servers alike, but the
single-run spread is large, especially at p99 (see the p99 range column). Treat differences under
about 20 % as noise. A quiet dedicated Linux host would give tighter numbers; rerun there before
quoting absolute capacity.

## Headline

Throughput ceiling (closed loop, 16 concurrent requests, 8 workers each, median of 3 runs):

| Case | xtandard-image (req/s) | imgproxy OSS (req/s) | Difference |
|---|---:|---:|---:|
| 400 px fit, JPEG → JPEG | 285.5 | 218.5 | +31 % |
| 400 px fit, JPEG → WebP | 224.3 | 178.8 | +25 % |
| 400 px fit, JPEG → AVIF | 236.9 | 182.6 | +30 % |
| 400 px fit, PNG with alpha → WebP | 60.8 | 70.4 | **−14 %** |
| 240 px fit, animated GIF (12 frames) → animated WebP | 24.2 | 21.2 | +14 % |
| 1200x1200 fill, JPEG → JPEG | 87.0 | 57.9 | +50 % |
| `/info` (xtandard-image only; imgproxy Pro feature) | 5164.3 | – | – |

Peak container memory under load is **2 to 5 times higher** for xtandard-image (298 to 749 MiB
against 127 to 346 MiB), while both idle at about 16 MiB.

Output sizes (measured with the same images and fixtures right after the run) are within 4 % of each other for
every case, and identical for GIF, so both servers are doing comparable work: for example
10,572 vs 10,762 bytes for the JPEG thumbnail, 6,117 vs 6,372 for AVIF, 72,085 vs 72,275 for the
1200 px fill.

## Interpretation

Where xtandard-image is ahead:

- **JPEG sources and large outputs.** Both use libvips shrink-on-load and the same encoder
  settings, and the lead is largest for the 1200 px fill (+50 %) where the resize and encode
  dominate. It has not been profiled, so the cause is not established. Candidates are libvips
  8.18.6 in our base image against the version bundled in imgproxy 4.0.15, jemalloc against
  imgproxy's default allocator, and PGO in our build.
- **Behaviour near saturation.** At about 90 % of the slower server's ceiling, imgproxy's
  unlimited queue lets latency grow to 1 to 4 seconds with dropped iterations (k6 could not start
  requests on time): 1.35 s p99 for JPEG→JPEG at 250 req/s, where xtandard-image stayed at 87 ms.
  xtandard-image bounds its queue (`workers x 16` requests, 5 s wait). It keeps latency lower and
  answers the excess with **429** (for AVIF at 180 req/s, 1,939 requests over the three runs, a median of 25.5 %).
  Whether fast rejection beats slow success depends on the deployment: behind a CDN with retries,
  429 is usually what you want. `XI_PROCESSING_QUEUE_SIZE` tunes it.

Where xtandard-image is behind:

- **PNG with alpha to WebP: 14 % lower ceiling and a worse tail** (p99 541 ms vs 373 ms in the
  closed loop; 477 ms vs 352 ms at 30 req/s). This is the main performance gap found. The PNG
  decode path (libspng in our base image), alpha premultiplication around the resize and the
  alpha-channel WebP encode are the candidates. It has not been profiled yet.
- **Memory.** Peak container memory is 2 to 5 times imgproxy's in every case (749 MiB for the
  animated GIF). Likely contributors: jemalloc with
  `MALLOC_ARENA_MAX=2` holding freed pages, the Go heap (GOMEMLIMIT is only set from a cgroup
  limit, and there was none here) and the per-request vips arenas for animated sources. For
  memory-limited containers this is the number to watch. `XI_PROCESSING_MEMORY_BUDGET` and a
  cgroup limit (which sets `GOMEMLIMIT`) should bound it, but that was not measured here.
- **Low-rate tails are mixed.** At 25 % load both servers are within noise at p50. The p99 values
  flip between servers from run to run (for example JPEG→AVIF at 50 req/s: 123 ms for us against
  67 ms for imgproxy, with a 25 to 230 ms range across our three runs). This run cannot separate
  the servers there.
- **`/info` goes through admission.** At 2,500 req/s the 128-slot queue filled and 24.7 % were
  answered 429. The request is only a header read, so a separate lighter admission path for
  `/info` may be worth it.

Next steps: profile the PNG-alpha path and the memory high-water mark (pprof on the admin
listener, `XI_ADMIN_PPROF=true`), then rerun on a quiet amd64 and arm64 Linux host before the
first release.

## Full results

- Date: 2026-09-26T16:33:05Z to 2026-09-26T17:24:47Z
- Host: Apple M1 Pro, 8 cores (6P + 2E), 16 GiB, macOS 26.6.1
- Docker: OrbStack 29.4.0, 8 CPUs, 8393601024 bytes, kernel 7.0.14-orbstack-00380-ga7e0a2dc9535, aarch64
- xtandard-image: `xtandard-image:bench-34ac996` (xtandard-image main-34ac996 (commit 34ac996e, built 2026-09-26T15:57:27Z, go1.27.1, linux/arm64)), `sha256:6e7f01beb81c9599013e5105fe01bd41a8da0ae46154baf1d869bc496a356cc2`, created 2026-09-26T15:57:27Z
- imgproxy: `darthsim/imgproxy:latest` (4.0.15), `darthsim/imgproxy@sha256:b42304f5e2edaa6ed492e60efa9bee28a4a5d57c80d4d8aa9990a3e131dbcd99`, created 2026-09-23T16:07:55.915321116Z
- k6: k6 v2.3.0 (commit/e088784614, go1.27.1, linux/arm64), `grafana/k6@sha256:e66db15b860113878fa74670e31f5e274830b7b6e42c8bff28b2f2d86a257603`
- Workers: 8 on both; closed-loop concurrency 16; 3 interleaved runs of 15s per server and point (medians below), 5s warm-up per case
- Idle memory after start: xtandard 16.61MiB, imgproxy 15.6MiB
- Host 1-minute load average before each run: median 34.59, range 12.32 to 126.81

### Fixed arrival rate

| Case | Rate (req/s) | Server | Achieved (req/s) | p50 (ms) | p95 (ms) | p99 (ms) | p99 range (ms) | Failed | Dropped | CPU avg (%) | Mem max (MiB) |
|---|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| gif-anim-240-webp | 5 | imgproxy | 5 | 90.8 | 117.3 | 155.2 | 118.7–304.3 | 0% | 0 | 46 | 93 |
| gif-anim-240-webp | 5 | xtandard | 5 | 94.1 | 155.4 | 182.7 | 162.8–565.9 | 0% | 0 | 47 | 469 |
| gif-anim-240-webp | 10 | imgproxy | 10 | 85.8 | 97.2 | 117.8 | 113.2–647.7 | 0% | 0 | 73 | 130 |
| gif-anim-240-webp | 10 | xtandard | 10 | 65.9 | 86.4 | 100.2 | 92.4–160.8 | 0% | 0 | 74 | 393 |
| gif-anim-240-webp | 18 | imgproxy | 17.3 | 745.5 | 1130.1 | 1280.7 | 304–1995.2 | 0% | 19 | 144 | 151 |
| gif-anim-240-webp | 18 | xtandard | 17.8 | 603 | 1099.6 | 1188.1 | 280.7–1985.3 | 0% | 19 | 168 | 718 |
| info | 500 | xtandard | 500 | 1.4 | 6 | 14.3 | 8.9–15.3 | 0% | 0 | 33 | 228 |
| info | 1000 | xtandard | 999.8 | 1.1 | 2.5 | 5.1 | 4.6–30.8 | 0% | 0 | 49 | 250 |
| info | 2500 | xtandard | 2464.8 | 48.2 | 345.2 | 586.2 | 203–751.3 | 24.7% (429: 22044) | 1435 | 102 | 350 |
| jpeg-1200-fill-jpeg | 15 | imgproxy | 15 | 39.6 | 52.8 | 60.5 | 57.8–204.2 | 0% | 0 | 53 | 156 |
| jpeg-1200-fill-jpeg | 15 | xtandard | 15 | 45.2 | 72.4 | 119.8 | 117.9–208.3 | 0% | 0 | 58 | 439 |
| jpeg-1200-fill-jpeg | 35 | imgproxy | 35 | 37.6 | 55.7 | 79.9 | 41.4–284.5 | 0% | 0 | 119 | 250 |
| jpeg-1200-fill-jpeg | 35 | xtandard | 35 | 33.2 | 38.9 | 46.4 | 38.8–112.6 | 0% | 0 | 108 | 538 |
| jpeg-1200-fill-jpeg | 65 | imgproxy | 55.8 | 940.6 | 1706.7 | 2072.5 | 60.8–3102.5 | 0% | 163 | 247 | 361 |
| jpeg-1200-fill-jpeg | 65 | xtandard | 64.9 | 37.4 | 84.3 | 160.1 | 134.3–1308.5 | 0% | 7 | 212 | 670 |
| jpeg-400-avif | 50 | imgproxy | 50 | 20.2 | 28.6 | 66.7 | 38.8–196.6 | 0% | 0 | 86 | 132 |
| jpeg-400-avif | 50 | xtandard | 50 | 21.7 | 52 | 122.7 | 25.1–229.8 | 0% | 0 | 91 | 380 |
| jpeg-400-avif | 100 | imgproxy | 99.9 | 23.9 | 38 | 52.7 | 36.4–245.5 | 0% | 0 | 165 | 151 |
| jpeg-400-avif | 100 | xtandard | 99.9 | 19.6 | 26.8 | 48.1 | 35.2–164 | 0% | 0 | 169 | 420 |
| jpeg-400-avif | 180 | imgproxy | 158.1 | 555.2 | 1505.3 | 1532.3 | 407.3–4467.8 | 0% | 380 | 299 | 154 |
| jpeg-400-avif | 180 | xtandard | 168.4 | 795.3 | 1344.4 | 1404.7 | 1358.4–1631.2 | 25.5% (429: 1939) | 0 | 299 | 559 |
| jpeg-400-jpeg | 50 | imgproxy | 49.9 | 17.9 | 67.3 | 121.6 | 37.7–283.9 | 0% | 0 | 76 | 126 |
| jpeg-400-jpeg | 50 | xtandard | 50 | 14.4 | 25.6 | 42.3 | 20.1–309.1 | 0% | 0 | 56 | 230 |
| jpeg-400-jpeg | 125 | imgproxy | 124.9 | 19.7 | 54.8 | 94.6 | 26.8–660.3 | 0% | 0 | 150 | 128 |
| jpeg-400-jpeg | 125 | xtandard | 124.9 | 25.1 | 103.8 | 142.5 | 40.2–214.4 | 0% | 0 | 147 | 260 |
| jpeg-400-jpeg | 250 | imgproxy | 228.2 | 1133.9 | 1315.3 | 1348.1 | 799.8–1696.9 | 0% | 270 | 304 | 156 |
| jpeg-400-jpeg | 250 | xtandard | 249.8 | 16.9 | 60.5 | 87.2 | 28.6–802.4 | 0% (429: 103) | 0 | 285 | 353 |
| jpeg-400-webp | 50 | imgproxy | 50 | 21.7 | 25.1 | 29.3 | 29.2–33.1 | 0% | 0 | 96 | 89 |
| jpeg-400-webp | 50 | xtandard | 50 | 20.3 | 23.9 | 26.6 | 24.4–26.8 | 0% | 0 | 88 | 230 |
| jpeg-400-webp | 100 | imgproxy | 99.9 | 23.4 | 30.6 | 48.9 | 31.3–96.4 | 0% | 0 | 192 | 120 |
| jpeg-400-webp | 100 | xtandard | 99.9 | 20.7 | 24.8 | 28.4 | 27.8–444.5 | 0% | 0 | 173 | 239 |
| jpeg-400-webp | 190 | imgproxy | 184.5 | 272.9 | 449.1 | 470.8 | 340.8–690.2 | 0% | 0 | 394 | 136 |
| jpeg-400-webp | 190 | xtandard | 189.8 | 26.5 | 44.1 | 59.5 | 50.4–62.5 | 0% | 0 | 353 | 276 |
| png-alpha-400-webp | 15 | imgproxy | 15 | 58.5 | 66.4 | 79.8 | 68.6–431 | 0% | 0 | 80 | 160 |
| png-alpha-400-webp | 15 | xtandard | 15 | 58.8 | 66.9 | 74.6 | 66.7–139.1 | 0% | 0 | 77 | 316 |
| png-alpha-400-webp | 30 | imgproxy | 29.8 | 107.1 | 245.1 | 351.8 | 264.6–804.1 | 0% | 0 | 171 | 182 |
| png-alpha-400-webp | 30 | xtandard | 29.9 | 66.1 | 275.9 | 476.7 | 186.9–1188.5 | 0% | 1 | 171 | 524 |
| png-alpha-400-webp | 60 | imgproxy | 38.3 | 3837.3 | 4351 | 4447.4 | 4375–7209 | 0% | 642 | 264 | 263 |
| png-alpha-400-webp | 60 | xtandard | 53.3 | 849.1 | 1647.4 | 1842.4 | 1318.8–5542.6 | 0% (429: 234, 503: 33) | 133 | 324 | 665 |

### Throughput ceiling (closed loop, 16 concurrent requests)

| Case | Server | Throughput (req/s) | p50 (ms) | p95 (ms) | p99 (ms) | Failed | CPU avg (%) | Mem max (MiB) |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| gif-anim-240-webp | imgproxy | 21.2 | 647.4 | 1251.9 | 1525.4 | 0% | 211 | 149 |
| gif-anim-240-webp | xtandard | 24.2 | 612.8 | 996.9 | 1289.7 | 0% | 237 | 749 |
| info | xtandard | 5164.3 | 2.8 | 5.9 | 8.3 | 0% | 174 | 236 |
| jpeg-1200-fill-jpeg | imgproxy | 57.9 | 232 | 546.4 | 713.9 | 0% | 271 | 346 |
| jpeg-1200-fill-jpeg | xtandard | 87 | 165 | 323.7 | 490.2 | 0% | 241 | 692 |
| jpeg-400-avif | imgproxy | 182.6 | 85.2 | 111.6 | 129.9 | 0% | 320 | 154 |
| jpeg-400-avif | xtandard | 236.9 | 65.3 | 91.8 | 108 | 0% | 408 | 482 |
| jpeg-400-jpeg | imgproxy | 218.5 | 70.2 | 101.8 | 117.8 | 0% | 288 | 132 |
| jpeg-400-jpeg | xtandard | 285.5 | 48.8 | 76.6 | 93.7 | 0% | 333 | 309 |
| jpeg-400-webp | imgproxy | 178.8 | 88 | 110.5 | 123.7 | 0% | 391 | 127 |
| jpeg-400-webp | xtandard | 224.3 | 69.8 | 91 | 103.8 | 0% | 464 | 298 |
| png-alpha-400-webp | imgproxy | 70.4 | 221.8 | 332 | 373.4 | 0% | 453 | 222 |
| png-alpha-400-webp | xtandard | 60.8 | 235.3 | 437.7 | 540.7 | 0% | 481 | 581 |
