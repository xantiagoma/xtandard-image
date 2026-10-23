package io.github.xantiagoma.xtandard.image.kmp

import io.github.xantiagoma.xtandard.image.kmp.Fixtures.int
import io.github.xantiagoma.xtandard.image.kmp.Fixtures.list
import io.github.xantiagoma.xtandard.image.kmp.Fixtures.obj
import io.github.xantiagoma.xtandard.image.kmp.Fixtures.str
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import kotlin.test.fail

/**
 * clients/spec/conformance.json: the cases every official client runs (ADR 0013).
 *
 * Positive cases must produce expect.value byte for byte; error cases must throw [UrlBuildException] with
 * expect.error as its code. Each case is built through the object form and, where one exists, the fluent chain;
 * both must agree.
 */
class ConformanceTest {
    private val fixture = Fixtures.load("clients/spec/conformance.json")
    private val keys = obj(fixture["keys"])
    private val cases = list(fixture["cases"]).map(::obj)

    @Test
    fun fixtureCoversEveryOp() {
        assertTrue(cases.map { it["op"] }.distinct().size >= 17)
        assertTrue(cases.size >= 100, "cases: ${cases.size}")
    }

    @Test
    fun cases() {
        val out = Outcomes("conformance.json")
        for (c in cases) out.run("${c["op"]}:${c["name"]}") { run(c) }
        out.finish()
        // Three cases need a second /info or /video pipeline or a /video PDF password, which the types rule out.
        assertEquals(3, out.skipped.size, out.skipped.joinToString("\n"))
    }

    private fun run(c: Map<String, Any?>) {
        val expect = obj(c["expect"])
        val error = str(expect, "error")
        if (error != null) {
            val e = buildError(c)
            assertEquals(error, e.code.code, e.message)
            if (fluentReaches(c)) {
                val f =
                    try {
                        fluent(c)
                        null
                    } catch (x: UrlBuildException) {
                        x
                    } ?: fail("fluent form: want $error, got a URL")
                assertEquals(error, f.code.code, "fluent: ${f.message}")
            }
            return
        }
        val want = str(expect, "value")
        assertEquals(want, mapping(c))
        fluent(c)?.let { assertEquals(want, it, "fluent form") }
    }

    private fun buildError(c: Map<String, Any?>): UrlBuildException {
        val got =
            try {
                mapping(c)
            } catch (e: UrlBuildException) {
                return e
            }
        fail("want an error, got $got")
    }

    private fun fluentReaches(c: Map<String, Any?>): Boolean =
        try {
            fluent(c) != null
        } catch (_: UrlBuildException) {
            true
        } catch (_: NotExpressible) {
            false
        }

    // ---- configuration ----------------------------------------------------------------------

    private fun config(c: Map<String, Any?>): Map<String, Any?> = c["config"]?.let(::obj) ?: emptyMap()

    private fun signing(cfg: Map<String, Any?>): SigningKey? {
        val s = cfg["signing"] ?: return null
        val k = if (s == true) obj(keys["signing"]) else obj(s)
        val key = SigningKey.of(str(k, "key")!!, str(k, "salt")!!)
        return int(cfg, "signature_size")?.let { key.withSize(it) } ?: key
    }

    private fun encryption(cfg: Map<String, Any?>): EncryptionKey? {
        val e = cfg["encryption"] ?: return null
        val k = if (e == true) obj(keys["encryption"]) else obj(e)
        val key = EncryptionKey.of(str(k, "key")!!)
        return str(k, "iv")?.let { key.withIv(it) } ?: key
    }

    private fun encoding(v: Any?): SourceEncoding? = (v as String?)?.let { SourceEncoding.of(it) ?: fail("unknown encoding $it") }

    private fun style(v: Any?): OptionNameStyle = OptionNameStyle.valueOf((v as String).uppercase())

    private fun imgproxy(cfg: Map<String, Any?>): ImgproxyUrlBuilder =
        ImgproxyUrlBuilder(
            baseUrl = str(cfg, "base_url") ?: "",
            signing = signing(cfg),
            unsignedSignature = str(cfg, "unsigned_signature") ?: Constants.DEFAULT_UNSIGNED,
            encryption = encryption(cfg),
            sourceEncoding = encoding(cfg["source_encoding"]) ?: SourceEncoding.BASE64,
            splitSource = int(cfg, "split_source") ?: 0,
            separator = str(cfg, "separator") ?: Constants.DEFAULT_SEPARATOR,
            optionNames = cfg["option_names"]?.let(::style) ?: OptionNameStyle.SHORT,
        )

    private fun query(cfg: Map<String, Any?>): QueryUrlBuilder =
        QueryUrlBuilder(
            baseUrl = str(cfg, "base_url") ?: "",
            mount = str(cfg, "mount") ?: Constants.QUERY_MOUNT,
            signing = signing(cfg),
            encryption = encryption(cfg),
            separator = str(cfg, "separator") ?: Constants.DEFAULT_SEPARATOR,
            optionNames = cfg["option_names"]?.let(::style) ?: OptionNameStyle.SHORT,
        )

    // ---- inputs -----------------------------------------------------------------------------

    /** The pipelines: each a list of (name, arguments) pairs. */
    private fun pipelines(input: Map<String, Any?>): List<List<Any?>> = input["options"]?.let { list(it).map(::list) } ?: emptyList()

    private fun <O : OptionList<O>> fill(
        options: O,
        pipeline: List<Any?>,
    ): O {
        for (e in pipeline) {
            val pair = list(e)
            options.option(pair[0] as String, *list(pair[1]).toTypedArray())
        }
        return options
    }

    private fun <O : OptionList<O>> single(
        input: Map<String, Any?>,
        empty: O,
        what: String,
    ): O? {
        val p = pipelines(input)
        if (p.size > 1) throw NotExpressible("$what takes one options list; chained pipelines are not in its type")
        return if (p.isEmpty()) null else fill(empty, p[0])
    }

    private fun stream(input: Map<String, Any?>): StreamParams =
        StreamParams(
            protocol = str(input, "protocol")!!,
            renditions = input["renditions"]?.let { r -> list(r).map { (it as Number).toInt() } } ?: emptyList(),
            segmentDuration = int(input, "segment_duration"),
            segmentFormat = str(input, "segment_format"),
            options = if (input["options"] != null) single(input, VideoOptions(), "streamUrl") else null,
            sourceEncoding = encoding(input["source_encoding"]),
        )

    private fun <P : ParamList<P>> params(
        input: Map<String, Any?>,
        empty: P,
    ): P {
        input["params"]?.let { p -> list(p).forEach { e -> list(e).let { empty.param(it[0] as String, it[1]) } } }
        return empty
    }

    private fun range(r: Any?): ByteRange {
        if (r is String) return ByteRange.Raw(r)
        val m = obj(r)
        m["suffix"]?.let { return ByteRange.Suffix((it as Number).toLong()) }
        val start = (m["start"] as Number).toLong()
        return m["end"]?.let { ByteRange.Between(start, (it as Number).toLong()) } ?: ByteRange.From(start)
    }

    // ---- the two forms ----------------------------------------------------------------------

    /** The case through the object form. */
    private fun mapping(c: Map<String, Any?>): String {
        val cfg = config(c)
        val input = obj(c["input"])
        val source = str(input, "source") ?: ""
        val ext = str(input, "extension")
        val enc = encoding(input["source_encoding"])
        val pdp = str(input, "pdf_password")
        return when (val op = c["op"]) {
            "imgproxy.url" -> {
                imgproxy(cfg).url(source, pipelines(input).map { fill(ProcessingOptions(), it) }, ext, enc, pdp)
            }

            "imgproxy.info_url" -> {
                imgproxy(cfg).infoUrl(source, single(input, InfoOptions(), "infoUrl"), enc, pdp)
            }

            "imgproxy.video_url" -> {
                if (pdp != null) throw NotExpressible("videoUrl has no PDF password parameter")
                imgproxy(cfg).videoUrl(source, single(input, VideoOptions(), "videoUrl"), ext, enc)
            }

            "imgproxy.stream_url" -> {
                imgproxy(cfg).streamUrl(source, stream(input))
            }

            "query.url" -> {
                query(cfg).url(source, params(input, QueryParams()), pipelines(input).map { fill(ProcessingOptions(), it) }, ext, pdp)
            }

            "query.video_url" -> {
                query(cfg).videoUrl(source, pipelines(input).map { fill(VideoOptions(), it) }, ext)
            }

            "query.stream_url" -> {
                query(cfg).streamUrl(source, stream(input))
            }

            "query.imaginary_url" -> {
                query(cfg).imaginaryUrl(str(input, "endpoint")!!, source, params(input, ImaginaryParams()))
            }

            "query.query_request" -> {
                // input.build names the query op that builds the URL from the other inputs.
                val q = queryMethodRequest(mapping(c + ("op" to str(input, "build"))))
                "${q.method} ${q.url}\nContent-Type: ${q.contentType}\n\n${q.body}"
            }

            "sign" -> {
                Signing.sign(str(input, "message")!!, signing(cfg)!!)
            }

            "sign_xi_url" -> {
                Signing.signXiUrl(str(input, "url")!!, signing(cfg)!!, str(input, "mount") ?: "")
            }

            "encrypt_source" -> {
                Encryption.encryptSource(source, encryption(cfg)!!)
            }

            "encrypt_pdf_password" -> {
                Encryption.encryptPdfPassword(str(input, "password")!!, encryption(cfg)!!)
            }

            "xi_source" -> {
                Sources.xiSource(str(input, "name")!!, str(input, "key")!!)
            }

            "source_url" -> {
                Sources.sourceUrl(str(input, "scheme")!!, str(input, "authority")!!, str(input, "key")!!)
            }

            "range_header" -> {
                XtandardClient.rangeHeader(range(input["range"]))
            }

            "escape_plain_source" -> {
                Sources.escapePlainSource(str(input, "value")!!)
            }

            "escape_query_value" -> {
                Sources.escapeQueryValue(str(input, "value")!!)
            }

            else -> {
                fail("unknown op $op")
            }
        }
    }

    /** The case through the fluent chains, for the ops that have one; null otherwise. */
    private fun fluent(c: Map<String, Any?>): String? {
        val cfg = config(c)
        val input = obj(c["input"])
        val source = str(input, "source") ?: ""
        val pipelines = pipelines(input)
        val ext = str(input, "extension")
        val enc = encoding(input["source_encoding"])
        val pdp = str(input, "pdf_password")
        return when (c["op"]) {
            "imgproxy.url" -> {
                imgproxy(cfg).image(source) {
                    pipelines.forEachIndexed { i, p ->
                        if (i > 0) pipeline()
                        addAll(this, p)
                    }
                    enc?.let { encoding(it) }
                    ext?.let { extension(it) }
                    pdp?.let { withPdfPassword(it) }
                }
            }

            "imgproxy.info_url" -> {
                if (pipelines.size > 1) throw NotExpressible("the /info chain has no pipeline()")
                imgproxy(cfg).info(source) {
                    pipelines.forEach { addAll(this, it) }
                    enc?.let { encoding(it) }
                    pdp?.let { withPdfPassword(it) }
                }
            }

            "imgproxy.video_url" -> {
                if (pipelines.size > 1 || pdp != null) throw NotExpressible("the /video chain has no pipeline() and no withPdfPassword()")
                imgproxy(cfg).video(source) {
                    pipelines.forEach { addAll(this, it) }
                    enc?.let { encoding(it) }
                    ext?.let { extension(it) }
                }
            }

            "query.url" -> {
                query(cfg).image(source) {
                    input["params"]?.let { p -> list(p).forEach { e -> list(e).let { param(it[0] as String, it[1]) } } }
                    pipelines.forEachIndexed { i, p ->
                        if (i > 0) pipeline()
                        addAll(this, p)
                    }
                    ext?.let { extension(it) }
                    pdp?.let { withPdfPassword(it) }
                }
            }

            "query.video_url" -> {
                query(cfg).video(source) {
                    pipelines.forEach { addAll(this, it) }
                    ext?.let { extension(it) }
                }
            }

            else -> {
                null
            }
        }
    }

    private fun addAll(
        chain: UrlChain<*>,
        pipeline: List<Any?>,
    ) {
        for (e in pipeline) {
            val pair = list(e)
            chain.option(pair[0] as String, *list(pair[1]).toTypedArray())
        }
    }
}
