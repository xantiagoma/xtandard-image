package io.github.xantiagoma.xtandard.image.kmp

import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.MockRequestHandleScope
import io.ktor.client.engine.mock.respond
import io.ktor.client.request.HttpRequestData
import io.ktor.client.request.HttpResponseData
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpMethod
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.utils.io.readBuffer
import kotlinx.coroutines.test.runTest
import kotlinx.io.readByteArray
import kotlinx.serialization.json.jsonPrimitive
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertIs
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** The HTTP client against Ktor's MockEngine, a stand-in server that records every request (every target). */
class ClientTest {
    private val requests = mutableListOf<HttpRequestData>()

    private fun client(handler: suspend MockRequestHandleScope.(HttpRequestData) -> HttpResponseData): XtandardClient {
        val engine =
            MockEngine { req ->
                requests += req
                handler(req)
            }
        return XtandardClient(
            "http://xi.test",
            headers = mapOf("X-Team" to "media"),
            httpClient = HttpClient(engine),
            playgroundBaseUrl = "http://admin.test",
        )
    }

    private val json = headersOf(HttpHeaders.ContentType, "application/json")

    @Test
    fun healthAndReadiness() =
        runTest {
            val xi =
                client { req ->
                    when (req.url.encodedPath) {
                        "/healthz" -> respond("ok")
                        else -> respond("draining\n", HttpStatusCode.ServiceUnavailable)
                    }
                }
            xi.healthz()
            assertEquals(Readiness(false, "draining"), xi.readyz())
            assertEquals("text/plain", requests[0].headers[HttpHeaders.Accept])
            assertEquals("xtandard-image-kotlin", requests[0].headers[HttpHeaders.UserAgent])
            assertEquals("media", requests[0].headers["X-Team"])
        }

    @Test
    fun infoIsDecoded() =
        runTest {
            val body = """{"width":640,"height":480,"format":"jpeg","size":1234,"alpha":false,"future":{"x":1},"exif":{"Make":"X"}}"""
            val xi = client { respond(body, headers = json) }
            val info =
                xi.info(
                    "s3://b/cat.jpg",
                    infoOptions {
                        size(true)
                        format(true)
                    },
                )
            assertEquals(640L, info.width)
            assertEquals("jpeg", info.format)
            assertEquals(mapOf("Make" to "X"), info.exif)
            assertNull(info.objects)
            assertEquals("http://xi.test/info/insecure/s:1/f:1/czM6Ly9iL2NhdC5qcGc", requests[0].url.toString())
            assertEquals("application/json", requests[0].headers[HttpHeaders.Accept])
        }

    @Test
    fun errorsAreTyped() =
        runTest {
            val envelope = """{"error":{"kind":"not_found","message":"source image not found","request_id":"r-1"}}"""
            val xi =
                client { req ->
                    when (req.url.encodedPath.substringAfterLast('/')) {
                        "bad" -> respond("teapot", HttpStatusCode(418, "I'm a teapot"))
                        "busy" -> respond("", HttpStatusCode.ServiceUnavailable)
                        else -> respond(envelope, HttpStatusCode.NotFound, json)
                    }
                }
            val e = assertFailsWith<HttpException.NotFound> { xi.image("s3://b/missing.jpg") }
            assertEquals(404, e.status)
            assertEquals("not_found", e.kind)
            assertEquals("r-1", e.requestId)
            assertEquals("source image not found", e.message)
            val other = assertFailsWith<HttpException> { xi.media("/bad") }
            assertEquals("teapot", other.message)
            assertEquals("unknown", other.kind)
            assertIs<HttpException.ServerError>(assertFailsWith<HttpException.ServiceUnavailable> { xi.media("busy") })
        }

    @Test
    fun rangeAndHead() =
        runTest {
            val xi =
                client { req ->
                    if (req.method == HttpMethod.Head) {
                        respond(
                            "",
                            headers =
                                headersOf(
                                    HttpHeaders.ContentType to listOf("video/webm"),
                                    HttpHeaders.ContentLength to listOf("1000"),
                                ),
                        )
                    } else {
                        respond(
                            ByteArray(10) { it.toByte() },
                            HttpStatusCode.PartialContent,
                            headersOf(HttpHeaders.ContentRange, "bytes 0-9/1000"),
                        )
                    }
                }
            val part =
                xi.video(
                    "s3://m/talk.mov",
                    videoOptions { duration(5) },
                    "webm",
                    request = MediaRequest(range = ByteRange.Between(0, 9)),
                )
            assertEquals(206, part.status)
            assertContentEquals(ByteArray(10) { it.toByte() }, part.body)
            assertEquals("bytes 0-9/1000", part.contentRange)
            assertEquals("bytes=0-9", requests[0].headers[HttpHeaders.Range])
            assertEquals("http://xi.test/video/insecure/vd:5/czM6Ly9tL3RhbGsubW92.webm", part.url)
            val head = xi.video("s3://m/talk.mov", null, "webm", request = MediaRequest(MediaMethod.HEAD, accept = "video/*"))
            assertEquals(HttpMethod.Head, requests[1].method)
            assertEquals("video/*", requests[1].headers[HttpHeaders.Accept])
            assertEquals("video/webm", head.contentType)
            assertEquals(1000L, head.contentLength)
        }

    @Test
    fun streamsAndTickets() =
        runTest {
            val playlist = "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n/video/_s/abc/360.m3u8\n"
            val xi =
                client { req ->
                    if (req.url.encodedPath.endsWith(".m3u8") && req.url.encodedPath.startsWith("/video/insecure")) {
                        respond(playlist, headers = headersOf(HttpHeaders.ContentType, "application/vnd.apple.mpegurl"))
                    } else {
                        respond(ByteArray(3))
                    }
                }
            val m = xi.stream("s3://m/talk.mov", StreamParams.hls(listOf(360)))
            assertTrue(m.body.startsWith("#EXTM3U"))
            assertEquals("application/vnd.apple.mpegurl", m.contentType)
            assertEquals("application/vnd.apple.mpegurl, */*;q=0.5", requests[0].headers[HttpHeaders.Accept])
            val child = m.body.lines().first { it.isNotEmpty() && !it.startsWith("#") }
            assertEquals(200, xi.media(child).status)
            assertEquals("http://xi.test/video/_s/abc/360.m3u8", requests[1].url.toString())
        }

    @Test
    fun mediaStreamReadsTheChannel() =
        runTest {
            val xi = client { respond(ByteArray(4096) { 7 }) }
            val n =
                xi.mediaStream("/insecure/YQ") { res ->
                    res.body
                        .readBuffer()
                        .readByteArray()
                        .size
                }
            assertEquals(4096, n)
            val boom = assertFailsWith<IllegalStateException> { xi.mediaStream("/insecure/YQ") { error("mine") } }
            assertEquals("mine", boom.message)
        }

    @Test
    fun transportFailures() =
        runTest {
            val xi = client { throw IllegalStateException("connection refused") }
            val e = assertFailsWith<TransportException> { xi.healthz() }
            assertTrue("connection refused" in e.message!!, e.message)
            val bad = client { respond("{not json", headers = json) }
            assertFailsWith<TransportException> { bad.info("a") }
            val unknown = client { respond("maybe") }
            assertFailsWith<TransportException> { unknown.readyz() }
        }

    @Test
    fun playgroundMeta() =
        runTest {
            val xi = client { respond("""{"version":"1.2.3"}""") }
            assertEquals("1.2.3", xi.playgroundMeta()["version"]!!.jsonPrimitive.content)
            assertEquals("http://admin.test/_playground/api/meta", requests[0].url.toString())
        }

    @Test
    fun urlErrorsAreNotSent() =
        runTest {
            val xi = client { respond("") }
            assertFailsWith<UrlBuildException> { xi.image("") }
            assertTrue(requests.isEmpty())
            assertEquals("bytes=-500", XtandardClient.rangeHeader(ByteRange.Suffix(500)))
        }
}
