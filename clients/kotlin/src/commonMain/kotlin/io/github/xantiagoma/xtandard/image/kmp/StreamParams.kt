package io.github.xantiagoma.xtandard.image.kmp

/**
 * An HLS or DASH manifest request: the protocol, the rendition ladder, the segment settings and other `/video`
 * options. Immutable.
 *
 * ```
 * StreamParams.hls(renditions = listOf(360, 720), segmentDuration = 4)
 * ```
 *
 * @property protocol `hls` or `dash` (another name is an error when the URL is built)
 * @property renditions rungs by the short side of the frame (at most 8); empty for the server's ladder
 * @property segmentDuration segment length in whole seconds (1 to 30); null for the server's default
 * @property segmentFormat `fmp4` (CMAF, the default) or `ts` (MPEG-TS, HLS only); null for the default
 * @property options other `/video` options (trim, crop, quality, audio, ...) applied to every rendition
 * @property sourceEncoding overrides the builder's source encoding (imgproxy dialect only); null keeps it
 */
public data class StreamParams(
    val protocol: String,
    val renditions: List<Int> = emptyList(),
    val segmentDuration: Int? = null,
    val segmentFormat: String? = null,
    val options: VideoOptions? = null,
    val sourceEncoding: SourceEncoding? = null,
) {
    /**
     * A manifest for a protocol.
     *
     * @param protocol the protocol
     * @param renditions short sides of the frame, e.g. 360, 720
     * @param segmentDuration whole seconds, 1 to 30
     * @param segmentFormat the segment format; null for the default
     * @param options other `/video` options
     * @param sourceEncoding the source encoding (imgproxy dialect)
     */
    public constructor(
        protocol: StreamProtocol,
        renditions: List<Int> = emptyList(),
        segmentDuration: Int? = null,
        segmentFormat: SegmentFormat? = null,
        options: VideoOptions? = null,
        sourceEncoding: SourceEncoding? = null,
    ) : this(protocol.value, renditions, segmentDuration, segmentFormat?.value, options, sourceEncoding)

    /** Factories per protocol. */
    public companion object {
        /**
         * An HLS manifest (`@m3u8`).
         *
         * @param renditions short sides of the frame; empty for the server's ladder
         * @param segmentDuration whole seconds, 1 to 30
         * @param segmentFormat the segment format; null for the default
         * @param options other `/video` options
         * @return the request
         */
        public fun hls(
            renditions: List<Int> = emptyList(),
            segmentDuration: Int? = null,
            segmentFormat: SegmentFormat? = null,
            options: VideoOptions? = null,
        ): StreamParams = StreamParams(StreamProtocol.HLS, renditions, segmentDuration, segmentFormat, options)

        /**
         * A DASH manifest (`@mpd`).
         *
         * @param renditions short sides of the frame; empty for the server's ladder
         * @param segmentDuration whole seconds, 1 to 30
         * @param segmentFormat the segment format; null for the default (`ts` is HLS only)
         * @param options other `/video` options
         * @return the request
         */
        public fun dash(
            renditions: List<Int> = emptyList(),
            segmentDuration: Int? = null,
            segmentFormat: SegmentFormat? = null,
            options: VideoOptions? = null,
        ): StreamParams = StreamParams(StreamProtocol.DASH, renditions, segmentDuration, segmentFormat, options)
    }
}

/** The `/video` options and manifest container of a stream request. */
internal class StreamParts(
    val entries: List<OptionEntry>,
    val container: String,
) {
    companion object {
        private val reserved = setOf("stream", "segment_duration", "segment_format", "format")

        fun of(p: StreamParams): StreamParts {
            val entries = mutableListOf<OptionEntry>()
            p.options?.let { entries += it.entries }
            for (e in entries) {
                val canonical = Catalogue.VIDEO.names[e.name]
                if (canonical != null && canonical in reserved) {
                    fail(
                        ErrorCode.INVALID_REQUEST,
                        "stream requests set ${e.name} through the protocol, the renditions and the segment settings",
                    )
                }
            }
            val protocol = StreamProtocol.of(p.protocol) ?: fail(ErrorCode.INVALID_REQUEST, "unknown stream protocol \"${p.protocol}\"")
            if (p.renditions.isNotEmpty()) entries += OptionEntry("stream", listOf<Any?>(protocol.value) + p.renditions)
            p.segmentDuration?.let { entries += OptionEntry.of("segment_duration", it) }
            p.segmentFormat?.let { name ->
                val f = SegmentFormat.of(name)
                if (f == null || protocol.value !in f.protocols) {
                    fail(ErrorCode.INVALID_REQUEST, "segment format \"$name\" is not available for ${protocol.value}")
                }
                if (f != SegmentFormat.DEFAULT) entries += OptionEntry.of("segment_format", f.value)
            }
            return StreamParts(entries, protocol.container)
        }
    }
}
