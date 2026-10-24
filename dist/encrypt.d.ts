import { type KeyMaterial } from "./bytes.ts";
/** An encryption key: the first of XI_ENCRYPTION_KEYS (imgproxy IMGPROXY_SOURCE_URL_ENCRYPTION_KEY). */
export interface EncryptionKey {
    /** AES key, hex or raw bytes: 16, 24 or 32 bytes. */
    readonly key: KeyMaterial;
    /**
     * A fixed 16-byte IV (hex or raw bytes). Leave it unset for a fresh random
     * IV per URL, as the server's own encoder does. A fixed IV makes URLs
     * deterministic (and reveals when two URLs share a source); use it only
     * when that trade-off is intended, such as for tests or cache keys.
     */
    readonly iv?: KeyMaterial;
}
/** Encrypts a source URL into an `enc/` token. */
export declare function encryptSource(source: string, k: EncryptionKey): Promise<string>;
/** The longest document password the server accepts, in UTF-8 bytes. */
export declare const MAX_PDF_PASSWORD_BYTES = 1024;
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
export declare function encryptPdfPassword(password: string, k: EncryptionKey): Promise<string>;
