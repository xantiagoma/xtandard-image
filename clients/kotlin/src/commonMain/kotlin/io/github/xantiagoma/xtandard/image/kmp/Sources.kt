package io.github.xantiagoma.xtandard.image.kmp

/**
 * Source references: the string a URL carries as its source. These helpers only join strings; the URL builders
 * escape or encode the result.
 *
 * ```
 * https://host/path         an http source
 * s3://bucket/key           gs://bucket/key   abs://container/key
 * local:///path             ftp://host/path   sftp://host/path
 * xi://<source>/<key>       a named source (XI_SOURCES)
 * ```
 */
public object Sources {
    private val name = Regex(Constants.XI_NAME_PATTERN)

    /**
     * A named source reference, `xi://<name>/<key>`. The key may be relative or, for an http source, an absolute
     * URL: `xiSource("photos", "2026/cat.jpg")` is `xi://photos/2026/cat.jpg`.
     *
     * @param name the source name (XI_SOURCES)
     * @param key the key inside the source; leading slashes are dropped
     * @return the source reference
     * @throws UrlBuildException [ErrorCode.INVALID_NAME] for a bad name
     */
    public fun xiSource(
        name: String,
        key: String,
    ): String {
        if (!this.name.matches(name)) fail(ErrorCode.INVALID_NAME, "invalid source name \"$name\"")
        return "xi://$name/" + key.trimStart('/')
    }

    /**
     * A source reference for a scheme: the bucket, container, host or source name, then the key. `local` takes no
     * authority (`local:///<path>`).
     *
     * @param scheme the scheme
     * @param authority the bucket, container, host[:port] or source name; empty for `local`
     * @param key the key or path; leading slashes are dropped
     * @return the source reference
     * @throws UrlBuildException [ErrorCode.INVALID_NAME] when the authority is missing, has `/`, `?`, `#` or
     *   whitespace, or is given for `local`
     */
    public fun sourceUrl(
        scheme: SourceScheme,
        authority: String,
        key: String,
    ): String = sourceUrl(scheme.value, authority, key)

    /**
     * A source reference for a scheme given by name (`s3`, `https`, `xi`, ...).
     *
     * @param scheme the scheme
     * @param authority the bucket, container, host[:port] or source name; empty for `local`
     * @param key the key or path; leading slashes are dropped
     * @return the source reference
     * @throws UrlBuildException [ErrorCode.INVALID_NAME] when the authority is missing, has `/`, `?`, `#` or
     *   whitespace, or is given for `local`
     */
    public fun sourceUrl(
        scheme: String,
        authority: String,
        key: String,
    ): String {
        if (scheme == "xi") return xiSource(authority, key)
        if (scheme == "local") {
            if (authority.isNotEmpty()) fail(ErrorCode.INVALID_NAME, "local sources have no authority: local:///<path>")
            return "local:///" + key.trimStart('/')
        }
        if (authority.isEmpty() || authority.any { it == '/' || it == '?' || it == '#' || Text.isJsWhitespace(it) }) {
            fail(ErrorCode.INVALID_NAME, "invalid $scheme authority \"$authority\"")
        }
        return "$scheme://$authority/" + key.trimStart('/')
    }

    /**
     * Percent-escapes a source for a `plain/` URL; the server path-unescapes it. ASCII letters, digits, RFC 3986
     * unreserved characters, the sub-delims, `:` and `/` are kept; everything else (`%`, `@`, `?`, `#`, spaces,
     * non-ASCII) is percent-encoded as UTF-8.
     *
     * @param source the source
     * @return the escaped source
     */
    public fun escapePlainSource(source: String): String = Text.escapePlain(source)

    /**
     * Percent-escapes a query key or value, keeping `:`, `/` and `,` readable.
     *
     * @param value the key or value
     * @return the escaped string
     */
    public fun escapeQueryValue(value: String): String = Text.escapeQuery(value)
}
