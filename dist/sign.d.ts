import { type KeyMaterial } from "./bytes.ts";
/** A signing key: XI_SIGNING_KEYS / XI_SIGNING_SALTS (imgproxy IMGPROXY_KEY / IMGPROXY_SALT). */
export interface SigningKey {
    /** HMAC key, hex (as in the server config) or raw bytes. */
    readonly key: KeyMaterial;
    /** Salt prepended to the message, hex or raw bytes. */
    readonly salt: KeyMaterial;
    /**
     * Signature size in bytes before encoding, 1–32 (XI_SIGNING_SIZE,
     * imgproxy IMGPROXY_SIGNATURE_SIZE). Default 32.
     */
    readonly size?: number;
}
/** The query parameter that carries an xi_sig signature. */
export declare const XI_SIG_PARAM = "xi_sig";
/**
 * Signs msg with the salted HMAC-SHA256 construction shared by the imgproxy
 * and xi_sig schemes. Like the server, a msg without a leading "/" is
 * signed as if it had one.
 */
export declare function sign(msg: string, k: SigningKey): Promise<string>;
/**
 * Returns "/" + signature + path for an imgproxy path (everything after the
 * signature segment: "/rs:fill:300:400/plain/…"), like the server's
 * Signer.SignImgproxyPath.
 */
export declare function signImgproxyPath(path: string, k: SigningKey): Promise<string>;
/**
 * Splits a raw query into the xi_sig value and the message xi_sig signs:
 * path, then "?" and the remaining non-empty parameters in their original
 * order and encoding. Throws on a repeated xi_sig (the server answers 403).
 */
export declare function xiMessage(path: string, rawQuery: string): {
    sig: string | undefined;
    msg: string;
};
/**
 * Returns rawQuery with any xi_sig removed and a fresh xi_sig for path
 * appended as the last parameter, like the server's Signer.SignXIQuery.
 * path is the escaped path the dialect sees (after its mount prefix).
 */
export declare function signXiQuery(path: string, rawQuery: string, k: SigningKey): Promise<string>;
/**
 * Signs a URL (absolute, or a path with an optional query) with xi_sig.
 * `mount` is the dialect's mount prefix, which the signed path excludes
 * (for example "/q" for the query dialect, docs/URL-DIALECTS.md §4).
 */
export declare function signXiUrl(url: string | URL, k: SigningKey, opts?: {
    mount?: string;
}): Promise<string>;
