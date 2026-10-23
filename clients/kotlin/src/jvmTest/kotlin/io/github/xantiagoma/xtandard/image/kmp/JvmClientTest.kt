package io.github.xantiagoma.xtandard.image.kmp

import com.sun.net.httpserver.HttpServer
import io.ktor.utils.io.readBuffer
import kotlinx.coroutines.test.runTest
import kotlinx.io.readByteArray
import java.net.InetAddress
import java.net.InetSocketAddress
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.spec.IvParameterSpec
import javax.crypto.spec.SecretKeySpec
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue
import kotlin.time.Duration.Companion.seconds

/**
 * The default HTTP client (OkHttp engine) against a real stand-in server, the JDK's HttpServer: method, Range,
 * HEAD, error envelopes, streaming and connection failures over a socket.
 */
class JvmClientTest {
    private lateinit var server: HttpServer
    private val seen = mutableListOf<String>()
    private val video = ByteArray(1000) { (it % 251).toByte() }

    @BeforeTest
    fun start() {
        server = HttpServer.create(InetSocketAddress(InetAddress.getLoopbackAddress(), 0), 0)
        server.createContext("/") { ex ->
            synchronized(seen) { seen += "${ex.requestMethod} ${ex.requestURI} range=${ex.requestHeaders.getFirst("Range")}" }
            val path = ex.requestURI.path
            when {
                path == "/healthz" -> {
                    ex.sendResponseHeaders(200, 2)
                    ex.responseBody.use { it.write("ok".toByteArray()) }
                }

                path.startsWith("/q/") -> {
                    // Echoes the method, content type and content (QUERY) or query (GET).
                    val content = ex.requestBody.use { it.readBytes().decodeToString() }
                    val echo = "${ex.requestMethod} ${ex.requestHeaders.getFirst(
                        "Content-Type",
                    )} ${content.ifEmpty { ex.requestURI.rawQuery }}"
                    val bytes = echo.toByteArray()
                    ex.sendResponseHeaders(200, bytes.size.toLong())
                    ex.responseBody.use { it.write(bytes) }
                }

                path.startsWith("/video/") -> {
                    ex.responseHeaders.add("Content-Type", "video/webm")
                    ex.responseHeaders.add("Accept-Ranges", "bytes")
                    val range = ex.requestHeaders.getFirst("Range")
                    when {
                        ex.requestMethod == "HEAD" -> {
                            ex.sendResponseHeaders(200, -1)
                        }

                        range == "bytes=0-99" -> {
                            ex.responseHeaders.add("Content-Range", "bytes 0-99/1000")
                            ex.sendResponseHeaders(206, 100)
                            ex.responseBody.use { it.write(video, 0, 100) }
                        }

                        else -> {
                            ex.sendResponseHeaders(200, video.size.toLong())
                            ex.responseBody.use { it.write(video) }
                        }
                    }
                }

                else -> {
                    val body = """{"error":{"kind":"unauthorized","message":"invalid signature","request_id":"abc"}}""".toByteArray()
                    ex.responseHeaders.add("Content-Type", "application/json")
                    ex.sendResponseHeaders(403, body.size.toLong())
                    ex.responseBody.use { it.write(body) }
                }
            }
        }
        server.start()
    }

    @AfterTest
    fun stop() = server.stop(0)

    private fun base() = "http://127.0.0.1:${server.address.port}"

    @Test
    fun overASocket() =
        runTest {
            XtandardClient(base(), timeout = 10.seconds).use { xi ->
                xi.healthz()
                val o = videoOptions { duration(1) }
                val part = xi.video("s3://m/a.mov", o, "webm", request = MediaRequest(range = ByteRange.Between(0, 99)))
                assertEquals(206, part.status)
                assertEquals(video.copyOf(100).toList(), part.body.toList())
                assertEquals("bytes 0-99/1000", part.contentRange)
                val head = xi.video("s3://m/a.mov", o, "webm", request = MediaRequest(MediaMethod.HEAD))
                assertEquals(0, head.body.size)
                assertEquals("video/webm", head.contentType)
                val streamed = xi.mediaStream(xi.urls.videoUrl("s3://m/a.mov", o, "webm")) { it.body.readBuffer().readByteArray() }
                assertEquals(video.toList(), streamed.toList())
                val e = assertFailsWith<HttpException.Forbidden> { xi.image("s3://b/cat.jpg") }
                assertEquals("unauthorized", e.kind)
                assertEquals("abc", e.requestId)
            }
            assertTrue(seen.any { it.startsWith("HEAD /video/insecure/vd:1/") }, seen.toString())
            assertTrue(seen.any { it.endsWith("range=bytes=0-99") }, seen.toString())
        }

    @Test
    fun queryMethodOverASocket() =
        runTest {
            val url = QueryUrlBuilder().url("s3://b/cat.jpg", queryParams { w(300) })
            val query = url.substringAfter('?')
            XtandardClient(base(), timeout = 10.seconds, queryMethod = true).use { xi ->
                assertEquals("QUERY application/x-www-form-urlencoded $query", xi.media(url).body.decodeToString())
                assertEquals("GET null $query", xi.media(url, MediaRequest(MediaMethod.GET)).body.decodeToString())
            }
            XtandardClient(base(), timeout = 10.seconds).use { xi ->
                assertEquals("GET null $query", xi.media(url).body.decodeToString())
                assertEquals(
                    "QUERY application/x-www-form-urlencoded $query",
                    xi.media(url, MediaRequest(MediaMethod.QUERY)).body.decodeToString(),
                )
            }
        }

    @Test
    fun connectionRefused() =
        runTest {
            val port = server.address.port
            server.stop(0)
            XtandardClient("http://127.0.0.1:$port", timeout = 5.seconds).use { xi ->
                assertFailsWith<TransportException> { xi.healthz() }
            }
        }

    @Test
    fun tokensDecrypt() {
        val keyHex = "1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1"
        val token = Encryption.encryptSource("s3://private/cat é.jpg", EncryptionKey.of(keyHex))
        val raw = Base64.getUrlDecoder().decode(token)
        val c = Cipher.getInstance("AES/CBC/PKCS5Padding")
        c.init(Cipher.DECRYPT_MODE, SecretKeySpec(Text.hex(keyHex, "k"), "AES"), IvParameterSpec(raw.copyOf(16)))
        assertEquals("s3://private/cat é.jpg", String(c.doFinal(raw, 16, raw.size - 16), Charsets.UTF_8))
    }
}
