package io.github.xantiagoma.xtandard.image.kmp

import io.ktor.utils.io.readBuffer
import kotlinx.coroutines.test.runTest
import org.junit.Assume
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue
import kotlin.time.Duration.Companion.minutes

/**
 * Smoke test against a running server, skipped unless XI_SMOKE_URL is set. The playground demo
 * (compose.playground.yml, unsigned URLs allowed) works: `XI_SMOKE_URL=http://127.0.0.1:8080 task test`. It needs the
 * demo's local fixtures (xi://local/...) and, for the /video cases, a full or gpl image with
 * XI_VIDEO_TRANSFORM_ENABLED.
 */
class SmokeTest {
    private val base = System.getenv("XI_SMOKE_URL").orEmpty()
    private val image = Sources.xiSource("local", "jpeg/baseline.jpg")
    private val video = Sources.xiSource("local", "video/bbb-h264-aac.mp4")

    private fun smoke(body: suspend () -> Unit) {
        Assume.assumeTrue("XI_SMOKE_URL is not set", base.isNotEmpty())
        runTest(timeout = 2.minutes) { body() }
    }

    @Test
    fun healthAndImage() =
        smoke {
            XtandardClient(base, sourceEncoding = SourceEncoding.PLAIN).use { xi ->
                xi.healthz()
                assertTrue(xi.readyz().ready)
                val res = xi.image(image, processingOptions { rs("fit", 64, 64) }, extension = "webp")
                assertEquals("image/webp", res.contentType)
                val info =
                    xi.info(
                        image,
                        infoOptions {
                            size(true)
                            format(true)
                            dimensions(true)
                        },
                    )
                assertEquals("jpeg", info.format)
                assertTrue((info.width ?: 0) > 0)
                assertFailsWith<HttpException.NotFound> { xi.image(Sources.xiSource("local", "jpeg/missing.jpg")) }
            }
        }

    @Test
    fun queryDialect() =
        smoke {
            val url =
                QueryUrlBuilder(baseUrl = base).url(
                    image,
                    queryParams {
                        w(32)
                        f("png")
                    },
                )
            XtandardClient(base).use { xi -> assertEquals("image/png", xi.media(url).contentType) }
        }

    @Test
    fun videoRangeAndStream() =
        smoke {
            XtandardClient(base).use { xi ->
                val o =
                    videoOptions {
                        vd(1)
                        w(160)
                    }
                val part = xi.video(video, o, "webm", request = MediaRequest(range = ByteRange.Between(0, 99)))
                assertTrue(part.status == 200 || part.status == 206, "status ${part.status}")
                val head = xi.video(video, o, "webm", request = MediaRequest(MediaMethod.HEAD))
                assertTrue(head.contentType!!.startsWith("video/webm"))
                val m = xi.stream(video, StreamParams.hls(listOf(240)))
                assertTrue(m.body.startsWith("#EXTM3U"))
                val child = m.body.lines().first { it.isNotEmpty() && !it.startsWith("#") }
                assertEquals(200, xi.media(child).status)
                val bytes = xi.mediaStream(child) { it.body.readBuffer().size }
                assertTrue(bytes > 0)
            }
        }
}
