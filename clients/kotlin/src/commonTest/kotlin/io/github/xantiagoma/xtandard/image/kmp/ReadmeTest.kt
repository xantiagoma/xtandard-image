package io.github.xantiagoma.xtandard.image.kmp

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** The README's URL builder examples, so they keep compiling and stay true. */
class ReadmeTest {
    private val signing =
        SigningKey.of(
            "943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881",
            "520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5",
        )
    private val source = "https://images.unsplash.com/photo-1506744038136-46273834b3fb"

    @Test
    fun quickStart() {
        val img = ImgproxyUrlBuilder(baseUrl = "http://localhost:8080", signing = signing)
        val url =
            img
                .image(source)
                .resize("fill", 1200, 630)
                .gravity("sm")
                .extension("jpg")
                .build()
        assertTrue(url.startsWith("http://localhost:8080/"), url)
        assertTrue(url.contains("/rs:fill:1200:630/g:sm/aHR0cHM6"), url)
        assertTrue(url.endsWith(".jpg"), url)
    }

    @Test
    fun buildingUrls() {
        val img = ImgproxyUrlBuilder(baseUrl = "https://img.example.com")
        val want = "https://img.example.com/insecure/rs:fill:300:400/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp"
        val a =
            img.url(
                "s3://photos/cat.jpg",
                processingOptions {
                    resize("fill", 300, 400)
                    gravity("sm")
                    quality(80)
                },
                extension = "webp",
            )
        val b =
            img.image("s3://photos/cat.jpg") {
                resize("fill", 300, 400)
                g("sm")
                quality(80)
                extension("webp")
            }
        val c =
            img
                .image("s3://photos/cat.jpg")
                .resize("fill", 300, 400)
                .g("sm")
                .quality(80)
                .extension("webp")
                .build()
        assertEquals(want, a)
        assertEquals(want, b)
        assertEquals(want, c)
    }

    @Test
    fun usage() {
        val img = ImgproxyUrlBuilder(baseUrl = "https://img.example.com")
        val chained =
            img.url("s3://photos/cat.jpg", listOf(processingOptions { rs("fit", 500, 500) }, processingOptions { bl(2) }))
        assertEquals(
            chained,
            img
                .image("s3://photos/cat.jpg")
                .rs("fit", 500, 500)
                .pipeline()
                .bl(2)
                .build(),
        )
        assertTrue(chained.contains("/rs:fit:500:500/-/bl:2/"), chained)

        val plain = img.url("https://example.com/a b.jpg", processingOptions { w(300) }, sourceEncoding = SourceEncoding.PLAIN)
        assertTrue(plain.endsWith("/plain/https://example.com/a%20b.jpg"), plain)
        assertEquals("xi://photos/2026/cat.jpg", Sources.xiSource("photos", "2026/cat.jpg"))
        assertEquals("s3://bucket/cat.jpg", Sources.sourceUrl("s3", "bucket", "cat.jpg"))

        val info =
            img.infoUrl(
                "s3://photos/cat.jpg",
                infoOptions {
                    size(true)
                    format(true)
                },
            )
        assertEquals(
            info,
            img
                .info("s3://photos/cat.jpg")
                .size(true)
                .format(true)
                .build(),
        )

        val video =
            img.video("s3://media/talk.mov") {
                rs("fill", 640, 360)
                vs(12)
                vd(5)
                extension("webm")
            }
        assertTrue(video.startsWith("https://img.example.com/video/"), video)
        val stream = img.streamUrl("s3://media/talk.mov", StreamParams.hls(renditions = listOf(360, 720), segmentDuration = 4))
        assertTrue(stream.contains("/st:hls:360:720/sgd:4/") && stream.endsWith(".m3u8"), stream)
    }

    @Test
    fun queryDialect() {
        val q = QueryUrlBuilder(baseUrl = "https://img.example.com", signing = signing)
        val u =
            q.url(
                "s3://photos/cat.jpg",
                queryParams {
                    w(300)
                    fit("cover")
                },
                processingOptions { bl(2) },
                extension = "webp",
            )
        assertTrue(u.startsWith("https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&fit=cover&o=bl:2&format=webp&xi_sig="), u)
        val i =
            q.imaginaryUrl(
                ImaginaryEndpoint.SMARTCROP,
                "s3://photos/cat.jpg",
                imaginaryParams {
                    width(300)
                    height(300)
                },
            )
        assertTrue(i.startsWith("https://img.example.com/q/smartcrop?url=s3://photos/cat.jpg&width=300&height=300&xi_sig="), i)
    }

    @Test
    fun signingAndEncryption() {
        val img =
            ImgproxyUrlBuilder(
                baseUrl = "https://img.example.com",
                signing = signing,
                encryption = EncryptionKey.of("1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1"),
            )
        val enc = img.url("s3://private/cat.jpg", processingOptions { w(300) }, sourceEncoding = SourceEncoding.ENCRYPTED)
        assertTrue(enc.contains("/w:300/enc/"), enc)
        val pdf = img.url("s3://docs/contract.pdf", processingOptions { rs("fit", 800, 0) }, pdfPassword = "s3cret")
        assertTrue(pdf.contains("/pdp:"), pdf)
    }
}
