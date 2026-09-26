# ADR 0004: Caching strategy

- Status: accepted
- Date: 2026-09-26

## Context

Most deployments sit behind a CDN, so the server should be stateless and emit correct HTTP caching semantics. Some deployments have no CDN or want to spare origins and CPU across replicas. Responsive images request many sizes of one original within milliseconds.

## Decision

- **Stateless by default.** Correct headers always: long `Cache-Control` with `stale-while-revalidate`, strong `ETag` derived from the canonical request, output format and source validator, `Last-Modified`, `Vary: Accept` only when the format was negotiated, `Content-DPR`, `Content-Disposition`; conditional requests answered with 304 before processing when possible; short or no caching for errors.
- **Cache keys come from the canonical model**, not the URL, so equivalent URLs across dialects share entries.
- **Original-fetch coalescing always on**: singleflight per source object plus a small in-memory byte-bounded LRU of originals (60 s) so bursts of sizes download once.
- **Optional result cache** behind a `cache.Store` interface with memory, disk, S3-compatible and Redis backends, composable as tiers, written asynchronously with drop-on-overload.
- libvips' own operation cache is disabled.

## Consequences

- Zero-config deployments work well behind a CDN; cache backends are opt-in and cannot take the service down (errors degrade to misses).
- ETag stability depends on `CanonicalKey` stability; changing it is a versioned event (`v1` prefix) that invalidates caches.
- No cross-replica invalidation API in scope; purge is the CDN's job, `cachebuster`/`XI_CACHE_ETAG_BUSTER` handle global busting.

## Implementation notes (WP-52, 2026-09-26)

- The result key holds the resolved source identity, not its validators, so a hit never contacts the origin (sparing origins across replicas, like imagor's result storage); freshness is bounded by `XI_CACHE_RESULT_TTL` (24 h default) and busted by `cachebuster` / `XI_CACHE_ETAG_BUSTER`. Negotiated formats are keyed by the Accept weights the negotiation reads (`negotiate.AcceptList.Key`), so no source metadata is needed before the lookup.
- The memory tier is an in-package byte-bounded LRU (`container/list`) instead of otter's W-TinyLFU: no new dependency, refcounted bodies (a hit shares the stored vips blob without copying), and deterministic tests under `testing/synctest`. Revisit with benchmarks if hit ratios under scan-heavy traffic call for admission control.
- Async writes are bounded by an `x/sync/semaphore` (`XI_CACHE_RESULT_WRITE_QUEUE`), dropped when full (`xi_cache_writes_dropped_total`).
