package io.github.xantiagoma.xtandard.image.kmp

import io.github.xantiagoma.xtandard.image.kmp.Fixtures.int
import io.github.xantiagoma.xtandard.image.kmp.Fixtures.list
import io.github.xantiagoma.xtandard.image.kmp.Fixtures.obj
import io.github.xantiagoma.xtandard.image.kmp.Fixtures.str
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * testdata/urls/imgproxy-builder.json: 400+ cases derived from the server's golden tables. The URLs were written by
 * the TypeScript client and parsed back by the Go dialect tests; every client must write the same ones: `url` with
 * option names as given, `url_short` with the default short names, and `query_url` where the case has one.
 */
class BuilderFixtureTest {
    private val fixture = Fixtures.load("testdata/urls/imgproxy-builder.json")
    private val cases = list(fixture["cases"]).map(::obj)

    @Test
    fun coversTheTables() {
        assertTrue(cases.size >= 400, "cases: ${cases.size}")
    }

    @Test
    fun cases() {
        val out = Outcomes("imgproxy-builder.json")
        for (c in cases) {
            out.run(str(c, "name")!!) {
                assertEquals(str(c, "url"), imgproxy(c, OptionNameStyle.GIVEN), "url (names as given)")
                assertEquals(str(c, "url_short"), imgproxy(c, OptionNameStyle.SHORT), "url_short")
                if (c["query"] == true) assertEquals(str(c, "query_url"), query(c), "query_url")
            }
        }
        out.finish()
        assertEquals(0, out.skipped.size)
    }

    private fun signing(c: Map<String, Any?>): SigningKey? {
        val size = int(c, "signature_size")
        if (size == null || size == 0) return null
        val k = obj(fixture["signing"])
        return SigningKey.of(str(k, "key")!!, str(k, "salt")!!, size)
    }

    private fun addAll(
        chain: UrlChain<*>,
        pipeline: Any?,
    ) {
        for (entry in list(pipeline)) {
            for ((name, args) in obj(entry)) chain.option(name, *list(args).toTypedArray())
        }
    }

    private fun imgproxy(
        c: Map<String, Any?>,
        style: OptionNameStyle,
    ): String {
        val enc = obj(fixture["encryption"])
        val b =
            ImgproxyUrlBuilder(
                unsignedSignature = "unsafe",
                encryption = EncryptionKey.of(str(enc, "key")!!).withIv(str(enc, "iv")!!),
                signing = signing(c),
                separator = str(c, "separator") ?: ":",
                optionNames = style,
            )
        val encoding = SourceEncoding.of(str(c, "encoding")!!)!!
        val pipelines = list(c["pipelines"])
        val ext = str(c, "extension")?.takeIf { it.isNotEmpty() }
        val source = str(c, "source")!!
        return when (str(c, "endpoint")) {
            "info" -> {
                b.info(source) {
                    encoding(encoding)
                    pipelines.forEach { addAll(this, it) }
                }
            }

            "video" -> {
                b.video(source) {
                    encoding(encoding)
                    pipelines.forEach { addAll(this, it) }
                    ext?.let { extension(it) }
                }
            }

            else -> {
                b.image(source) {
                    encoding(encoding)
                    pipelines.forEachIndexed { i, p ->
                        if (i > 0) pipeline()
                        addAll(this, p)
                    }
                    ext?.let { extension(it) }
                }
            }
        }
    }

    private fun query(c: Map<String, Any?>): String {
        val q = QueryUrlBuilder(signing = signing(c))
        val pipelines = list(c["pipelines"])
        val ext = str(c, "extension")?.takeIf { it.isNotEmpty() }
        val source = str(c, "source")!!
        if (str(c, "endpoint") == "video") {
            return q.video(source) {
                pipelines.forEach { addAll(this, it) }
                ext?.let { extension(it) }
            }
        }
        return q.image(source) {
            pipelines.forEachIndexed { i, p ->
                if (i > 0) pipeline()
                addAll(this, p)
            }
            ext?.let { extension(it) }
        }
    }
}
