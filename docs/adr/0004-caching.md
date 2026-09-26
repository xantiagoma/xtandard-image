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
