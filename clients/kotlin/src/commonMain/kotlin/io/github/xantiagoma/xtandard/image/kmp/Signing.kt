package io.github.xantiagoma.xtandard.image.kmp

/**
 * URL signing, byte-compatible with the server (`internal/security/sign`).
 *
 * - imgproxy: `base64url(HMAC-SHA256(key, salt || path)[:size])`, the signature segment of
 *   `/{signature}/{options}/{source}`; `/video` URLs sign `"/video" + path`.
 * - `xi_sig`: the same construction over `path || "?" || query-without-xi_sig`, appended as the last query
 *   parameter.
 */
public object Signing {
    private val absolute = Regex("^[a-zA-Z][a-zA-Z0-9+.-]*://")

    /**
     * Signs a message with the salted HMAC-SHA256 construction of the imgproxy and `xi_sig` schemes. Like the
     * server, a message without a leading `/` is signed as if it had one.
     *
     * @param message the message (an imgproxy path, or an `xi_sig` message)
     * @param key the signing key
     * @return the unpadded base64url signature
     */
    public fun sign(
        message: String,
        key: SigningKey,
    ): String {
        val m = if (message.startsWith("/")) message else "/$message"
        val digest = hmacSha256(key.key, key.salt + Text.utf8(m))
        return Text.b64url(digest.copyOf(key.size))
    }

    /**
     * Signs an imgproxy path (everything after the signature segment).
     *
     * @param path the path, with or without its leading `/`
     * @param key the signing key
     * @return `"/" + signature + path`
     */
    public fun signImgproxyPath(
        path: String,
        key: SigningKey,
    ): String {
        val p = if (path.startsWith("/")) path else "/$path"
        return "/" + sign(p, key) + p
    }

    /**
     * A raw query split into its `xi_sig` value and the message `xi_sig` signs.
     *
     * @property signature the `xi_sig` value, or null when there is none
     * @property message the path, then `?` and the remaining non-empty parameters in their original order and
     *   encoding (no `?` when none remain)
     */
    public data class XiMessage(
        val signature: String?,
        val message: String,
    )

    /**
     * Splits a raw query into its `xi_sig` value and the message `xi_sig` signs.
     *
     * @param path the escaped path the dialect sees, after its mount prefix
     * @param rawQuery the query, without `?`
     * @return the signature and the message
     * @throws UrlBuildException [ErrorCode.INVALID_REQUEST] when the query has two `xi_sig` (the server answers 403)
     */
    public fun xiMessage(
        path: String,
        rawQuery: String,
    ): XiMessage {
        var sig: String? = null
        val kept = mutableListOf<String>()
        for (part in rawQuery.split('&')) {
            if (part.isEmpty()) continue
            val eq = part.indexOf('=')
            val k = if (eq < 0) part else part.substring(0, eq)
            if (k == Constants.XI_SIG_PARAM) {
                if (sig != null) {
                    fail(ErrorCode.INVALID_REQUEST, "query has more than one ${Constants.XI_SIG_PARAM} parameter")
                }
                sig = if (eq < 0) "" else part.substring(eq + 1)
                continue
            }
            kept += part
        }
        return XiMessage(sig, if (kept.isEmpty()) path else path + "?" + kept.joinToString("&"))
    }

    /**
     * Re-signs a raw query: drops any `xi_sig` and appends a fresh one for the path.
     *
     * @param path the escaped path the dialect sees, after its mount prefix
     * @param rawQuery the query, with or without its leading `?`
     * @param key the signing key
     * @return the query (without `?`) ending in `xi_sig=...`
     */
    public fun signXiQuery(
        path: String,
        rawQuery: String,
        key: SigningKey,
    ): String {
        val q = rawQuery.removePrefix("?")
        val msg = xiMessage(path, q).message
        val sig = sign(msg, key)
        val rest = msg.substring(path.length)
        return if (rest.isEmpty()) "${Constants.XI_SIG_PARAM}=$sig" else rest.substring(1) + "&${Constants.XI_SIG_PARAM}=$sig"
    }

    /**
     * Signs a URL (absolute, or a path with an optional query) with `xi_sig`.
     *
     * @param url the URL; a fragment is dropped
     * @param key the signing key
     * @param mount the dialect's mount prefix, which the signed path excludes (`"/q"` for the query dialect); `""`
     *   for a dialect mounted at the root
     * @return the signed URL
     * @throws UrlBuildException [ErrorCode.INVALID_REQUEST] when the path is not under the mount or the query has
     *   two `xi_sig`
     */
    public fun signXiUrl(
        url: String,
        key: SigningKey,
        mount: String = "",
    ): String {
        val noHash = url.substringBefore('#')
        val qm = noHash.indexOf('?')
        val head = if (qm < 0) noHash else noHash.substring(0, qm)
        val query = if (qm < 0) "" else noHash.substring(qm + 1)
        val pathStart = if (absolute.containsMatchIn(head)) head.indexOf('/', head.indexOf("//") + 2) else 0
        val origin = if (pathStart < 0) head else head.substring(0, pathStart)
        var path = if (pathStart < 0) "/" else head.substring(pathStart)
        val m = stripTrailingSlashes(mount)
        if (m.isNotEmpty()) {
            if (path != m && !path.startsWith("$m/")) fail(ErrorCode.INVALID_REQUEST, "path $path is not under the mount $m")
            path = path.substring(m.length).ifEmpty { "/" }
        }
        return origin + m + path + "?" + signXiQuery(path, query, key)
    }

    internal fun stripTrailingSlashes(s: String): String = s.trimEnd('/')
}
