package io.github.xantiagoma.xtandard.image.kmp

/**
 * Builds query dialect URLs (the server needs `XI_DIALECTS_QUERY_ENABLED=true`), mounted at `/q`.
 *
 * ```
 * {base}{mount}/?url=<source>&<native key>=...&o=<imgproxy options>[&xi_sig=...]
 * {base}{mount}/video?url=<source>&<video option>=<arg,arg>...[&xi_sig=...]
 * {base}{mount}/<imaginary endpoint>?url=<source>&<param>=...[&xi_sig=...]
 * ```
 *
 * With a signing key every URL ends in an `xi_sig` over the path after the mount and the query:
 *
 * ```
 * val q = QueryUrlBuilder(baseUrl = "https://img.example.com", signing = key)
 * q.url("s3://bucket/cat.jpg", queryParams { w(300); f("webp") }, processingOptions { bl(2) })
 * // https://img.example.com/q/?url=s3://bucket/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=...
 * ```
 *
 * Immutable and thread-safe.
 *
 * @param baseUrl the server origin (and any path before the mount); trailing slashes are dropped
 * @param mount the query dialect's mount prefix (XI_DIALECTS_QUERY_PREFIX); `/q` by default
 * @property signing signs with `xi_sig` (the imgproxy key and salt); null for unsigned URLs
 * @property encryption the key for PDF passwords (XI_ENCRYPTION_KEYS)
 * @property separator the argument separator inside `o=`: one character, not `/`, `-`, `%`, `&` or `=`
 * @property optionNames how option names are written; short names by default
 */
public class QueryUrlBuilder(
    baseUrl: String = "",
    mount: String = Constants.QUERY_MOUNT,
    public val signing: SigningKey? = null,
    public val encryption: EncryptionKey? = null,
    public val separator: String = Constants.DEFAULT_SEPARATOR,
    public val optionNames: OptionNameStyle = OptionNameStyle.SHORT,
) {
    /** The base URL without trailing slashes. */
    public val baseUrl: String = Signing.stripTrailingSlashes(baseUrl)

    /** The mount without trailing slashes. */
    public val mount: String = Signing.stripTrailingSlashes(mount)

    // ---- object form ---------------------------------------------------------------------------

    /**
     * A native `/q/?url=...` image URL: native keys, then imgproxy options as `o=`, `format=` and `pdp=`.
     *
     * @param source the source URL
     * @param params the native keys (`w`, `fit`, `g`, `f`, ...); null for none
     * @param options imgproxy processing options, written into `o=`; null for none
     * @param extension the output format (`format=`); null for none
     * @param pdfPassword the password of a password-protected PDF source (needs both keys)
     * @return the URL
     * @throws UrlBuildException when an option, the source, the extension, a value or a key is invalid
     */
    public fun url(
        source: String,
        params: QueryParams? = null,
        options: ProcessingOptions? = null,
        extension: String? = null,
        pdfPassword: String? = null,
    ): String = image(source, params, listOfNotNull(options?.entries), extension, pdfPassword)

    /**
     * A native `/q/?url=...` image URL with chained pipelines in `o=`.
     *
     * @param source the source URL
     * @param params the native keys; null for none
     * @param pipelines imgproxy processing options per pipeline
     * @param extension the output format (`format=`); null for none
     * @param pdfPassword the password of a password-protected PDF source (needs both keys)
     * @return the URL
     * @throws UrlBuildException when an option, the source, the extension, a value or a key is invalid
     */
    public fun url(
        source: String,
        params: QueryParams?,
        pipelines: List<ProcessingOptions>,
        extension: String? = null,
        pdfPassword: String? = null,
    ): String = image(source, params, pipelines.map { it.entries }, extension, pdfPassword)

    /**
     * A `/q/video` URL: every video option is a key, its arguments joined by `,`; `crop` and `flip` are written `c`
     * and `fl`.
     *
     * @param source the source video
     * @param options the `/video` options; null for none
     * @param extension the output container (`format=`); null for the server's default
     * @return the URL
     * @throws UrlBuildException when an option, the source or the extension is invalid
     */
    public fun videoUrl(
        source: String,
        options: VideoOptions? = null,
        extension: String? = null,
    ): String = video(source, options?.entries.orEmpty(), extension)

    /**
     * A `/q/video` URL from several option lists, written in order (a key may repeat; later wins argument by
     * argument).
     *
     * @param source the source video
     * @param options the `/video` option lists
     * @param extension the output container; null for the server's default
     * @return the URL
     * @throws UrlBuildException when an option, the source or the extension is invalid
     */
    public fun videoUrl(
        source: String,
        options: List<VideoOptions>,
        extension: String? = null,
    ): String = video(source, options.flatMap { it.entries }, extension)

    /**
     * A `/q/video` HLS or DASH manifest URL (`format=m3u8|mpd`).
     *
     * @param source the source video
     * @param stream the protocol, renditions, segment settings and other options
     * @return the URL
     * @throws UrlBuildException as [ImgproxyUrlBuilder.streamUrl], and [ErrorCode.INVALID_REQUEST] for a source
     *   encoding (query URLs carry the source as `url=`)
     */
    public fun streamUrl(
        source: String,
        stream: StreamParams,
    ): String {
        if (stream.sourceEncoding != null) fail(ErrorCode.INVALID_REQUEST, "query URLs carry the source as url=, with no source encoding")
        val parts = StreamParts.of(stream)
        return video(source, parts.entries, parts.container)
    }

    /**
     * An imaginary endpoint URL (`/q/resize?url=...&width=300`).
     *
     * @param endpoint the endpoint
     * @param source the source URL
     * @param params the imaginary parameters; null for none
     * @return the URL
     * @throws UrlBuildException when the source or a value is invalid
     */
    public fun imaginaryUrl(
        endpoint: ImaginaryEndpoint,
        source: String,
        params: ImaginaryParams? = null,
    ): String = imaginaryUrl(endpoint.value, source, params)

    /**
     * An imaginary endpoint URL for an endpoint given by name.
     *
     * @param endpoint lower-case letters, not `video`
     * @param source the source URL
     * @param params the imaginary parameters; null for none
     * @return the URL
     * @throws UrlBuildException [ErrorCode.INVALID_ENDPOINT] for a bad endpoint; or an invalid source or value
     */
    public fun imaginaryUrl(
        endpoint: String,
        source: String,
        params: ImaginaryParams? = null,
    ): String {
        if (!endpointPattern.matches(endpoint) || endpoint == "video") {
            fail(ErrorCode.INVALID_ENDPOINT, "invalid imaginary endpoint \"$endpoint\"")
        }
        val pairs = mutableListOf("url" to Text.checkText(source, ErrorCode.INVALID_SOURCE, "source"))
        if (params != null) pairs += params.formatted()
        return finish("/$endpoint", pairs)
    }

    // ---- fluent form ---------------------------------------------------------------------------

    /**
     * Starts a fluent `/q/?url=...` URL: the option methods write `o=`, [QueryUrlChain.param] a native key.
     *
     * @param source the source URL
     * @return the chain
     */
    public fun image(source: String): QueryUrlChain = QueryUrlChain(this, source)

    /**
     * A `/q/?url=...` URL from a DSL block: `q.image(src) { param("w", 300); blur(2) }`.
     *
     * @param source the source URL
     * @param block adds keys, options and settings to the chain
     * @return the URL
     */
    public fun image(
        source: String,
        block: QueryUrlChain.() -> Unit,
    ): String = image(source).apply(block).build()

    /**
     * Starts a fluent `/q/video` URL.
     *
     * @param source the source video
     * @return the chain
     */
    public fun video(source: String): QueryVideoUrlChain = QueryVideoUrlChain(this, source)

    /**
     * A `/q/video` URL from a DSL block.
     *
     * @param source the source video
     * @param block adds options and settings to the chain
     * @return the URL
     */
    public fun video(
        source: String,
        block: QueryVideoUrlChain.() -> Unit,
    ): String = video(source).apply(block).build()

    // ---- building ------------------------------------------------------------------------------

    internal fun image(
        source: String,
        params: QueryParams?,
        pipelines: List<List<OptionEntry>>,
        ext: String?,
        pdfPassword: String?,
    ): String {
        val sep = Text.checkSeparator(separator, Constants.FORBIDDEN_QUERY_SEPARATORS)
        val pairs = mutableListOf("url" to Text.checkText(source, ErrorCode.INVALID_SOURCE, "source"))
        if (params != null) pairs += params.formatted()
        val written =
            pipelines.filter { it.isNotEmpty() }.map { p -> p.joinToString("/") { Catalogue.PROCESSING.format(it, sep, optionNames) } }
        if (written.isNotEmpty()) pairs += "o" to written.joinToString("/${Constants.PIPELINE_SEPARATOR}/")
        if (!ext.isNullOrEmpty()) pairs += "format" to Text.checkExtension(ext)
        if (pdfPassword != null) {
            val token = ImgproxyUrlBuilder.passwordToken(encryption, signing, pdfPassword)
            pairs += (if (optionNames == OptionNameStyle.CANONICAL) Constants.PDF_PASSWORD_NAME else Constants.PDF_PASSWORD_SHORT) to token
        }
        return finish("/", pairs)
    }

    internal fun video(
        source: String,
        entries: List<OptionEntry>,
        ext: String?,
    ): String {
        val pairs = mutableListOf("url" to Text.checkText(source, ErrorCode.INVALID_SOURCE, "source"))
        for (e in entries) {
            var key = Catalogue.VIDEO.written(e.name, optionNames)
            if (e.args.isEmpty()) fail(ErrorCode.INVALID_ARGUMENT, "option ${e.name}: at least one argument is required")
            key = Constants.NATIVE_VIDEO_KEYS[key] ?: key
            val values =
                e.args.mapIndexed { i, a ->
                    val where = Catalogue.where(e, i)
                    val s = Catalogue.formatArg(a, ",", where)
                    if (':' in s) fail(ErrorCode.INVALID_ARGUMENT, "$where: \"$s\" contains ':'")
                    s
                }
            pairs += key to values.joinToString(",")
        }
        if (!ext.isNullOrEmpty()) pairs += "format" to Text.checkExtension(ext)
        return finish("/video", pairs)
    }

    private fun finish(
        path: String,
        pairs: List<Pair<String, String>>,
    ): String {
        var query = pairs.joinToString("&") { (k, v) -> Text.escapeQuery(k) + "=" + Text.escapeQuery(v) }
        signing?.let { query = Signing.signXiQuery(path, query, it) }
        return "$baseUrl$mount$path?$query"
    }

    override fun toString(): String =
        "QueryUrlBuilder(baseUrl=$baseUrl, mount=$mount, signed=${signing != null}, separator=$separator, optionNames=$optionNames)"

    private companion object {
        val endpointPattern = Regex("[a-z]+")
    }
}
