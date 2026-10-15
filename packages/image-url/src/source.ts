// Source references (docs/URL-DIALECTS.md §3.1): the string a URL carries
// as its source, whatever the dialect. These helpers only join strings;
// the URL builders escape or encode the result.
//
//   https://host/path         an http source (the first whose allowlist matches)
//   s3://bucket/key           gs://bucket/key   abs://container/key
//   local:///path             ftp://host/path   sftp://host/path
//   xi://<source>/<key>       a named source (XI_SOURCES): the key is relative
//                             to it; an http source without base_url takes an
//                             absolute URL as its key

/** Source schemes the server resolves. */
export type SourceScheme = "http" | "https" | "s3" | "gs" | "abs" | "local" | "ftp" | "sftp" | "xi";

function trimKey(key: string): string {
  return key.replace(/^\/+/, "");
}

/**
 * A named source reference, `xi://<name>/<key>` (WP-95). The key may be a
 * relative key or, for an http source, an absolute http(s) URL.
 *
 * ```ts
 * xiSource("photos", "2026/cat.jpg");                 // xi://photos/2026/cat.jpg
 * xiSource("web", "https://cdn.example.com/a.jpg");   // xi://web/https://cdn.example.com/a.jpg
 * ```
 */
export function xiSource(name: string, key: string): string {
  if (!/^[A-Za-z0-9][A-Za-z0-9_.-]*$/.test(name)) throw new TypeError(`invalid source name ${JSON.stringify(name)}`);
  return `xi://${name}/${trimKey(key)}`;
}

/**
 * A source reference for a scheme: the bucket, container, host or source
 * name, then the key. `local` takes only a path (`local:///<path>`).
 *
 * ```ts
 * sourceUrl("s3", "photos", "cat.jpg");   // s3://photos/cat.jpg
 * sourceUrl("local", "", "img/cat.jpg");  // local:///img/cat.jpg
 * ```
 */
export function sourceUrl(scheme: SourceScheme, authority: string, key: string): string {
  if (scheme === "xi") return xiSource(authority, key);
  if (scheme === "local") {
    if (authority !== "") throw new TypeError("local sources have no authority: local:///<path>");
    return `local:///${trimKey(key)}`;
  }
  if (authority === "" || /[/?#\s]/.test(authority)) throw new TypeError(`invalid ${scheme} authority ${JSON.stringify(authority)}`);
  return `${scheme}://${authority}/${trimKey(key)}`;
}
