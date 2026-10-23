package io.github.xantiagoma.xtandard.image.kmp

/**
 * The language-neutral code of a URL building fault, as in `clients/spec/conformance.json`.
 *
 * @property code the code as written in the fixture (`invalid_source`, ...)
 */
public enum class ErrorCode(
    public val code: String,
) {
    /** An option name not in the catalogue. */
    UNKNOWN_OPTION("unknown_option"),

    /** No arguments, a forbidden character, a non-finite number, an unsupported type, or `:` in a `/q/video` argument. */
    INVALID_ARGUMENT("invalid_argument"),

    /** An empty source, or a source with control characters. */
    INVALID_SOURCE("invalid_source"),

    /** An extension that is not alphanumeric. */
    INVALID_EXTENSION("invalid_extension"),

    /** A separator that is not one character, or is forbidden. */
    INVALID_SEPARATOR("invalid_separator"),

    /** A PDF password without a signing key. */
    MISSING_SIGNING("missing_signing"),

    /** An encrypted source or a PDF password without an encryption key. */
    MISSING_ENCRYPTION("missing_encryption"),

    /** Key material that is not hex, a key or IV of the wrong size, or a signature size outside 1 to 32. */
    INVALID_KEY("invalid_key"),

    /** An empty password, a password with control characters, or one over 1024 UTF-8 bytes. */
    INVALID_PASSWORD("invalid_password"),

    /** A request the endpoint cannot carry: chained `/info` or `/video` pipelines, a stream option, two `xi_sig`, ... */
    INVALID_REQUEST("invalid_request"),

    /** A bad `xi://` source name or authority. */
    INVALID_NAME("invalid_name"),

    /** An imaginary endpoint that is not lower-case letters, or is `video`. */
    INVALID_ENDPOINT("invalid_endpoint"),

    /** A negative range value, or an end before the start. */
    INVALID_RANGE("invalid_range"),
    ;

    override fun toString(): String = code
}

/**
 * Everything this library throws: [UrlBuildException] for bad input, [HttpException] for non-2xx answers and
 * [TransportException] for connection failures and malformed answers.
 */
public sealed class XtandardException(
    message: String,
    cause: Throwable? = null,
) : RuntimeException(message, cause)

/**
 * Bad input to a URL builder, key or helper.
 *
 * @property code the language-neutral code of the fault
 */
public class UrlBuildException(
    public val code: ErrorCode,
    message: String,
) : XtandardException(message)

/** The server could not be reached, the request failed in flight, or the answer was not what the endpoint returns. */
public class TransportException(
    message: String,
    cause: Throwable? = null,
) : XtandardException(message, cause)

/**
 * A non-2xx answer. [of] picks the subclass by status: [BadRequest] (400), [Forbidden] (403), [NotFound] (404),
 * [NotAcceptable] (406), [Unprocessable] (422), [TooManyRequests] (429), [ServerError] (5xx, with
 * [ServiceUnavailable] for 503 and [Gateway] for 502 and 504); other statuses are a plain [HttpException].
 *
 * @property status the HTTP status
 * @property url the request URL
 * @property body the server's JSON error envelope, or null when the body was not one
 */
public open class HttpException(
    public val status: Int,
    public val url: String,
    message: String,
    public val body: ErrorResponse?,
) : XtandardException(message) {
    /** The error kind from the envelope (`not_found`, `unauthorized`, ...), or `unknown`. */
    public val kind: String get() = body?.error?.kind ?: "unknown"

    /** The request ID from the envelope, also in the server's logs. */
    public val requestId: String? get() = body?.error?.requestId

    /** 400: an invalid URL. */
    public class BadRequest(
        status: Int,
        url: String,
        message: String,
        body: ErrorResponse?,
    ) : HttpException(status, url, message, body)

    /** 403: a bad signature or a forbidden source. */
    public class Forbidden(
        status: Int,
        url: String,
        message: String,
        body: ErrorResponse?,
    ) : HttpException(status, url, message, body)

    /** 404: the source was not found, or the URL expired. */
    public class NotFound(
        status: Int,
        url: String,
        message: String,
        body: ErrorResponse?,
    ) : HttpException(status, url, message, body)

    /** 406: no acceptable output format. */
    public class NotAcceptable(
        status: Int,
        url: String,
        message: String,
        body: ErrorResponse?,
    ) : HttpException(status, url, message, body)

    /** 422: the source is too large, corrupt or unsupported, or exceeds a limit. */
    public class Unprocessable(
        status: Int,
        url: String,
        message: String,
        body: ErrorResponse?,
    ) : HttpException(status, url, message, body)

    /** 429: the server's queue is full. */
    public class TooManyRequests(
        status: Int,
        url: String,
        message: String,
        body: ErrorResponse?,
    ) : HttpException(status, url, message, body)

    /** 5xx. */
    public open class ServerError(
        status: Int,
        url: String,
        message: String,
        body: ErrorResponse?,
    ) : HttpException(status, url, message, body)

    /** 503: overloaded or shutting down. */
    public class ServiceUnavailable(
        status: Int,
        url: String,
        message: String,
        body: ErrorResponse?,
    ) : ServerError(status, url, message, body)

    /** 502 or 504: the upstream source failed or timed out. */
    public class Gateway(
        status: Int,
        url: String,
        message: String,
        body: ErrorResponse?,
    ) : ServerError(status, url, message, body)

    /** Builds the exception for a status. */
    public companion object {
        /**
         * The exception class for a status.
         *
         * @param status the HTTP status
         * @param url the request URL
         * @param message the message (the envelope's, or the body text)
         * @param body the envelope, or null
         * @return the exception, not thrown
         */
        public fun of(
            status: Int,
            url: String,
            message: String,
            body: ErrorResponse?,
        ): HttpException =
            when (status) {
                400 -> BadRequest(status, url, message, body)
                403 -> Forbidden(status, url, message, body)
                404 -> NotFound(status, url, message, body)
                406 -> NotAcceptable(status, url, message, body)
                422 -> Unprocessable(status, url, message, body)
                429 -> TooManyRequests(status, url, message, body)
                502, 504 -> Gateway(status, url, message, body)
                503 -> ServiceUnavailable(status, url, message, body)
                else -> if (status >= 500) ServerError(status, url, message, body) else HttpException(status, url, message, body)
            }
    }
}

internal fun fail(
    code: ErrorCode,
    message: String,
): Nothing = throw UrlBuildException(code, message)
