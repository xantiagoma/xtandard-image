package io.github.xantiagoma.xtandard.image.kmp

/**
 * Query keys and values in the order they are added: what [QueryParams] and [ImaginaryParams] share. Keys may
 * repeat; a null value is skipped. Mutable and not thread-safe.
 *
 * @param S the concrete type
 */
@XtandardDsl
public sealed class ParamList<S : ParamList<S>> {
    private val pairs = mutableListOf<Pair<String, Any?>>()

    internal abstract fun self(): S

    /**
     * Appends a key; any key is allowed.
     *
     * @param key the key
     * @param value a string, number or boolean (`true`/`false`); null skips the key
     * @return this
     */
    public fun param(
        key: String,
        value: Any?,
    ): S {
        pairs += key to value
        return self()
    }

    /** The keys and values so far, in order (null values included). */
    public val entries: List<Pair<String, Any?>> get() = pairs.toList()

    /** The non-null pairs, formatted (booleans true/false, numbers as ECMAScript writes them). */
    internal fun formatted(): List<Pair<String, String>> = pairs.mapNotNull { (k, v) -> if (v == null) null else k to nativeValue(k, v) }

    override fun toString(): String = "${this::class.simpleName}$pairs"

    internal companion object {
        fun nativeValue(
            key: String,
            v: Any,
        ): String =
            when (v) {
                is Boolean -> v.toString()
                is Number -> Text.formatNumber(v, "query key $key")
                is UInt, is ULong, is UShort, is UByte -> v.toString()
                is CharSequence, is Enum<*> -> v.toString()
                else -> fail(ErrorCode.INVALID_ARGUMENT, "query key $key: unsupported value type ${v::class.simpleName}")
            }
    }
}

/**
 * Native query keys from a DSL block: `queryParams { w(300); f("webp") }`.
 *
 * @param block adds the keys
 * @return the keys
 */
public fun queryParams(block: QueryParams.() -> Unit): QueryParams = QueryParams().apply(block)

/**
 * imaginary parameters from a DSL block: `imaginaryParams { width(300) }`.
 *
 * @param block adds the parameters
 * @return the parameters
 */
public fun imaginaryParams(block: ImaginaryParams.() -> Unit): ImaginaryParams = ImaginaryParams().apply(block)
