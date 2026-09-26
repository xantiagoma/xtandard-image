import { describe, expect, test } from "bun:test";
import { base64url, fromHex } from "./bytes.ts";
import { encryptSource } from "./encrypt.ts";

// testdata/security/vectors.json, shared with internal/security/crypt.
const vectors = (await Bun.file(new URL("../../../testdata/security/vectors.json", import.meta.url)).json()) as {
  encryption: { name: string; key: string; iv: string; plaintext: string; token: string }[];
};

async function decrypt(token: string, key: string): Promise<string> {
  const raw = Uint8Array.from(atob(token.replaceAll("-", "+").replaceAll("_", "/")), (c) => c.charCodeAt(0));
  const k = await crypto.subtle.importKey("raw", fromHex(key), "AES-CBC", false, ["decrypt"]);
  const pt = await crypto.subtle.decrypt({ name: "AES-CBC", iv: raw.subarray(0, 16) }, k, raw.subarray(16));
  return new TextDecoder().decode(pt);
}

describe("encryption vectors", () => {
  test("there are vectors", () => expect(vectors.encryption.length).toBeGreaterThanOrEqual(3));
  for (const v of vectors.encryption) {
    test(v.name, async () => {
      expect(await encryptSource(v.plaintext, { key: v.key, iv: v.iv })).toBe(v.token);
      expect(await decrypt(v.token, v.key)).toBe(v.plaintext);
    });
  }
});

describe("random IV", () => {
  const key = vectors.encryption[0]!.key;
  test("two tokens differ and both decrypt", async () => {
    const src = "s3://bucket/some/key with spaces.png";
    const a = await encryptSource(src, { key });
    const b = await encryptSource(src, { key });
    expect(a).not.toBe(b);
    expect(await decrypt(a, key)).toBe(src);
    expect(await decrypt(b, key)).toBe(src);
    expect(a).toMatch(/^[A-Za-z0-9_-]+$/);
  });
  test("non-ASCII source round-trips", async () => {
    expect(await decrypt(await encryptSource("local:///images/ü.webp", { key }), key)).toBe("local:///images/ü.webp");
  });
});

describe("errors", () => {
  test("bad key length", async () => {
    await expect(encryptSource("x", { key: "0011" })).rejects.toThrow(RangeError);
  });
  test("bad iv length", async () => {
    await expect(encryptSource("x", { key: vectors.encryption[0]!.key, iv: "00" })).rejects.toThrow(RangeError);
  });
  test("control characters", async () => {
    await expect(encryptSource("a\u0000b", { key: vectors.encryption[0]!.key })).rejects.toThrow(TypeError);
  });
});

test("base64url matches Buffer", () => {
  for (const n of [0, 1, 2, 3, 15, 16, 17, 100, 70000]) {
    const b = crypto.getRandomValues(new Uint8Array(n));
    expect(base64url(b)).toBe(Buffer.from(b).toString("base64url"));
  }
});
