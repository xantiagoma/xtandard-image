// Source URL encryption for `enc/` sources, byte-compatible with
// internal/security/crypt and imgproxy's reference encoder:
//
//   token = base64url_unpadded( IV[16] ‖ AES-CBC(key, IV, PKCS#7(source)) )
//
// Keys are 16, 24 or 32 bytes (AES-128/192/256). WebCrypto in Chromium
// rejects 192-bit AES keys; Bun, Node, Deno, Firefox and Safari accept them.

import { base64url, concat, hasControlChar, type KeyMaterial, keyBytes, subtle, utf8 } from "./bytes.ts";

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

const BLOCK = 16;

const keyCache = new WeakMap<EncryptionKey, Promise<CryptoKey>>();

function aesKey(k: EncryptionKey): Promise<CryptoKey> {
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
export async function encryptSource(source: string, k: EncryptionKey): Promise<string> {
  if (source === "" || hasControlChar(source)) {
    throw new TypeError("source must be a non-empty string without control characters");
  }
  let iv: Uint8Array<ArrayBuffer>;
  if (k.iv === undefined) {
    iv = globalThis.crypto.getRandomValues(new Uint8Array(BLOCK));
  } else {
    iv = keyBytes(k.iv);
    if (iv.length !== BLOCK) throw new RangeError(`iv is ${iv.length} bytes, want ${BLOCK}`);
  }
  const ct = await subtle().encrypt({ name: "AES-CBC", iv }, await aesKey(k), utf8(source));
  return base64url(concat(iv, new Uint8Array(ct)));
}
