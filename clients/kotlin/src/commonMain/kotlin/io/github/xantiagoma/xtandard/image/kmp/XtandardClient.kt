package io.github.xantiagoma.xtandard.image.kmp

import io.ktor.client.HttpClient
import io.ktor.client.plugins.HttpTimeout
import io.ktor.client.plugins.expectSuccess
import io.ktor.client.request.header
import io.ktor.client.request.prepareRequest
import io.ktor.client.request.url
import io.ktor.client.statement.HttpResponse
import io.ktor.client.statement.bodyAsBytes
import io.ktor.client.statement.bodyAsChannel
import io.ktor.client.statement.bodyAsText
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpMethod
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlin.time.Duration
import kotlin.time.Duration.Companion.seconds

/**
 * The JSON configuration of the client's answers: unknown keys are ignored, so a newer server's extra fields do not
 * break an older client. Decode a body fetched another way with `XtandardJson.decodeFromString<InfoResponse>(text)`.
 */
public val XtandardJson: Json =
    Json {
        ignoreUnknownKeys = true
    }

/**
 * An HTTP client for an xtandard-image server, on Ktor: suspend functions, OkHttp on the JVM and Android, Darwin
 * (URLSession) on Apple platforms. It builds URLs with [urls] and fetches them.
 *
 * ```
 * XtandardClient("http://localhost:8080", signing = SigningKey.of(KEY, SALT)).use { xi ->
 *     xi.healthz()
 *     val info = xi.info("s3://photos/cat.jpg", infoOptions { size(true); format(true) })
 *     val img = xi.image("s3://photos/cat.jpg", processingOptions { width(300) }, extension = "webp")
 * }
 * ```
 *
 * Non-2xx answers throw an [HttpException] subclass chosen by status; connection failures and malformed answers
 * throw [TransportException]; bad input throws [UrlBuildException]. Thread-safe. [close] closes the HTTP client
 * only when this client created it.
 *
 * @param baseUrl the server origin (and the imgproxy dialect's mount prefix, if any)
 * @param signing signs URLs with the imgproxy scheme; null for unsigned URLs
 * @param encryption the key for encrypted sources and PDF passwords
 * @param unsignedSignature the signature segment of unsigned URLs
 * @param sourceEncoding the default source encoding
 * @param splitSource splits base64 and encrypted sources into chunks of this many characters; 0 does not split
 * @param separator the argument separator
 * @param optionNames how option names are written
 * @param headers sent with every request
 * @param timeout the request timeout of the HTTP client this client creates (default 60 s)
 * @param httpClient a Ktor client to use instead (engine, proxy, TLS, logging); its own timeouts apply, and this
 *   client never closes it
 * @param playgroundBaseUrl the origin of the playground (the admin listener); [baseUrl] by default
 * @param playgroundPath the playground's path (XI_PLAYGROUND_PATH)
 */
public class XtandardClient(
    baseUrl: String,
    signing: SigningKey? = null,
    encryption: EncryptionKey? = null,
    unsignedSignature: String = Constants.DEFAULT_UNSIGNED,
    sourceEncoding: SourceEncoding = SourceEncoding.BASE64,
    splitSource: Int = 0,
    separator: String = Constants.DEFAULT_SEPARATOR,
    optionNames: OptionNameStyle = OptionNameStyle.SHORT,
    headers: Map<String, String> = emptyMap(),
    timeout: Duration = 60.seconds,
    httpClient: HttpClient? = null,
    playgroundBaseUrl: String? = null,
    playgroundPath: String = "/_playground/",
) : AutoCloseable {
    /** The URL builder of this client's URLs. */
    public val urls: ImgproxyUrlBuilder =
        ImgproxyUrlBuilder(baseUrl, signing, unsignedSignature, encryption, sourceEncoding, splitSource, separator, optionNames)

    private val defaultHeaders = headers.toMap()
    private val ownsClient = httpClient == null
    private val http: HttpClient =
        httpClient ?: HttpClient(defaultEngine()) {
            expectSuccess = false
            install(HttpTimeout) {
                requestTimeoutMillis = timeout.inWholeMilliseconds
                connectTimeoutMillis = timeout.inWholeMilliseconds
            }
        }
    private val playgroundMetaUrl: String =
        Signing.stripTrailingSlashes(playgroundBaseUrl ?: baseUrl) + "/" + Signing.stripTrailingSlashes(playgroundPath.trimStart('/')) +
            "/api/meta"

    /** Closes the HTTP client if this client created it. */
    override fun close() {
        if (ownsClient) http.close()
    }

    /**
     * `GET /healthz`: returns when the server is alive.
     *
     * @throws HttpException for a non-2xx answer
     * @throws TransportException when the server cannot be reached
     */
    public suspend fun healthz() {
        send(urls.baseUrl + "/healthz", MediaRequest(accept = "text/plain")) { }
    }

    /**
     * `GET /readyz`: whether the server takes traffic. 503 with `starting` or `draining` is an answer, not an error.
     *
     * @return the state
     * @throws HttpException for another status
     * @throws TransportException when the server cannot be reached or the state is unknown
     */
    public suspend fun readyz(): Readiness {
        val url = urls.baseUrl + "/readyz"
        val (status, text) = exchange(url, MediaRequest(accept = "text/plain")) { it.status.value to it.bodyAsText() }
        if (status != 200 && status != 503) throw httpError(status, url, text)
        val state = text.trim()
        if (state != "ready" && state != "starting" && state != "draining") {
            throw TransportException("unexpected readiness state \"$state\" from $url")
        }
        return Readiness(status == 200 && state == "ready", state)
    }

    /**
     * `/info`: the source's metadata.
     *
     * @param source the source URL
     * @param options the `/info` options; null for the server's defaults
     * @param sourceEncoding overrides the client's source encoding
     * @param pdfPassword the password of a password-protected PDF source
     * @return the decoded answer
     * @throws UrlBuildException for bad input
     * @throws HttpException for a non-2xx answer
     * @throws TransportException when the server cannot be reached or the answer is not an /info body
     */
    public suspend fun info(
        source: String,
        options: InfoOptions? = null,
        sourceEncoding: SourceEncoding? = null,
        pdfPassword: String? = null,
    ): InfoResponse = infoAt(urls.infoUrl(source, options, sourceEncoding, pdfPassword))

    /**
     * `/info` at a URL built elsewhere.
     *
     * @param url an absolute URL, or a path under the base URL
     * @return the decoded answer
     */
    public suspend fun infoAt(url: String): InfoResponse {
        val abs = absolute(url)
        val text = send(abs, MediaRequest(accept = "application/json")) { it.bodyAsText() }
        return try {
            XtandardJson.decodeFromString(InfoResponse.serializer(), text)
        } catch (e: SerializationException) {
            throw TransportException("invalid /info response from $abs", e)
        } catch (e: IllegalArgumentException) {
            throw TransportException("invalid /info response from $abs", e)
        }
    }

    /**
     * A processed image.
     *
     * @param source the source URL
     * @param options the processing options; null for none
     * @param extension the output format; null to negotiate it with [MediaRequest.accept]
     * @param sourceEncoding overrides the client's source encoding
     * @param pdfPassword the password of a password-protected PDF source
     * @param request the method, range, Accept and extra headers
     * @return the answer
     */
    public suspend fun image(
        source: String,
        options: ProcessingOptions? = null,
        extension: String? = null,
        sourceEncoding: SourceEncoding? = null,
        pdfPassword: String? = null,
        request: MediaRequest = MediaRequest(),
    ): MediaResponse = media(urls.url(source, options, extension, sourceEncoding, pdfPassword), request)

    /**
     * A `/video` result (a transformed video, audio or a waveform). Use [MediaRequest.range] for a part (206) and
     * [MediaMethod.HEAD] for the headers only.
     *
     * @param source the source video
     * @param options the `/video` options; null for none
     * @param extension the output container; null for the server's default
     * @param sourceEncoding overrides the client's source encoding
     * @param request the method, range, Accept and extra headers
     * @return the answer
     */
    public suspend fun video(
        source: String,
        options: VideoOptions? = null,
        extension: String? = null,
        sourceEncoding: SourceEncoding? = null,
        request: MediaRequest = MediaRequest(),
    ): MediaResponse = media(urls.videoUrl(source, options, extension, sourceEncoding), request)

    /**
     * An HLS playlist or DASH MPD. Its child URLs are tickets: fetch them with [media], unsigned.
     *
     * @param source the source video
     * @param stream the protocol, renditions, segment settings and other options
     * @return the manifest
     */
    public suspend fun stream(
        source: String,
        stream: StreamParams,
    ): StreamManifest {
        val url = urls.streamUrl(source, stream)
        val ctype = StreamProtocol.of(stream.protocol)?.contentType ?: "application/octet-stream"
        return send(url, MediaRequest(accept = "$ctype, */*;q=0.5")) {
            StreamManifest(url, it.headers[HttpHeaders.ContentType] ?: ctype, it.bodyAsText())
        }
    }

    /**
     * Any URL (built by a URL builder, or a stream ticket), with the body in memory.
     *
     * @param url an absolute URL, or a path under the base URL
     * @param request the method, range, Accept and extra headers
     * @return the answer
     */
    public suspend fun media(
        url: String,
        request: MediaRequest = MediaRequest(),
    ): MediaResponse {
        val abs = absolute(url)
        return send(abs, request) { MediaResponse(it.status.value, abs, it.headers, it.bodyAsBytes()) }
    }

    /**
     * Any URL, with the body read as it arrives: large videos without holding them in memory. The response is
     * valid inside [block] only; exceptions from [block] pass through unchanged.
     *
     * ```
     * xi.mediaStream(url) { res -> res.body.copyTo(fileChannel) }
     * ```
     *
     * @param url an absolute URL, or a path under the base URL
     * @param request the method, range, Accept and extra headers
     * @param block reads the response
     * @return what [block] returns
     */
    public suspend fun <T> mediaStream(
        url: String,
        request: MediaRequest = MediaRequest(),
        block: suspend (MediaStream) -> T,
    ): T {
        val abs = absolute(url)
        return send(abs, request) { res ->
            try {
                block(MediaStream(res.status.value, abs, res.headers, res.bodyAsChannel()))
            } catch (e: CancellationException) {
                throw e
            } catch (e: Throwable) {
                throw PassThrough(e)
            }
        }
    }

    /**
     * The playground's `api/meta` (the admin listener's playground, when enabled).
     *
     * @return the JSON object
     */
    public suspend fun playgroundMeta(): JsonObject {
        val text = send(playgroundMetaUrl, MediaRequest(accept = "application/json")) { it.bodyAsText() }
        return try {
            XtandardJson.decodeFromString(JsonObject.serializer(), text)
        } catch (e: SerializationException) {
            throw TransportException("invalid JSON response from $playgroundMetaUrl", e)
        } catch (e: IllegalArgumentException) {
            throw TransportException("invalid JSON response from $playgroundMetaUrl", e)
        }
    }

    override fun toString(): String = "XtandardClient($urls)"

    // ---- transport -----------------------------------------------------------------------------

    private fun absolute(url: String): String =
        if (scheme.containsMatchIn(url)) {
            url
        } else {
            urls.baseUrl + (if (url.startsWith("/")) "" else "/") + url
        }

    /** Sends the request; a non-2xx answer throws [HttpException]. */
    private suspend fun <T> send(
        url: String,
        m: MediaRequest,
        read: suspend (HttpResponse) -> T,
    ): T =
        exchange(url, m) { res ->
            val status = res.status.value
            if (status / 100 != 2) throw httpError(status, url, res.bodyAsText())
            read(res)
        }

    /** Sends the request and hands any answer to [read]; transport failures become [TransportException]. */
    private suspend fun <T> exchange(
        url: String,
        m: MediaRequest,
        read: suspend (HttpResponse) -> T,
    ): T {
        try {
            val statement =
                http.prepareRequest {
                    method = if (m.method == MediaMethod.HEAD) HttpMethod.Head else HttpMethod.Get
                    url(url)
                    expectSuccess = false
                    header(HttpHeaders.UserAgent, USER_AGENT)
                    for ((k, v) in defaultHeaders) header(k, v)
                    for ((k, v) in m.headers) header(k, v)
                    header(HttpHeaders.Accept, m.accept)
                    m.range?.let { header(HttpHeaders.Range, it.header) }
                }
            return statement.execute { read(it) }
        } catch (e: PassThrough) {
            throw e.error
        } catch (e: XtandardException) {
            throw e
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            throw TransportException("${m.method} $url: ${e.message ?: e::class.simpleName}", e)
        }
    }

    /** Carries an exception of the caller's [mediaStream] block past the transport handling. */
    private class PassThrough(
        val error: Throwable,
    ) : RuntimeException(error)

    /** Static helpers. */
    public companion object {
        private const val USER_AGENT = "xtandard-image-kotlin"
        private val scheme = Regex("^[a-zA-Z][a-zA-Z0-9+.-]*:")

        /**
         * The `Range` header value of a byte range.
         *
         * @param range the range
         * @return e.g. `bytes=0-1023`
         */
        public fun rangeHeader(range: ByteRange): String = range.header

        internal fun httpError(
            status: Int,
            url: String,
            text: String,
        ): HttpException {
            val body =
                try {
                    XtandardJson.decodeFromString(ErrorResponse.serializer(), text)
                } catch (_: SerializationException) {
                    null
                } catch (_: IllegalArgumentException) {
                    null
                }
            val message = body?.error?.message ?: text.trim().ifEmpty { "HTTP $status" }
            return HttpException.of(status, url, message, body)
        }
    }
}
