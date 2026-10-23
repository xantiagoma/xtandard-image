package io.github.xantiagoma.xtandard.image.kmp

/**
 * Builds imgproxy-dialect URLs for an xtandard-image (or imgproxy) server.
 *
 * ```
 * {base}[/info]/{signature}/{option}/...[/-/{option}/...]/{source}
 * {base}/video/{signature}/{video option}/.../{source}      (signs "/video" + path)
 * ```
 *
 * Sources are base64url (default), `plain/` (percent-escaped, `@ext`) or `enc/` (AES-CBC); base64 and encrypted
 * sources take `.ext`.
 *
 * ```
 * val img = ImgproxyUrlBuilder(baseUrl = "https://img.example.com", signing = SigningKey.of(keyHex, saltHex))
 * img.url("s3://bucket/cat.jpg", processingOptions { resize("fill", 300, 400); quality(80) }, extension = "webp")
 * img.image("s3://bucket/cat.jpg") { resize("fill", 300, 400); quality(80); extension("webp") }
 * img.videoUrl("s3://bucket/talk.mov", videoOptions { start(12); duration(8) }, extension = "webm")
 * img.streamUrl("s3://bucket/talk.mov", StreamParams.hls(renditions = listOf(360, 720)))
 * ```
 *
 * Immutable and thread-safe.
 *
 * @param baseUrl prepended to every path: the server origin and the dialect's mount prefix, if any; trailing
 *   slashes are dropped. `""` (the default) gives root-relative paths.
 * @property signing signs URLs with the imgproxy scheme; null (the default) writes [unsignedSignature]
 * @property unsignedSignature the signature segment of unsigned URLs: `insecure` (default), `unsafe` or `_`
 * @property encryption the key for encrypted sources and PDF passwords
 * @property sourceEncoding the default source encoding: base64 (default), plain or encrypted
 * @property splitSource splits base64 and encrypted sources into `/`-separated chunks of this many characters; 0
 *   (default) does not split
 * @property separator the argument separator (XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR), one character, not `/`,
 *   `-` or `%`; checked when a URL is built
 * @property optionNames how option names are written; short names by default
 */
public class ImgproxyUrlBuilder(
    baseUrl: String = "",
    public val signing: SigningKey? = null,
    public val unsignedSignature: String = Constants.DEFAULT_UNSIGNED,
    public val encryption: EncryptionKey? = null,
    public val sourceEncoding: SourceEncoding = SourceEncoding.BASE64,
    public val splitSource: Int = 0,
    public val separator: String = Constants.DEFAULT_SEPARATOR,
    public val optionNames: OptionNameStyle = OptionNameStyle.SHORT,
) {
    /** The base URL without trailing slashes. */
    public val baseUrl: String = Signing.stripTrailingSlashes(baseUrl)

    internal enum class Endpoint { PROCESS, INFO, VIDEO }

    // ---- object form ---------------------------------------------------------------------------

    /**
     * A processing URL.
     *
     * @param source the source URL (`https://`, `s3://`, `xi://`, ...)
     * @param options the processing options; null for none
     * @param extension the output format (`@webp` / `.webp`); null for none
     * @param sourceEncoding overrides the builder's source encoding
     * @param pdfPassword the password of a password-protected PDF source (needs both keys)
     * @return the URL
     * @throws UrlBuildException when an option, the source, the extension or a key is invalid
     */
    public fun url(
        source: String,
        options: ProcessingOptions? = null,
        extension: String? = null,
        sourceEncoding: SourceEncoding? = null,
        pdfPassword: String? = null,
    ): String = url(source, listOfNotNull(options), extension, sourceEncoding, pdfPassword)

    /**
     * A processing URL with chained pipelines (`/-/`); empty pipelines are dropped.
     *
     * @param source the source URL
     * @param pipelines the options of each pipeline, in order
     * @param extension the output format; null for none
     * @param sourceEncoding overrides the builder's source encoding
     * @param pdfPassword the password of a password-protected PDF source (needs both keys)
     * @return the URL
     * @throws UrlBuildException when an option, the source, the extension or a key is invalid
     */
    public fun url(
        source: String,
        pipelines: List<ProcessingOptions>,
        extension: String? = null,
        sourceEncoding: SourceEncoding? = null,
        pdfPassword: String? = null,
    ): String = build(Endpoint.PROCESS, pipelines.map { it.entries }, source, sourceEncoding, extension, pdfPassword)

    /**
     * An `/info` URL (`/info` takes one pipeline and no extension).
     *
     * @param source the source URL
     * @param options the `/info` options; null for the server's defaults
     * @param sourceEncoding overrides the builder's source encoding
     * @param pdfPassword the password of a password-protected PDF source (needs both keys)
     * @return the URL
     * @throws UrlBuildException when an option, the source or a key is invalid
     */
    public fun infoUrl(
        source: String,
        options: InfoOptions? = null,
        sourceEncoding: SourceEncoding? = null,
        pdfPassword: String? = null,
    ): String = build(Endpoint.INFO, listOfNotNull(options?.entries), source, sourceEncoding, null, pdfPassword)

    /**
     * A `/video` URL (a video transformation, audio or a waveform). `/video` takes one pipeline and no PDF password.
     *
     * @param source the source video
     * @param options the `/video` options; null for none
     * @param extension the output container (`webm`, `mp4`, `mp3`, `png`, ...); null for the server's default
     * @param sourceEncoding overrides the builder's source encoding
     * @return the URL
     * @throws UrlBuildException when an option, the source or the extension is invalid
     */
    public fun videoUrl(
        source: String,
        options: VideoOptions? = null,
        extension: String? = null,
        sourceEncoding: SourceEncoding? = null,
    ): String = build(Endpoint.VIDEO, listOfNotNull(options?.entries), source, sourceEncoding, extension, null)

    /**
     * A `/video` URL with an output container.
     *
     * @param source the source video
     * @param options the `/video` options; null for none
     * @param container the output container
     * @return the URL
     * @throws UrlBuildException when an option or the source is invalid
     */
    public fun videoUrl(
        source: String,
        options: VideoOptions?,
        container: VideoContainer,
    ): String = videoUrl(source, options, container.value)

    /**
     * An HLS or DASH manifest URL (`/video/...@m3u8` or `@mpd`).
     *
     * @param source the source video
     * @param stream the protocol, renditions, segment settings and other options
     * @return the URL
     * @throws UrlBuildException [ErrorCode.INVALID_REQUEST] for an unknown protocol, `ts` on DASH, or `stream`,
     *   `segment_duration`, `segment_format` or `format` among the options; or an invalid option or source
     */
    public fun streamUrl(
        source: String,
        stream: StreamParams,
    ): String {
        val parts = StreamParts.of(stream)
        return build(Endpoint.VIDEO, listOf(parts.entries), source, stream.sourceEncoding, parts.container, null)
    }

    // ---- fluent form ---------------------------------------------------------------------------

    /**
     * Starts a fluent processing URL: `img.image(src).resize("fit", 300, 300).extension("png").build()`.
     *
     * @param source the source URL
     * @return the chain
     */
    public fun image(source: String): ImgproxyUrlChain = ImgproxyUrlChain(this, source)

    /**
     * A processing URL from a DSL block: `img.image(src) { resize("fit", 300, 300); extension("png") }`.
     *
     * @param source the source URL
     * @param block adds options and settings to the chain
     * @return the URL
     * @throws UrlBuildException when an option, the source, the extension or a key is invalid
     */
    public fun image(
        source: String,
        block: ImgproxyUrlChain.() -> Unit,
    ): String = image(source).apply(block).build()

    /**
     * Starts a fluent `/info` URL.
     *
     * @param source the source URL
     * @return the chain
     */
    public fun info(source: String): ImgproxyInfoUrlChain = ImgproxyInfoUrlChain(this, source)

    /**
     * An `/info` URL from a DSL block.
     *
     * @param source the source URL
     * @param block adds options and settings to the chain
     * @return the URL
     */
    public fun info(
        source: String,
        block: ImgproxyInfoUrlChain.() -> Unit,
    ): String = info(source).apply(block).build()

    /**
     * Starts a fluent `/video` URL.
     *
     * @param source the source video
     * @return the chain
     */
    public fun video(source: String): ImgproxyVideoUrlChain = ImgproxyVideoUrlChain(this, source)

    /**
     * A `/video` URL from a DSL block.
     *
     * @param source the source video
     * @param block adds options and settings to the chain
     * @return the URL
     */
    public fun video(
        source: String,
        block: ImgproxyVideoUrlChain.() -> Unit,
    ): String = video(source).apply(block).build()

    // ---- building ------------------------------------------------------------------------------

    internal fun build(
        endpoint: Endpoint,
        pipelines: List<List<OptionEntry>>,
        source: String,
        encoding: SourceEncoding?,
        ext: String?,
        pdfPassword: String?,
    ): String {
        val sep = Text.checkSeparator(separator, Constants.FORBIDDEN_SEPARATORS)
        val cat =
            when (endpoint) {
                Endpoint.INFO -> Catalogue.INFO
                Endpoint.VIDEO -> Catalogue.VIDEO
                Endpoint.PROCESS -> Catalogue.PROCESSING
            }
        val nonEmpty = pipelines.filter { it.isNotEmpty() }
        val what = if (endpoint == Endpoint.INFO) "/info" else "/video"
        if (endpoint != Endpoint.PROCESS && nonEmpty.size > 1) fail(ErrorCode.INVALID_REQUEST, "$what URLs have no chained pipelines")
        if (endpoint == Endpoint.VIDEO && pdfPassword != null) fail(ErrorCode.INVALID_REQUEST, "/video URLs take no pdf_password")
        if (endpoint == Endpoint.INFO && !ext.isNullOrEmpty()) fail(ErrorCode.INVALID_REQUEST, "/info URLs take no extension")
        val segments = mutableListOf<String>()
        if (pdfPassword != null) {
            val token = passwordToken(encryption, signing, pdfPassword)
            val name = if (optionNames == OptionNameStyle.CANONICAL) Constants.PDF_PASSWORD_NAME else Constants.PDF_PASSWORD_SHORT
            segments += name + sep + token
        }
        nonEmpty.forEachIndexed { i, p ->
            if (i > 0) segments += Constants.PIPELINE_SEPARATOR
            for (e in p) segments += cat.format(e, sep, optionNames)
        }
        segments += sourceSegment(source, encoding ?: sourceEncoding, ext)
        val path = "/" + segments.joinToString("/")
        val key = signing
        if (endpoint == Endpoint.VIDEO) {
            val sig = if (key == null) unsignedSignature else Signing.sign("/video$path", key)
            return "$baseUrl/video/$sig$path"
        }
        val signed = if (key == null) "/$unsignedSignature$path" else Signing.signImgproxyPath(path, key)
        return baseUrl + (if (endpoint == Endpoint.INFO) "/info" else "") + signed
    }

    private fun sourceSegment(
        source: String,
        encoding: SourceEncoding,
        ext: String?,
    ): String {
        Text.checkText(source, ErrorCode.INVALID_SOURCE, "source")
        val e = if (ext.isNullOrEmpty()) null else Text.checkExtension(ext)
        return when (encoding) {
            SourceEncoding.PLAIN -> {
                "plain/" + Text.escapePlain(source) + (if (e == null) "" else "@$e")
            }

            SourceEncoding.ENCRYPTED -> {
                val key = encryption ?: fail(ErrorCode.MISSING_ENCRYPTION, "encrypted sources need an encryption key")
                "enc/" + split(Encryption.encryptSource(source, key)) + (if (e == null) "" else ".$e")
            }

            SourceEncoding.BASE64 -> {
                split(Text.b64url(Text.utf8(source))) + (if (e == null) "" else ".$e")
            }
        }
    }

    private fun split(token: String): String {
        val n = splitSource
        if (n <= 0 || token.length <= n) return token
        return token.chunked(n).joinToString("/")
    }

    override fun toString(): String =
        "ImgproxyUrlBuilder(baseUrl=$baseUrl, signed=${signing != null}, encryption=${encryption != null}, " +
            "sourceEncoding=$sourceEncoding, separator=$separator, optionNames=$optionNames)"

    internal companion object {
        /** The pdf_password token, checking that both keys are present. */
        fun passwordToken(
            encryption: EncryptionKey?,
            signing: SigningKey?,
            password: String,
        ): String {
            val key = encryption ?: fail(ErrorCode.MISSING_ENCRYPTION, "pdf_password needs an encryption key")
            if (signing == null) fail(ErrorCode.MISSING_SIGNING, "pdf_password needs signing: the server refuses it on unsigned URLs")
            return Encryption.encryptPdfPassword(password, key)
        }
    }
}
