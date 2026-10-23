package io.github.xantiagoma.xtandard.image.kmp

import kotlinx.io.buffered
import kotlinx.io.files.Path
import kotlinx.io.files.SystemFileSystem
import kotlinx.io.readString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull

/** The shared fixtures of the repository (clients/spec, testdata/), read by absolute path from [REPO_ROOT]. */
internal object Fixtures {
    fun load(relative: String): Map<String, Any?> {
        val text = SystemFileSystem.source(Path(REPO_ROOT, relative)).buffered().use { it.readString() }
        return obj(plain(Json.parseToJsonElement(text)))
    }

    /** JSON as plain values: maps, lists, strings, booleans, Long or Double (as JavaScript reads them), null. */
    fun plain(e: JsonElement): Any? =
        when (e) {
            JsonNull -> {
                null
            }

            is JsonObject -> {
                e.mapValues { plain(it.value) }
            }

            is JsonArray -> {
                e.map { plain(it) }
            }

            is JsonPrimitive -> {
                when {
                    e.isString -> e.content
                    e.booleanOrNull != null -> e.booleanOrNull
                    e.content.any { it == '.' || it == 'e' || it == 'E' } -> e.content.toDouble()
                    else -> e.content.toLongOrNull() ?: e.content.toDouble()
                }
            }
        }

    @Suppress("UNCHECKED_CAST")
    fun obj(v: Any?): Map<String, Any?> = v as Map<String, Any?>

    @Suppress("UNCHECKED_CAST")
    fun list(v: Any?): List<Any?> = v as List<Any?>

    fun str(
        m: Map<String, Any?>,
        k: String,
    ): String? = m[k] as String?

    fun int(
        m: Map<String, Any?>,
        k: String,
    ): Int? = (m[k] as Number?)?.toInt()
}

/** Collects the failures of table cases so one test reports all of them. */
internal class Outcomes(
    private val what: String,
) {
    var passed: Int = 0
    val skipped: MutableList<String> = mutableListOf()
    val failed: MutableList<String> = mutableListOf()

    fun run(
        name: String,
        body: () -> Unit,
    ) {
        try {
            body()
            passed++
        } catch (e: NotExpressible) {
            skipped += "$name: ${e.message}"
        } catch (e: Throwable) {
            failed += "$name: ${e.message}"
        }
    }

    fun finish() {
        println("$what: $passed passed, ${skipped.size} skipped, ${failed.size} failed")
        skipped.forEach { println("  skipped $it") }
        if (failed.isNotEmpty()) throw AssertionError("$what: ${failed.size} failed:\n" + failed.joinToString("\n"))
    }
}

/** A case the Kotlin API cannot express; it is skipped with the reason. */
internal class NotExpressible(
    reason: String,
) : RuntimeException(reason)
