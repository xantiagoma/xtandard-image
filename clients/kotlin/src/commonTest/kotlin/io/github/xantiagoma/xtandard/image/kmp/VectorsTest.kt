package io.github.xantiagoma.xtandard.image.kmp

import io.github.xantiagoma.xtandard.image.kmp.Fixtures.int
import io.github.xantiagoma.xtandard.image.kmp.Fixtures.list
import io.github.xantiagoma.xtandard.image.kmp.Fixtures.obj
import io.github.xantiagoma.xtandard.image.kmp.Fixtures.str
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** testdata/security/vectors.json: the signing, enc/ and pdf_password vectors shared with Go and TypeScript. */
class VectorsTest {
    private val v = Fixtures.load("testdata/security/vectors.json")

    private fun each(
        group: String,
        check: (Map<String, Any?>) -> Unit,
    ) {
        val out = Outcomes("vectors.json $group")
        val cases = list(v[group]).map(::obj)
        assertTrue(cases.isNotEmpty())
        for (c in cases) out.run(str(c, "name")!!) { check(c) }
        out.finish()
    }

    @Test
    fun imgproxy() =
        each("imgproxy") { c ->
            val k = SigningKey.of(str(c, "key")!!, str(c, "salt")!!, int(c, "size")!!)
            val path = str(c, "path")!!
            assertEquals(str(c, "signature"), Signing.sign(path, k))
            val p = if (path.startsWith("/")) path else "/$path"
            assertEquals("/" + str(c, "signature") + p, Signing.signImgproxyPath(path, k))
            val raw = SigningKey.of(Text.hex(str(c, "key")!!, "k"), Text.hex(str(c, "salt")!!, "s"), int(c, "size")!!)
            assertEquals(str(c, "signature"), Signing.sign(path, raw))
        }

    @Test
    fun xiSig() =
        each("xi_sig") { c ->
            val k = SigningKey.of(str(c, "key")!!, str(c, "salt")!!, int(c, "size")!!)
            val path = str(c, "path")!!
            val signed = str(c, "signed_query")!!
            assertEquals(signed, Signing.signXiQuery(path, str(c, "query")!!, k))
            assertEquals(signed, Signing.signXiQuery(path, signed, k))
            assertEquals(
                "https://img.example.com$path?$signed",
                Signing.signXiUrl("https://img.example.com$path?" + str(c, "query"), k),
            )
        }

    @Test
    fun encryption() =
        each("encryption") { c ->
            val key = EncryptionKey.of(str(c, "key")!!).withIv(str(c, "iv")!!)
            assertEquals(str(c, "token"), Encryption.encryptSource(str(c, "plaintext")!!, key))
        }

    @Test
    fun pdfPassword() =
        each("pdf_password") { c ->
            val key = EncryptionKey.of(str(c, "key")!!).withIv(str(c, "iv")!!)
            assertEquals(str(c, "token"), Encryption.encryptPdfPassword(str(c, "password")!!, key))
        }

    @Test
    fun randomIv() {
        val key = EncryptionKey.of("1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1")
        val a = Encryption.encryptPdfPassword("s3cret", key)
        val b = Encryption.encryptPdfPassword("s3cret", key)
        assertNotEquals(a, b)
        assertEquals(a.length, b.length)
    }

    @Test
    fun emptyHmacKey() {
        // RFC 2104 pads keys with zeros: an empty key and a one-zero-byte key are the same key.
        val a = Signing.sign("/x", SigningKey.of(ByteArray(0), ByteArray(0)))
        val b = Signing.sign("/x", SigningKey.of(ByteArray(1), ByteArray(0)))
        assertEquals(a, b)
    }

    @Test
    fun xiMessage() {
        assertEquals(Signing.XiMessage("s", "/?url=x&w=1"), Signing.xiMessage("/", "url=x&&xi_sig=s&w=1"))
        val none = Signing.xiMessage("/", "")
        assertNull(none.signature)
        assertEquals("/", none.message)
        val e = assertFailsWith<UrlBuildException> { Signing.xiMessage("/", "xi_sig=a&xi_sig=b") }
        assertEquals(ErrorCode.INVALID_REQUEST, e.code)
    }

    @Test
    fun keysHideTheirSecrets() {
        val k = SigningKey.of("00".repeat(32), "11".repeat(32))
        assertFalse("00" in k.toString())
        assertFalse("11" in k.toString())
        val e = EncryptionKey.of("ab".repeat(32))
        assertFalse("ab" in e.toString())
        assertEquals(listOf(16, 24, 32), Constants.ENCRYPTION_KEY_SIZES)
    }

    @Test
    fun hexKeysAreStrict() {
        for (bad in listOf("0", "zz", "0g", "１２")) {
            val e = assertFailsWith<UrlBuildException> { SigningKey.of(bad, "00") }
            assertEquals(ErrorCode.INVALID_KEY, e.code)
        }
        SigningKey.of(" 0A0b ", "")
        assertEquals(ErrorCode.INVALID_KEY, assertFailsWith<UrlBuildException> { EncryptionKey.of("00".repeat(15)) }.code)
        assertEquals(ErrorCode.INVALID_KEY, assertFailsWith<UrlBuildException> { EncryptionKey.of("00".repeat(16)).withIv("00") }.code)
        assertEquals(ErrorCode.INVALID_KEY, assertFailsWith<UrlBuildException> { SigningKey.of("00", "00", 33) }.code)
    }
}
