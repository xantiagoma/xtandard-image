import { describe, expect, test } from "bun:test";
import { fromHex } from "./bytes.ts";
import { buildImgproxyUrl } from "./builder.ts";
import { sign, signImgproxyPath, signXiQuery, signXiUrl, xiMessage } from "./sign.ts";

// testdata/security/vectors.json: the WP-19 vectors, shared with the Go
// tests (internal/security/sign TestSharedVectors).
const vectors = (await Bun.file(new URL("../../../testdata/security/vectors.json", import.meta.url)).json()) as {
  imgproxy: { name: string; key: string; salt: string; size: number; path: string; signature: string }[];
  xi_sig: { name: string; key: string; salt: string; size: number; path: string; query: string; signed_query: string }[];
};

describe("imgproxy signature vectors", () => {
  test("there are vectors", () => expect(vectors.imgproxy.length).toBeGreaterThanOrEqual(6));
  for (const v of vectors.imgproxy) {
    test(v.name, async () => {
      const k = { key: v.key, salt: v.salt, size: v.size };
      expect(await sign(v.path, k)).toBe(v.signature);
      const p = v.path.startsWith("/") ? v.path : `/${v.path}`;
      expect(await signImgproxyPath(v.path, k)).toBe(`/${v.signature}${p}`);
      // Raw key bytes sign the same as hex.
      expect(await sign(v.path, { key: fromHex(v.key), salt: fromHex(v.salt), size: v.size })).toBe(v.signature);
    });
  }
});

describe("xi_sig vectors", () => {
  test("there are vectors", () => expect(vectors.xi_sig.length).toBeGreaterThanOrEqual(3));
  for (const v of vectors.xi_sig) {
    test(v.name, async () => {
      const k = { key: v.key, salt: v.salt, size: v.size };
      expect(await signXiQuery(v.path, v.query, k)).toBe(v.signed_query);
      // Re-signing a signed query is idempotent.
      expect(await signXiQuery(v.path, v.signed_query, k)).toBe(v.signed_query);
      expect(await signXiUrl(`https://img.example.com${v.path}?${v.query}`, k)).toBe(
        `https://img.example.com${v.path}?${v.signed_query}`,
      );
    });
  }
});

describe("builder output signs like the docs", () => {
  test("docs example end to end", async () => {
    const v = vectors.imgproxy.find((x) => x.name === "docs_signing_url")!;
    const url = await buildImgproxyUrl(
      {
        source: "http://example.com/images/curiosity.jpg",
        options: { resize: ["fill", 300, 400, false], gravity: "sm" },
        extension: "png",
      },
      { signing: { key: v.key, salt: v.salt }, splitSource: 16 },
    );
    expect(url).toBe(`/${v.signature}${v.path}`);
  });

  test("examples_signature with a plain source and a truncated signature", async () => {
    const v = vectors.imgproxy.find((x) => x.name === "examples_signature_truncated")!;
    const url = await buildImgproxyUrl(
      { source: "http://img.example.com/pretty/image.jpg", options: { rs: ["fit", 300, 300] }, sourceEncoding: "plain" },
      { signing: { key: v.key, salt: v.salt, size: 8 }, baseUrl: "https://img.example.com/" },
    );
    expect(url).toBe(`https://img.example.com/${v.signature}${v.path}`);
  });
});

describe("xiMessage", () => {
  const cases: [string, string, string | undefined, string][] = [
    ["/a.jpg", "", undefined, "/a.jpg"],
    ["/a.jpg", "xi_sig=abc", "abc", "/a.jpg"],
    ["/a.jpg", "w=1&xi_sig=abc&h=2", "abc", "/a.jpg?w=1&h=2"],
    ["/a.jpg", "url=https%3A%2F%2Fx&xi_sig=s", "s", "/a.jpg?url=https%3A%2F%2Fx"],
    ["/a.jpg", "&w=1&&xi_sig=s&", "s", "/a.jpg?w=1"],
    ["/a.jpg", "xi_sig&w=1", "", "/a.jpg?w=1"],
    ["/a.jpg", "xi_sig2=1&xxi_sig=2&xi%5Fsig=3", undefined, "/a.jpg?xi_sig2=1&xxi_sig=2&xi%5Fsig=3"],
  ];
  for (const [path, query, sig, msg] of cases) {
    test(`${path}?${query}`, () => expect(xiMessage(path, query)).toEqual({ sig, msg }));
  }
  test("duplicate xi_sig throws", () => expect(() => xiMessage("/a", "xi_sig=a&xi_sig=b")).toThrow());
});

describe("signXiUrl", () => {
  const k = { key: vectors.xi_sig[0]!.key, salt: vectors.xi_sig[0]!.salt };
  test("mount prefix is not signed", async () => {
    const direct = await signXiQuery("/resize", "width=300&url=x", k);
    expect(await signXiUrl("https://h.example/q/resize?width=300&url=x", k, { mount: "/q" })).toBe(
      `https://h.example/q/resize?${direct}`,
    );
  });
  test("root-relative URL and fragment", async () => {
    const direct = await signXiQuery("/imgix/a.jpg", "w=1", k);
    expect(await signXiUrl("/imgix/a.jpg?w=1#frag", k)).toBe(`/imgix/a.jpg?${direct}`);
  });
  test("path outside the mount throws", async () => {
    await expect(signXiUrl("/other/a.jpg", k, { mount: "/q" })).rejects.toThrow();
  });
});

test("signature size is validated", async () => {
  const k = { key: "00", salt: "00" };
  await expect(sign("/x", { ...k, size: 0 })).rejects.toThrow(RangeError);
  await expect(sign("/x", { ...k, size: 33 })).rejects.toThrow(RangeError);
  await expect(sign("/x", { key: "zz", salt: "00" })).rejects.toThrow(TypeError);
});
