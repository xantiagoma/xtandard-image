package io.github.xantiagoma.xtandard.image.kmp

/** How option names are written in URLs. */
public enum class OptionNameStyle {
    /** The short name of each option (`rs`, `q`): the default. */
    SHORT,

    /** The canonical name (`resize`, `quality`). */
    CANONICAL,

    /** The name or alias the caller used. */
    GIVEN,
}

/**
 * One option occurrence: a name or alias and its arguments (strings, numbers, booleans; null is an empty argument).
 *
 * @property name the option name or alias
 * @property args the arguments
 */
public data class OptionEntry(
    val name: String,
    val args: List<Any?>,
) {
    /** Factories. */
    public companion object {
        /**
         * An option occurrence.
         *
         * @param name the option name or alias
         * @param args the arguments
         * @return the entry
         */
        public fun of(
            name: String,
            vararg args: Any?,
        ): OptionEntry = OptionEntry(name, args.toList())
    }
}

/** Marks the option and URL builder DSLs, so an inner block cannot call an outer receiver by mistake. */
@DslMarker
public annotation class XtandardDsl

/**
 * The options of one pipeline, in URL order: what [ProcessingOptions], [InfoOptions] and [VideoOptions] share.
 * Mutable and not thread-safe, like a [StringBuilder].
 *
 * @param S the concrete options type
 */
@XtandardDsl
public sealed class OptionList<S : OptionList<S>> {
    private val list = mutableListOf<OptionEntry>()

    internal abstract fun self(): S

    /**
     * Appends one option occurrence.
     *
     * @param name an option name or alias
     * @param args the arguments: strings, numbers, booleans or null (an empty argument)
     * @return this
     */
    public fun option(
        name: String,
        vararg args: Any?,
    ): S {
        list += OptionEntry(name, args.toList())
        return self()
    }

    /**
     * Appends one option occurrence.
     *
     * @param entry the option
     * @return this
     */
    public fun option(entry: OptionEntry): S {
        list += entry
        return self()
    }

    /** The options so far, in order. */
    public val entries: List<OptionEntry> get() = list.toList()

    /** Whether no option was added. */
    public fun isEmpty(): Boolean = list.isEmpty()

    override fun toString(): String = "${this::class.simpleName}$list"
}

/** Processing options of one pipeline: `rs:fill:300:400/q:80`. Build with [processingOptions] or chain the methods. */
public class ProcessingOptions :
    OptionList<ProcessingOptions>(),
    ProcessingMethods<ProcessingOptions> {
    override fun self(): ProcessingOptions = this
}

/** `/info` options: `s:1/f:1/d:1`. Build with [infoOptions] or chain the methods. */
public class InfoOptions :
    OptionList<InfoOptions>(),
    InfoMethods<InfoOptions> {
    override fun self(): InfoOptions = this
}

/** `/video` options: `vs:12/vd:8/w:640`. Build with [videoOptions] or chain the methods. */
public class VideoOptions :
    OptionList<VideoOptions>(),
    VideoMethods<VideoOptions> {
    override fun self(): VideoOptions = this
}

/**
 * Processing options from a DSL block: `processingOptions { resize("fill", 300, 400); quality(80) }`.
 *
 * @param block adds the options
 * @return the options
 */
public fun processingOptions(block: ProcessingOptions.() -> Unit): ProcessingOptions = ProcessingOptions().apply(block)

/**
 * `/info` options from a DSL block: `infoOptions { size(true); format(true) }`.
 *
 * @param block adds the options
 * @return the options
 */
public fun infoOptions(block: InfoOptions.() -> Unit): InfoOptions = InfoOptions().apply(block)

/**
 * `/video` options from a DSL block: `videoOptions { start(12); duration(8) }`.
 *
 * @param block adds the options
 * @return the options
 */
public fun videoOptions(block: VideoOptions.() -> Unit): VideoOptions = VideoOptions().apply(block)

/** One option catalogue at run time, and the serialisation of options into option segments. */
internal class Catalogue(
    private val kind: String,
    val names: Map<String, String>,
    private val shortNames: Map<String, String>,
) {
    /** The canonical name of an option; unknown names are an error. */
    fun canonical(name: String): String = names[name] ?: fail(ErrorCode.UNKNOWN_OPTION, "unknown $kind option \"$name\"")

    /** The name an option is written with. */
    fun written(
        name: String,
        style: OptionNameStyle,
    ): String {
        val c = canonical(name)
        return when (style) {
            OptionNameStyle.GIVEN -> name
            OptionNameStyle.CANONICAL -> c
            OptionNameStyle.SHORT -> shortNames[c] ?: c
        }
    }

    /** Formats one option segment (`rs:fill:300:400`). */
    fun format(
        e: OptionEntry,
        sep: String,
        style: OptionNameStyle,
    ): String {
        val written = written(e.name, style)
        if (e.args.isEmpty()) fail(ErrorCode.INVALID_ARGUMENT, "option ${e.name}: at least one argument is required")
        val b = StringBuilder(written)
        e.args.forEachIndexed { i, a -> b.append(sep).append(formatArg(a, sep, where(e, i))) }
        return b.toString()
    }

    companion object {
        val PROCESSING = Catalogue("imgproxy", OptionTables.PROCESSING_NAMES, OptionTables.PROCESSING_SHORT)
        val INFO = Catalogue("info", OptionTables.INFO_NAMES, OptionTables.INFO_SHORT)
        val VIDEO = Catalogue("video", OptionTables.VIDEO_NAMES, OptionTables.VIDEO_SHORT)

        fun where(
            e: OptionEntry,
            i: Int,
        ): String = "option ${e.name} argument ${i + 1}"

        /** Formats one argument. Strings are checked, never escaped: the server does not unescape option arguments. */
        fun formatArg(
            a: Any?,
            sep: String,
            where: String,
        ): String =
            when (a) {
                null -> {
                    ""
                }

                is Boolean -> {
                    if (a) "1" else "0"
                }

                is Number -> {
                    Text.formatNumber(a, where)
                }

                is UInt, is ULong, is UShort, is UByte -> {
                    a.toString()
                }

                is CharSequence, is Enum<*> -> {
                    val s = a.toString()
                    if (s.contains(sep) || s.any { badArgChar(it) }) {
                        fail(ErrorCode.INVALID_ARGUMENT, "$where: argument \"$s\" contains a character that cannot appear in an option")
                    }
                    s
                }

                else -> {
                    fail(ErrorCode.INVALID_ARGUMENT, "$where: unsupported argument type ${a::class.simpleName}")
                }
            }

        private fun badArgChar(c: Char): Boolean = c == '/' || c == '?' || c == '#' || c < ' ' || c == '\u007f' || Text.isJsWhitespace(c)
    }
}
