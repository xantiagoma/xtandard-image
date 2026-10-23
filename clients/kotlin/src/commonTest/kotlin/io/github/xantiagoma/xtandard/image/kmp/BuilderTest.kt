package io.github.xantiagoma.xtandard.image.kmp

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class BuilderTest {
    private val unsigned = ImgproxyUrlBuilder(unsignedSignature = "unsafe")

    @Test
    fun numbersAreWrittenLikeJavaScript() {
        val cases =
            listOf<Pair<Number, String>>(
                300 to "300",
                300L to "300",
                300.0 to "300",
                -0.0 to "0",
                0.1 to "0.1",
                1.5 to "1.5",
                1.1f to "1.1",
                0.3f to "0.3",
                1e-7 to "1e-7",
                1.5e-7 to "1.5e-7",
                0.000001 to "0.000001",
                1e21 to "1e+21",
                1.2345e22 to "1.2345e+22",
                123456789012345680000.0 to "123456789012345680000",
                9007199254740993.0 to "9007199254740992",
                0.1 + 0.2 to "0.30000000000000004",
                5e-324 to "5e-324",
                Double.MAX_VALUE to "1.7976931348623157e+308",
                -2.5 to "-2.5",
                (-12).toShort() to "-12",
                Long.MAX_VALUE to "9223372036854775807",
                100.0 / 3 to "33.333333333333336",
                2.0 / 3 to "0.6666666666666666",
                1e300 * 10 to "1e+301",
            )
        for ((n, want) in cases) assertEquals(want, Text.formatNumber(n, "test"), "format $n")
        for (bad in listOf(Double.NaN, Double.POSITIVE_INFINITY, Float.NEGATIVE_INFINITY)) {
            assertEquals(ErrorCode.INVALID_ARGUMENT, assertFailsWith<UrlBuildException> { Text.formatNumber(bad, "t") }.code)
        }
    }

    @Test
    fun objectAndFluentFormsAgree() {
        val o =
            processingOptions {
                resize("fill", 300, 400.0)
                quality(80)
                sharpen(0.5)
            }
        val a = unsigned.url("s3://bucket/cat.jpg", o, extension = "webp")
        val b =
            unsigned
                .image("s3://bucket/cat.jpg")
                .resize("fill", 300, 400)
                .quality(80)
                .sharpen(0.5)
                .extension(ImageFormat.WEBP)
                .build()
        val c =
            unsigned.image("s3://bucket/cat.jpg") {
                options(o)
                extension("webp")
            }
        assertEquals("/unsafe/rs:fill:300:400/q:80/sh:0.5/czM6Ly9idWNrZXQvY2F0LmpwZw.webp", a)
        assertEquals(a, b)
        assertEquals(a, c)
    }

    @Test
    fun nullsAndBooleans() {
        assertEquals(
            "/unsafe/rs:fill:300::1/plain/https://example.com/a%20b.jpg@png",
            unsigned.url(
                "https://example.com/a b.jpg",
                ProcessingOptions().option("rs", "fill", 300, null, true),
                extension = "png",
                sourceEncoding = SourceEncoding.PLAIN,
            ),
        )
    }

    @Test
    fun namesStyles() {
        val o = ProcessingOptions().resize("fit", 10, 10).q(5)
        assertEquals("/unsafe/rs:fit:10:10/q:5/YQ", unsigned.url("a", o))
        assertEquals(
            "/unsafe/resize:fit:10:10/quality:5/YQ",
            ImgproxyUrlBuilder(unsignedSignature = "unsafe", optionNames = OptionNameStyle.CANONICAL).url("a", o),
        )
        assertEquals(
            "/unsafe/resize:fit:10:10/q:5/YQ",
            ImgproxyUrlBuilder(unsignedSignature = "unsafe", optionNames = OptionNameStyle.GIVEN).url("a", o),
        )
    }

    @Test
    fun chainedPipelinesAndBaseUrl() {
        val b = ImgproxyUrlBuilder(baseUrl = "https://img.example.com//")
        assertEquals(
            "https://img.example.com/insecure/rs:fit:500:500/-/bl:2/YQ",
            b.url("a", listOf(processingOptions { rs("fit", 500, 500) }, ProcessingOptions(), processingOptions { bl(2) })),
        )
    }

    @Test
    fun infoVideoAndStreams() {
        assertEquals(
            "/info/unsafe/s:1/f:1/YQ",
            unsigned.infoUrl(
                "a",
                infoOptions {
                    size(true)
                    format(true)
                },
            ),
        )
        assertEquals(
            "/video/unsafe/vs:12/vd:8/YQ.webm",
            unsigned.videoUrl(
                "a",
                videoOptions {
                    start(12)
                    duration(8)
                },
                VideoContainer.WEBM,
            ),
        )
        assertEquals("/video/unsafe/st:hls:360:720/sgd:4/YQ.m3u8", unsigned.streamUrl("a", StreamParams.hls(listOf(360, 720), 4)))
        assertEquals("/video/unsafe/sgf:ts/YQ.m3u8", unsigned.streamUrl("a", StreamParams.hls(segmentFormat = SegmentFormat.TS)))
        val e = assertFailsWith<UrlBuildException> { unsigned.streamUrl("a", StreamParams.dash(segmentFormat = SegmentFormat.TS)) }
        assertEquals(ErrorCode.INVALID_REQUEST, e.code)
        val f =
            assertFailsWith<UrlBuildException> { unsigned.streamUrl("a", StreamParams.hls(options = videoOptions { stream("hls", 360) })) }
        assertEquals(ErrorCode.INVALID_REQUEST, f.code)
    }

    @Test
    fun queryDialect() {
        val q = QueryUrlBuilder(baseUrl = "https://img.example.com")
        assertEquals(
            "https://img.example.com/q/?url=s3://bucket/cat.jpg&w=300&f=webp&o=bl:2",
            q.url(
                "s3://bucket/cat.jpg",
                queryParams {
                    w(300)
                    f("webp")
                },
                processingOptions { bl(2) },
            ),
        )
        assertEquals(
            "https://img.example.com/q/?url=s3://bucket/cat.jpg&w=300&o=bl:2",
            q.image("s3://bucket/cat.jpg") {
                param("w", 300)
                param("h", null)
                bl(2)
            },
        )
        assertEquals(
            "https://img.example.com/q/video?url=a&c=10,10&vs=1&format=webm",
            q.videoUrl(
                "a",
                videoOptions {
                    crop(10, 10)
                    vs(1)
                },
                "webm",
            ),
        )
        assertEquals(
            "https://img.example.com/q/resize?url=a&width=300&force=true",
            q.imaginaryUrl(
                ImaginaryEndpoint.RESIZE,
                "a",
                imaginaryParams {
                    width(300)
                    force(true)
                },
            ),
        )
        assertEquals(ErrorCode.INVALID_ENDPOINT, assertFailsWith<UrlBuildException> { q.imaginaryUrl("video", "a") }.code)
        val signed = QueryUrlBuilder(signing = SigningKey.of("00", "00")).url("a", queryParams { w(1) })
        assertTrue(signed.startsWith("/q/?url=a&w=1&xi_sig="), signed)
        assertEquals(signed, Signing.signXiUrl(signed, SigningKey.of("00", "00"), "/q"))
    }

    @Test
    fun errorsCarryTheirCode() {
        fun code(f: () -> Unit) = assertFailsWith<UrlBuildException> { f() }.code
        assertEquals(ErrorCode.UNKNOWN_OPTION, code { unsigned.url("a", ProcessingOptions().option("nope", 1)) })
        assertEquals(ErrorCode.INVALID_ARGUMENT, code { unsigned.url("a", ProcessingOptions().option("q")) })
        assertEquals(ErrorCode.INVALID_ARGUMENT, code { unsigned.url("a", ProcessingOptions().option("wm", "a/b")) })
        assertEquals(ErrorCode.INVALID_ARGUMENT, code { unsigned.url("a", ProcessingOptions().option("wm", "a\u00a0b")) })
        assertEquals(ErrorCode.INVALID_ARGUMENT, code { unsigned.url("a", ProcessingOptions().option("wm", listOf(1))) })
        assertEquals(ErrorCode.INVALID_SOURCE, code { unsigned.url("") })
        assertEquals(ErrorCode.INVALID_EXTENSION, code { unsigned.url("a", extension = "we bp") })
        assertEquals(ErrorCode.INVALID_SEPARATOR, code { ImgproxyUrlBuilder(separator = "/").url("a") })
        assertEquals(ErrorCode.MISSING_ENCRYPTION, code { unsigned.url("a", sourceEncoding = SourceEncoding.ENCRYPTED) })
        assertEquals(ErrorCode.MISSING_ENCRYPTION, code { unsigned.url("a", pdfPassword = "x") })
        val enc = EncryptionKey.of("00".repeat(32))
        assertEquals(ErrorCode.MISSING_SIGNING, code { ImgproxyUrlBuilder(encryption = enc).url("a", pdfPassword = "x") })
        assertEquals(ErrorCode.INVALID_NAME, code { Sources.xiSource("-bad", "k") })
        assertEquals(ErrorCode.INVALID_RANGE, code { ByteRange.Between(5, 1) })
    }

    @Test
    fun sourcesAndEscapes() {
        assertEquals("xi://photos/2026/cat.jpg", Sources.xiSource("photos", "/2026/cat.jpg"))
        assertEquals("s3://bucket/k", Sources.sourceUrl(SourceScheme.S3, "bucket", "k"))
        assertEquals("local:///p", Sources.sourceUrl("local", "", "/p"))
        assertEquals("a%20b%C3%A9%F0%9F%98%80%EF%BF%BD", Sources.escapePlainSource("a bé\uD83D\uDE00\uD800"))
        assertEquals("a:/,%26", Sources.escapeQueryValue("a:/,&"))
    }

    @Test
    fun queryMethodRequestSplitsAtTheFirstQuestionMark() {
        assertEquals(
            QueryMethodRequest("QUERY", "https://img.example.com/q/", QueryMethodRequest.CONTENT_TYPE, "url=s3://b/a.jpg?x=1&w=3"),
            queryMethodRequest("https://img.example.com/q/?url=s3://b/a.jpg?x=1&w=3"),
        )
        assertEquals("application/x-www-form-urlencoded", QueryMethodRequest.CONTENT_TYPE)
        for (bad in listOf("/q/", "/q/?", "/q/?url=a#f")) {
            assertEquals(ErrorCode.INVALID_REQUEST, assertFailsWith<UrlBuildException> { queryMethodRequest(bad) }.code, bad)
        }
    }

    @Test
    fun splitSources() {
        val b = ImgproxyUrlBuilder(unsignedSignature = "unsafe", splitSource = 4)
        assertEquals("/unsafe/aHR0/cHM6/Ly9l/LmNv/bS9h/Lmpw/Zw.png", b.url("https://e.com/a.jpg", extension = "png"))
    }
}
