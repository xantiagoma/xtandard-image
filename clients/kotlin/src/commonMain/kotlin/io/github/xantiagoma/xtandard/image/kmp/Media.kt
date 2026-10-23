package io.github.xantiagoma.xtandard.image.kmp

import io.ktor.http.Headers
import io.ktor.http.HttpHeaders
import io.ktor.utils.io.ByteReadChannel

/**
 * A byte range for the `Range` header of media requests (`/video` results and stream parts answer 206 with
 * `Content-Range`).
 *
 * ```
 * ByteRange.From(0)            // bytes=0-
 * ByteRange.Between(0, 1023)   // bytes=0-1023
 * ByteRange.Suffix(500)        // bytes=-500
 * ByteRange.Raw("bytes=1-2,5-6")
 * ```
 */
public sealed interface ByteRange {
    /** The `Range` header value, e.g. `bytes=0-1023`. */
    public val header: String

    /**
     * Bytes from [start] to the end.
     *
     * @property start the first byte, at least 0
     * @throws UrlBuildException [ErrorCode.INVALID_RANGE] for a negative start
     */
    public data class From(
        val start: Long,
    ) : ByteRange {
        init {
            nonNegative(start, "start")
        }

        override val header: String get() = "bytes=$start-"
    }

    /**
     * Bytes [start] to [end], inclusive.
     *
     * @property start the first byte, at least 0
     * @property end the last byte, at least [start]
     * @throws UrlBuildException [ErrorCode.INVALID_RANGE] for a negative value or an end before the start
     */
    public data class Between(
        val start: Long,
        val end: Long,
    ) : ByteRange {
        init {
            nonNegative(start, "start")
            nonNegative(end, "end")
            if (end < start) fail(ErrorCode.INVALID_RANGE, "range end $end is before start $start")
        }

        override val header: String get() = "bytes=$start-$end"
    }

    /**
     * The last [length] bytes.
     *
     * @property length the number of bytes, at least 0
     * @throws UrlBuildException [ErrorCode.INVALID_RANGE] for a negative length
     */
    public data class Suffix(
        val length: Long,
    ) : ByteRange {
        init {
            nonNegative(length, "suffix")
        }

        override val header: String get() = "bytes=-$length"
    }

    /**
     * A header value written verbatim.
     *
     * @property header the value, e.g. `bytes=1-2`
     */
    public data class Raw(
        override val header: String,
    ) : ByteRange
}

private fun nonNegative(
    n: Long,
    what: String,
) {
    if (n < 0) fail(ErrorCode.INVALID_RANGE, "range $what $n is not a non-negative integer")
}

/** The HTTP method of a media request. */
public enum class MediaMethod {
    /** Fetch the body. */
    GET,

    /** Headers only: size, type, `Accept-Ranges`. */
    HEAD,
}

/**
 * How a media request is sent.
 *
 * @property method GET (default) or HEAD
 * @property range a byte range; null for the whole body
 * @property accept the `Accept` header; any type by default (`image/avif,image/webp` negotiates the format of
 *   format-less URLs)
 * @property headers extra headers
 */
public data class MediaRequest(
    val method: MediaMethod = MediaMethod.GET,
    val range: ByteRange? = null,
    val accept: String = "*/*",
    val headers: Map<String, String> = emptyMap(),
)

/**
 * A 2xx media answer with its body in memory.
 *
 * @property status 200, or 206 for a range
 * @property url the request URL
 * @property headers the response headers (case-insensitive)
 * @property body the body; empty for HEAD
 */
public class MediaResponse(
    public val status: Int,
    public val url: String,
    public val headers: Headers,
    public val body: ByteArray,
) {
    /** The `Content-Type` header. */
    public val contentType: String? get() = headers[HttpHeaders.ContentType]

    /** The `Content-Length` header. */
    public val contentLength: Long? get() = headers[HttpHeaders.ContentLength]?.toLongOrNull()

    /** The `Content-Range` header of a 206 answer. */
    public val contentRange: String? get() = headers[HttpHeaders.ContentRange]

    override fun toString(): String = "MediaResponse(status=$status, url=$url, contentType=$contentType, bytes=${body.size})"
}

/**
 * A 2xx media answer whose body is read as it arrives ([XtandardClient.mediaStream]). Valid only inside the block it
 * is passed to.
 *
 * @property status 200, or 206 for a range
 * @property url the request URL
 * @property headers the response headers (case-insensitive)
 * @property body the body, read with `readRemaining`, `readAvailable`, `copyTo` and the like
 */
public class MediaStream(
    public val status: Int,
    public val url: String,
    public val headers: Headers,
    public val body: ByteReadChannel,
) {
    /** The `Content-Type` header. */
    public val contentType: String? get() = headers[HttpHeaders.ContentType]

    /** The `Content-Length` header. */
    public val contentLength: Long? get() = headers[HttpHeaders.ContentLength]?.toLongOrNull()
}

/**
 * The answer of `/readyz`.
 *
 * @property ready whether the server takes traffic (200 `ready`)
 * @property state `ready`, `starting` or `draining`
 */
public data class Readiness(
    val ready: Boolean,
    val state: String,
)

/**
 * An HLS playlist or DASH MPD.
 *
 * @property url the manifest URL
 * @property contentType its media type
 * @property body the manifest text; child URLs are server-issued tickets that need no signing
 */
public data class StreamManifest(
    val url: String,
    val contentType: String,
    val body: String,
)
