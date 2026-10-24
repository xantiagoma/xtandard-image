// Source URL encryption for `enc/` sources, byte-compatible with
// internal/security/crypt and imgproxy's reference encoder:
//
//   token = base64url_unpadded( IV[16] ‖ AES-CBC(key, IV, PKCS#7(source)) )
//
// Document passwords for `pdf_password` (WP-115) use the same format.
//
// Keys are 16, 24 or 32 bytes (AES-128/192/256). WebCrypto in Chromium
// rejects 192-bit AES keys; Bun, Node, Deno, Firefox and Safari accept them.
import { base64url, concat, hasControlChar, keyBytes, subtle, utf8 } from "./bytes.js";
const BLOCK = 16;
const keyCache = new WeakMap();
function aesKey(k) {
    let p = keyCache.get(k);
    if (!p) {
        const raw = keyBytes(k.key);
        if (raw.length !== 16 && raw.length !== 24 && raw.length !== 32) {
            throw new RangeError(`encryption key is ${raw.length} bytes, want 16, 24 or 32`);
        }
        p = subtle().importKey("raw", raw, { name: "AES-CBC" }, false, ["encrypt"]);
        keyCache.set(k, p);
    }
    return p;
}
/** Encrypts a source URL into an `enc/` token. */
export async function encryptSource(source, k) {
    if (source === "" || hasControlChar(source)) {
        throw new TypeError("source must be a non-empty string without control characters");
    }
    return seal(source, k);
}
/** The longest document password the server accepts, in UTF-8 bytes. */
export const MAX_PDF_PASSWORD_BYTES = 1024;
/**
 * Encrypts the password of a password-protected PDF into a `pdf_password`
 * (`pdp`) token: the `enc/` format over the password, with the same key
 * (the first of XI_ENCRYPTION_KEYS). The server accepts the option on
 * signed URLs only, so pair it with `signing`; like the key, encrypt on a
 * server, not in browser code.
 *
 * ```ts
 * const token = await encryptPdfPassword("s3cret", { key: process.env.XI_ENCRYPTION_KEY! });
 * await img.url({ source: "s3://docs/a.pdf", options: { pdp: token, rs: ["fit", 800, 0] } });
 * ```
 */
export async function encryptPdfPassword(password, k) {
    if (password === "" || hasControlChar(password)) {
        throw new TypeError("password must be a non-empty string without control characters");
    }
    if (utf8(password).length > MAX_PDF_PASSWORD_BYTES) {
        throw new RangeError(`password is longer than ${MAX_PDF_PASSWORD_BYTES} bytes`);
    }
    return seal(password, k);
}
async function seal(plaintext, k) {
    let iv;
    if (k.iv === undefined) {
        iv = globalThis.crypto.getRandomValues(new Uint8Array(BLOCK));
    }
    else {
        iv = keyBytes(k.iv);
        if (iv.length !== BLOCK)
            throw new RangeError(`iv is ${iv.length} bytes, want ${BLOCK}`);
    }
    const ct = await subtle().encrypt({ name: "AES-CBC", iv }, await aesKey(k), utf8(plaintext));
    return base64url(concat(iv, new Uint8Array(ct)));
}
