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

## Implementation notes (WP-53/54/55, 2026-09-26)

- **Disk**: one file per entry under two levels of hex shards of the SHA-256 of namespace+key; a single framed file (`"XIC1"`, JSON metadata length, `cache.Meta`, body) rather than a header file plus a body file, so an entry appears and disappears with one rename or unlink. No fsync: the cache is disposable and a torn file fails its length or CRC-32C check, is removed and counted, like any corrupt entry. The size index is rebuilt at start from a directory scan (LRU order = mtime, i.e. write time; hits are not persisted to spare a metadata write per hit). A janitor goroutine (Ticker, synctest-deterministic) evicts down to 90 % of the bound when a write goes over it and sweeps expired entries each minute. The directory must be a writable volume dedicated to one process.
- **S3**: reuses a named s3 source's client (`s3src.NewClient`), so credentials, endpoint and assumed roles are configured once. The entry metadata travels in `x-amz-meta-xi-meta` and expiry is checked from it on read, because lifecycle rules have day granularity and are not available on every S3-compatible store; a lifecycle rule on the prefix is only needed to reclaim space.
- **Redis**: `github.com/redis/go-redis/v9` (BSD-2-Clause), the official client, chosen over `rueidis` (Apache-2.0, client-side caching we do not need) for its smaller API surface and `ParseURL` (TLS, auth, pool options from one secret URL). Values are the same frame as the disk file; `SET … PX` lets the server expire entries, values above 1 MiB are skipped (large results belong on disk or in S3, not in Redis memory). Integration tests run against **Valkey** (BSD-3-Clause), because Redis 7.4+ is RSALv2/SSPL and Redis 8 adds AGPL, which ADR 0006 forbids even as a test service; only GET, SET PX and DEL are used, so any RESP server works.
- **Origin store**: `XI_CACHE_ORIGIN_STORES` backs fetch's in-process LRU with the same backends under `orig/` (`fetch.WithStore`), sharing originals across replicas and restarts for `XI_CACHE_ORIGIN_STORE_TTL` without revalidation. A backend listed for both caches is one instance (one disk budget, one connection pool).
