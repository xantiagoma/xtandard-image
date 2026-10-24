// Byte helpers shared by signing and encryption. Runtime-neutral: only
// TextEncoder, btoa and WebCrypto, which Bun, Node 22+, Deno, browsers and
// workers all provide.
const encoder = new TextEncoder();
/** UTF-8 bytes of s. */
export function utf8(s) {
    return encoder.encode(s);
}
/** Decodes a hex string (either case, surrounding whitespace ignored). */
export function fromHex(hex) {
    const s = hex.trim();
    if (s.length % 2 !== 0 || !/^[0-9a-fA-F]*$/.test(s)) {
        throw new TypeError("key material is not a hex string");
    }
    const out = new Uint8Array(s.length / 2);
    for (let i = 0; i < out.length; i++)
        out[i] = Number.parseInt(s.slice(2 * i, 2 * i + 2), 16);
    return out;
}
/** Key bytes from hex or raw bytes (copied, so callers may clear theirs). */
export function keyBytes(k) {
    return typeof k === "string" ? fromHex(k) : new Uint8Array(k);
}
/** Unpadded base64url (RFC 4648 §5), as Go's base64.RawURLEncoding. */
export function base64url(bytes) {
    let bin = "";
    for (let i = 0; i < bytes.length; i += 0x8000) {
        bin += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
    }
    return btoa(bin).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
}
/** Concatenates byte arrays. */
export function concat(...parts) {
    const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
    let off = 0;
    for (const p of parts) {
        out.set(p, off);
        off += p.length;
    }
    return out;
}
/** WebCrypto, from globalThis (no node:crypto import). */
export function subtle() {
    const s = globalThis.crypto?.subtle;
    if (!s)
        throw new Error("WebCrypto (globalThis.crypto.subtle) is not available in this runtime");
    return s;
}
/** Reports whether s contains an ASCII control character (C0 or DEL). */
export function hasControlChar(s) {
    return /[\u0000-\u001f\u007f]/.test(s);
}
