package io.github.xantiagoma.xtandard.image.kmp

/**
 * A fluent URL under construction: the source, the options of each pipeline, and the per-URL settings. Mutable and
 * not thread-safe; [build] can be called any number of times.
 *
 * @param S the concrete chain type
 */
@XtandardDsl
public sealed class UrlChain<S : UrlChain<S>>(
    internal val source: String,
) {
    internal val pipelines: MutableList<MutableList<OptionEntry>> = mutableListOf(mutableListOf())
    internal var encodingValue: SourceEncoding? = null
    internal var ext: String? = null
    internal var pdfPasswordValue: String? = null

    internal abstract fun self(): S

    /**
     * Appends one option occurrence to the current pipeline.
     *
     * @param name an option name or alias
     * @param args the arguments: strings, numbers, booleans or null (an empty argument)
     * @return this
     */
    public fun option(
        name: String,
        vararg args: Any?,
    ): S {
        pipelines.last() += OptionEntry(name, args.toList())
        return self()
    }

    /**
     * Appends one option occurrence to the current pipeline.
     *
     * @param entry the option
     * @return this
     */
    public fun option(entry: OptionEntry): S {
        pipelines.last() += entry
        return self()
    }

    internal fun addAll(options: OptionList<*>): S {
        pipelines.last() += options.entries
        return self()
    }

    /**
     * Builds the URL.
     *
     * @return the URL
     * @throws UrlBuildException when an option, the source, the extension or a key is invalid
     */
    public abstract fun build(): String

    /** The URL, as [build] writes it. */
    override fun toString(): String = build()
}

/** A fluent processing URL ([ImgproxyUrlBuilder.image]). */
public class ImgproxyUrlChain internal constructor(
    private val builder: ImgproxyUrlBuilder,
    source: String,
) : UrlChain<ImgproxyUrlChain>(source),
    ProcessingMethods<ImgproxyUrlChain> {
    override fun self(): ImgproxyUrlChain = this

    /**
     * Appends options to the current pipeline.
     *
     * @param options the options
     * @return this
     */
    public fun options(options: ProcessingOptions): ImgproxyUrlChain = addAll(options)

    /**
     * Starts the next chained pipeline (`/-/`).
     *
     * @return this
     */
    public fun pipeline(): ImgproxyUrlChain = apply { pipelines += mutableListOf<OptionEntry>() }

    /**
     * Sets the output format (`@webp` or `.webp`).
     *
     * @param extension an alphanumeric extension
     * @return this
     */
    public fun extension(extension: String): ImgproxyUrlChain = apply { ext = extension }

    /**
     * Sets the output format.
     *
     * @param format the format
     * @return this
     */
    public fun extension(format: ImageFormat): ImgproxyUrlChain = extension(format.value)

    /**
     * Overrides the builder's source encoding.
     *
     * @param encoding the encoding
     * @return this
     */
    public fun encoding(encoding: SourceEncoding): ImgproxyUrlChain = apply { encodingValue = encoding }

    /**
     * Adds the encrypted password of a password-protected PDF source (needs both keys).
     *
     * @param password the plain-text password
     * @return this
     */
    public fun withPdfPassword(password: String): ImgproxyUrlChain = apply { pdfPasswordValue = password }

    override fun build(): String =
        builder.build(ImgproxyUrlBuilder.Endpoint.PROCESS, pipelines, source, encodingValue, ext, pdfPasswordValue)
}

/** A fluent `/info` URL ([ImgproxyUrlBuilder.info]): one pipeline, no extension. */
public class ImgproxyInfoUrlChain internal constructor(
    private val builder: ImgproxyUrlBuilder,
    source: String,
) : UrlChain<ImgproxyInfoUrlChain>(source),
    InfoMethods<ImgproxyInfoUrlChain> {
    override fun self(): ImgproxyInfoUrlChain = this

    /**
     * Appends options.
     *
     * @param options the options
     * @return this
     */
    public fun options(options: InfoOptions): ImgproxyInfoUrlChain = addAll(options)

    /**
     * Overrides the builder's source encoding.
     *
     * @param encoding the encoding
     * @return this
     */
    public fun encoding(encoding: SourceEncoding): ImgproxyInfoUrlChain = apply { encodingValue = encoding }

    /**
     * Adds the encrypted password of a password-protected PDF source (needs both keys).
     *
     * @param password the plain-text password
     * @return this
     */
    public fun withPdfPassword(password: String): ImgproxyInfoUrlChain = apply { pdfPasswordValue = password }

    override fun build(): String = builder.build(ImgproxyUrlBuilder.Endpoint.INFO, pipelines, source, encodingValue, null, pdfPasswordValue)
}

/** A fluent `/video` URL ([ImgproxyUrlBuilder.video]): one pipeline, no PDF password. */
public class ImgproxyVideoUrlChain internal constructor(
    private val builder: ImgproxyUrlBuilder,
    source: String,
) : UrlChain<ImgproxyVideoUrlChain>(source),
    VideoMethods<ImgproxyVideoUrlChain> {
    override fun self(): ImgproxyVideoUrlChain = this

    /**
     * Appends options.
     *
     * @param options the options
     * @return this
     */
    public fun options(options: VideoOptions): ImgproxyVideoUrlChain = addAll(options)

    /**
     * Sets the output container.
     *
     * @param extension `webm`, `mp4`, `mp3`, `png`, ...
     * @return this
     */
    public fun extension(extension: String): ImgproxyVideoUrlChain = apply { ext = extension }

    /**
     * Sets the output container.
     *
     * @param container the container
     * @return this
     */
    public fun extension(container: VideoContainer): ImgproxyVideoUrlChain = extension(container.value)

    /**
     * Overrides the builder's source encoding.
     *
     * @param encoding the encoding
     * @return this
     */
    public fun encoding(encoding: SourceEncoding): ImgproxyVideoUrlChain = apply { encodingValue = encoding }

    override fun build(): String = builder.build(ImgproxyUrlBuilder.Endpoint.VIDEO, pipelines, source, encodingValue, ext, null)
}

/** A fluent `/q/?url=...` URL ([QueryUrlBuilder.image]): option methods write `o=`, [param] a native key. */
public class QueryUrlChain internal constructor(
    private val builder: QueryUrlBuilder,
    source: String,
) : UrlChain<QueryUrlChain>(source),
    ProcessingMethods<QueryUrlChain> {
    private val params = QueryParams()

    override fun self(): QueryUrlChain = this

    /**
     * Adds a native key (`w`, `fit`, `f`, ...); null skips it.
     *
     * @param key the key
     * @param value a string, number or boolean
     * @return this
     */
    public fun param(
        key: String,
        value: Any?,
    ): QueryUrlChain = apply { params.param(key, value) }

    /**
     * Adds native keys.
     *
     * @param params the keys
     * @return this
     */
    public fun params(params: QueryParams): QueryUrlChain = apply { params.entries.forEach { (k, v) -> this.params.param(k, v) } }

    /**
     * Appends options to `o=`'s current pipeline.
     *
     * @param options the options
     * @return this
     */
    public fun options(options: ProcessingOptions): QueryUrlChain = addAll(options)

    /**
     * Starts the next chained pipeline inside `o=`.
     *
     * @return this
     */
    public fun pipeline(): QueryUrlChain = apply { pipelines += mutableListOf<OptionEntry>() }

    /**
     * Sets the output format (`format=`).
     *
     * @param extension an alphanumeric extension
     * @return this
     */
    public fun extension(extension: String): QueryUrlChain = apply { ext = extension }

    /**
     * Sets the output format (`format=`).
     *
     * @param format the format
     * @return this
     */
    public fun extension(format: ImageFormat): QueryUrlChain = extension(format.value)

    /**
     * Adds the encrypted password of a password-protected PDF source (`pdp=`; needs both keys).
     *
     * @param password the plain-text password
     * @return this
     */
    public fun withPdfPassword(password: String): QueryUrlChain = apply { pdfPasswordValue = password }

    override fun build(): String = builder.image(source, params, pipelines, ext, pdfPasswordValue)
}

/** A fluent `/q/video` URL ([QueryUrlBuilder.video]): every option is a query key. */
public class QueryVideoUrlChain internal constructor(
    private val builder: QueryUrlBuilder,
    source: String,
) : UrlChain<QueryVideoUrlChain>(source),
    VideoMethods<QueryVideoUrlChain> {
    override fun self(): QueryVideoUrlChain = this

    /**
     * Appends options.
     *
     * @param options the options
     * @return this
     */
    public fun options(options: VideoOptions): QueryVideoUrlChain = addAll(options)

    /**
     * Sets the output container (`format=`).
     *
     * @param extension `webm`, `mp4`, ...
     * @return this
     */
    public fun extension(extension: String): QueryVideoUrlChain = apply { ext = extension }

    /**
     * Sets the output container (`format=`).
     *
     * @param container the container
     * @return this
     */
    public fun extension(container: VideoContainer): QueryVideoUrlChain = extension(container.value)

    override fun build(): String = builder.video(source, pipelines.flatten(), ext)
}
