/** Key material: a hex string (as in XI_SIGNING_KEYS / IMGPROXY_KEY) or raw bytes. */
export type KeyMaterial = string | Uint8Array;
/** UTF-8 bytes of s. */
export declare function utf8(s: string): Uint8Array<ArrayBuffer>;
/** Decodes a hex string (either case, surrounding whitespace ignored). */
export declare function fromHex(hex: string): Uint8Array<ArrayBuffer>;
/** Key bytes from hex or raw bytes (copied, so callers may clear theirs). */
export declare function keyBytes(k: KeyMaterial): Uint8Array<ArrayBuffer>;
/** Unpadded base64url (RFC 4648 §5), as Go's base64.RawURLEncoding. */
export declare function base64url(bytes: Uint8Array): string;
/** Concatenates byte arrays. */
export declare function concat(...parts: Uint8Array[]): Uint8Array<ArrayBuffer>;
/** WebCrypto, from globalThis (no node:crypto import). */
export declare function subtle(): SubtleCrypto;
/** Reports whether s contains an ASCII control character (C0 or DEL). */
export declare function hasControlChar(s: string): boolean;
