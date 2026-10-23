package io.github.xantiagoma.xtandard.image.kmp

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** The README's URL builder examples, so they keep compiling and stay true. */
class ReadmeTest {
    private val keyHex = "943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881"
    private val saltHex = "520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5"

    @Test
    fun urlBuilder() {
        val img =
            ImgproxyUrlBuilder(
                baseUrl = "https://img.example.com",
                signing = SigningKey.of(keyHex, saltHex),
                encryption = EncryptionKey.of("1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1"),
            )
        val a =
            img.url(
                "s3://photos/cat.jpg",
                processingOptions {
                    rs("fill", 1200, 630)
                    g("sm")
                    q(80)
                },
                extension = "webp",
            )
        assertTrue(a.endsWith("/rs:fill:1200:630/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp"), a)
        val b =
            img.image(Sources.xiSource("photos", "2026/cat.jpg")) {
                resize("fit", 300, 300)
                blur(2)
                extension(ImageFormat.PNG)
            }
        val c =
            img
                .image(Sources.xiSource("photos", "2026/cat.jpg"))
                .resize("fit", 300, 300)
                .blur(2)
                .extension("png")
                .build()
        assertEquals(b, c)
        img.url("s3://photos/cat.jpg", listOf(processingOptions { rs("fit", 500, 500) }, processingOptions { bl(2) }))
        img.url("https://example.com/a b.jpg", processingOptions { w(300) }, sourceEncoding = SourceEncoding.PLAIN)
        img.url("s3://private/cat.jpg", processingOptions { w(300) }, sourceEncoding = SourceEncoding.ENCRYPTED)
        img.url("s3://docs/report.pdf", processingOptions { rs("fit", 800, 0) }, extension = "png", pdfPassword = "s3cret")
        img.infoUrl(
            "s3://photos/cat.jpg",
            infoOptions {
                size(true)
                format(true)
                dimensions(true)
            },
        )
        img.videoUrl(
            "s3://media/talk.mov",
            videoOptions {
                rs("fill", 640, 360)
                vs(12)
                vd(8)
            },
            VideoContainer.WEBM,
        )
        val v =
            img.video("s3://media/talk.mov") {
                start(12)
                duration(8)
                mute(true)
                extension("mp4")
            }
        assertTrue(v.startsWith("https://img.example.com/video/"), v)
        img.streamUrl("s3://media/talk.mov", StreamParams.hls(renditions = listOf(360, 720), segmentDuration = 4))
    }

    @Test
    fun queryDialect() {
        val q = QueryUrlBuilder(baseUrl = "https://img.example.com", signing = SigningKey.of(keyHex, saltHex))
        val u =
            q.url(
                "s3://photos/cat.jpg",
                queryParams {
                    w(300)
                    f("webp")
                },
                processingOptions { bl(2) },
            )
        assertTrue(u.startsWith("https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&f=webp&o=bl:2&xi_sig="), u)
        q.videoUrl(
            "s3://media/talk.mov",
            videoOptions {
                rs("fill", 640, 360)
                vs(1)
            },
            "webm",
        )
        q.streamUrl("s3://media/talk.mov", StreamParams.hls(listOf(360)))
        q.imaginaryUrl(ImaginaryEndpoint.RESIZE, "s3://photos/cat.jpg", imaginaryParams { width(300) })
        q.image("s3://photos/cat.jpg") {
            param("w", 300)
            bl(2)
        }
    }
}
