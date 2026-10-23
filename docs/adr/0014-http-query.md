# ADR 0014: HTTP QUERY on the query dialect

- Status: accepted (WP-147)
- Date: 2026-10-23

## Context

Query dialect requests carry everything in the URL. Long option chains, `wmt` watermark text, `enc/`-style tokens and video overlays make URLs long, and some never fit through proxies' URL limits (xtandard-image itself refuses request targets over 8 KiB). RFC 10008 (Proposed Standard) defines `QUERY`: a safe, idempotent method whose query is in the request content, typed by its media type, with cacheable responses keyed on the content. The owner asked for it in the server, on by default, and as an opt-in in the official clients.

## Decision

1. **Where.** `QUERY` is accepted on the query dialect's endpoints only: `/q/` (native), `/q/video` and the imaginary endpoints `/q/<endpoint>`, on every mount of the dialect (prefix, host bindings, root `?url=` rule). The imaginary endpoints are included because they are the same parser behind the same mount and the same signature; `/q/pipeline`'s JSON `operations` is one of the parameters that most needs the room. Path dialects (imgproxy, thumbor, …) put their options in the path, which QUERY does not move, and the other query-string dialects mirror a vendor's URL API that has no QUERY form; both answer 405 as before.
2. **How.** The ingress turns a QUERY request into its equivalent GET request (same path, the content's query as the URL query, no body) and dispatches that. Nothing after the ingress knows the method: one parser, one signature check, one unsigned policy, one set of limits, one result cache key, and `HEAD`/`GET` handling unchanged. Go 1.27 has no `http.MethodQuery`: `server.MethodQuery` is defined locally.
3. **Media types.** `application/x-www-form-urlencoded` is the GET query string verbatim. `application/json` is an object of the same parameters (typed values, document order) standing for a canonical query, which escapes like the official clients. Anything else is 415 with `Accept-Query`; malformed content is 400; content over `XI_SERVER_QUERY_MAX_BODY_BYTES` (64 KiB) is 413. A request URL with its own query is a 400, not merged: two places for one parameter would need precedence rules and would make the signed message ambiguous.
4. **Signing.** `xi_sig` (and imaginary's `sign`) stay parameters, in the content, over the same message as the GET URL (path after the mount, `?`, the query without the signature). The method is deliberately not covered: QUERY and GET are both safe and produce the same representation, so binding it would protect nothing, while not binding it lets clients sign once and send either, and lets the server answer with a `Location` that works without minting a signature. A header (`X-Xi-Signature`) was rejected as a second, method-specific place for the signature.
5. **Caching and conditional requests.** The result cache key is derived from the parsed request, so GET and QUERY share entries and ETags; existing GET keys do not change (pinned by `TestResultKeysPinned`). `If-None-Match` works unchanged; responses add `Vary: Content-Type`.
6. **Location, not Content-Location.** A 2xx carries `Location` with the equivalent GET URL when it fits `XI_SERVER_QUERY_LOCATION_MAX_LENGTH` (2048 by default, at most the 8 KiB URL limit) and has no `pdf_password` (kept out of URLs, as out of logs). It only echoes the client's parameters and signature, so it grants nothing under `restricted` or `deny` that the request did not have. `Content-Location` would claim a resource holding this exact result; the result depends on `Accept` and client hints, so it is not sent.
7. **Toggle and compatibility.** `XI_SERVER_QUERY_METHOD` (default `true`); `false` restores the previous behaviour exactly (QUERY is 405 with `Allow: GET, HEAD`, no `Accept-Query`). With it on, GET and HEAD responses change only by adding `Accept-Query` on the query dialect's endpoints, and their 405 answers list `QUERY` in `Allow`.
8. **Clients.** Off by default (`queryMethod` / `query_method`), because many CDNs and proxies neither cache nor forward QUERY. URL builders are unchanged; a client splits the built URL into the QUERY request (`queryMethodRequest`), checked by the `query.query_request` conformance cases against the server's content parser and signature.
9. **OpenAPI.** OpenAPI 3.1 has no `query` operation (3.2 adds one); the three query dialect operations carry an `x-xi-query-method` extension (media types, `QueryContent` schema, extra headers and statuses) instead of moving the document to 3.2.

## Consequences

- Requests whose options do not fit in a URL work against xtandard-image directly; CDN caching of them depends on the CDN.
- Signed QUERY content and signed GET URLs are interchangeable; anyone holding one holds the other (as before: the content was a URL query).
- No new dependency: `encoding/json/jsontext` (standard library) reads JSON content in order and rejects duplicate names.
