/** Source schemes the server resolves. */
export type SourceScheme = "http" | "https" | "s3" | "gs" | "abs" | "local" | "ftp" | "sftp" | "xi";
/**
 * A named source reference, `xi://<name>/<key>` (WP-95). The key may be a
 * relative key or, for an http source, an absolute http(s) URL.
 *
 * ```ts
 * xiSource("photos", "2026/cat.jpg");                 // xi://photos/2026/cat.jpg
 * xiSource("web", "https://cdn.example.com/a.jpg");   // xi://web/https://cdn.example.com/a.jpg
 * ```
 */
export declare function xiSource(name: string, key: string): string;
/**
 * A source reference for a scheme: the bucket, container, host or source
 * name, then the key. `local` takes only a path (`local:///<path>`).
 *
 * ```ts
 * sourceUrl("s3", "photos", "cat.jpg");   // s3://photos/cat.jpg
 * sourceUrl("local", "", "img/cat.jpg");  // local:///img/cat.jpg
 * ```
 */
export declare function sourceUrl(scheme: SourceScheme, authority: string, key: string): string;
