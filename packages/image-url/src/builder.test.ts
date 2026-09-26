import { describe, expect, test } from "bun:test";
import {
  buildImgproxyInfoUrl,
  buildImgproxyUrl,
  escapePlainSource,
  imgproxy,
  imgproxyInfoAliases,
  imgproxyInfoOptionNames,
  imgproxyProcessingAliases,
  imgproxyProcessingOptionNames,
} from "./index.ts";

const src = "https://example.com/a.jpg";
const b64 = "aHR0cHM6Ly9leGFtcGxlLmNvbS9hLmpwZw";

describe("sources", () => {
  test("base64 by default, with extension", async () => {
    expect(await buildImgproxyUrl({ source: src, options: { w: 300 }, extension: "webp" })).toBe(
      `/insecure/w:300/${b64}.webp`,
    );
  });

  test("base64 split into segments", async () => {
    expect(await buildImgproxyUrl({ source: src }, { splitSource: 10 })).toBe(
      `/insecure/aHR0cHM6Ly/9leGFtcGxl/LmNvbS9hLm/pwZw`,
    );
  });

  test("plain, escaped", async () => {
    expect(
      await buildImgproxyUrl(
        { source: "https://e.com/a b/ü.jpg?v=1&x=2#f@2x%", extension: "png" },
        { sourceEncoding: "plain" },
      ),
    ).toBe("/insecure/plain/https://e.com/a%20b/%C3%BC.jpg%3Fv=1&x=2%23f%402x%25@png");
  });

  test("escapePlainSource keeps scheme separators and sub-delims", () => {
    expect(escapePlainSource("s3://bucket/k(1)+x,y;z=w:1!$'*~")).toBe("s3://bucket/k(1)+x,y;z=w:1!$'*~");
    expect(escapePlainSource("a\"<>\\^`{|}[]")).toBe("a%22%3C%3E%5C%5E%60%7B%7C%7D%5B%5D");
  });

  test("encrypted, fixed IV (imgproxy example vector)", async () => {
    const url = await buildImgproxyUrl(
      { source: "http://img.example.com/pretty/image.jpg", sourceEncoding: "encrypted", extension: "png" },
      {
        encryption: {
          key: "1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1",
          iv: "30313233343536373839616263646566",
        },
      },
    );
    expect(url).toBe(
      "/insecure/enc/MDEyMzQ1Njc4OWFiY2RlZj206ZwwVKiBydg5ip7OI2oVBFCdcMXXGze1K8CjwiQuFTiJkTDmRJeJccTs0wH06w.png",
    );
  });

  test("encrypted without a key throws", async () => {
    await expect(buildImgproxyUrl({ source: src, sourceEncoding: "encrypted" })).rejects.toThrow();
  });

  test("invalid sources and extensions throw", async () => {
    await expect(buildImgproxyUrl({ source: "" })).rejects.toThrow(TypeError);
    await expect(buildImgproxyUrl({ source: "a\nb" })).rejects.toThrow(TypeError);
    await expect(buildImgproxyUrl({ source: src, extension: "we.bp" })).rejects.toThrow(TypeError);
  });
});

describe("options", () => {
  test("names: short (default), canonical, given", async () => {
    const options = { resize: ["fill", 300, 400] as const, crop_aspect_ratio: 1.5, format: "webp" as const };
    expect(await buildImgproxyUrl({ source: src, options })).toBe(`/insecure/rs:fill:300:400/car:1.5/f:webp/${b64}`);
    expect(await buildImgproxyUrl({ source: src, options }, { optionNames: "canonical" })).toBe(
      `/insecure/resize:fill:300:400/crop_aspect_ratio:1.5/format:webp/${b64}`,
    );
    expect(await buildImgproxyUrl({ source: src, options: { crop_ar: 2, rs: "fit" } }, { optionNames: "given" })).toBe(
      `/insecure/crop_ar:2/rs:fit/${b64}`,
    );
  });

  test("argument encoding: booleans, null, numbers", async () => {
    expect(
      await buildImgproxyUrl({ source: src, options: { rs: ["fit", null, 100, true, false], pd: [1, 2.5], g: ["we", -10, 0.25] } }),
    ).toBe(`/insecure/rs:fit::100:1:0/pd:1:2.5/g:we:-10:0.25/${b64}`);
  });

  test("undefined values are skipped", async () => {
    expect(await buildImgproxyUrl({ source: src, options: { w: undefined, h: 10 } as never })).toBe(`/insecure/h:10/${b64}`);
  });

  test("chained pipelines; empty ones are dropped", async () => {
    expect(await buildImgproxyUrl({ source: src, options: [{ w: 100 }, {}, { bl: 2 }] })).toBe(`/insecure/w:100/-/bl:2/${b64}`);
    expect(await imgproxy().from(src).chain().w(1).chain().chain().h(2).chain().build()).toBe(`/insecure/w:1/-/h:2/${b64}`);
  });

  test("custom separator", async () => {
    expect(await buildImgproxyUrl({ source: src, options: { rs: ["fill", 1, 2] } }, { separator: "," })).toBe(
      `/insecure/rs,fill,1,2/${b64}`,
    );
    await expect(buildImgproxyUrl({ source: src }, { separator: "/" })).rejects.toThrow(TypeError);
  });

  test("runtime checks", async () => {
    await expect(buildImgproxyUrl({ source: src, options: { nope: [1] } as never })).rejects.toThrow(/unknown imgproxy option/);
    await expect(buildImgproxyUrl({ source: src, options: { cb: "a/b" } })).rejects.toThrow(TypeError);
    await expect(buildImgproxyUrl({ source: src, options: { cb: "a:b" } })).rejects.toThrow(TypeError);
    await expect(buildImgproxyUrl({ source: src, options: { w: Number.NaN } })).rejects.toThrow(RangeError);
    await expect(buildImgproxyUrl({ source: src, options: { w: [] as never } })).rejects.toThrow(TypeError);
  });
});

test("README example", async () => {
  const img = imgproxy({ baseUrl: "https://img.example.com" });
  const want =
    "https://img.example.com/insecure/rs:fill:300:400/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp";
  expect(
    await img.url({
      source: "s3://photos/cat.jpg",
      options: { resize: ["fill", 300, 400], gravity: "sm", quality: 80 },
      extension: "webp",
    }),
  ).toBe(want);
  expect(await img.from("s3://photos/cat.jpg").resize("fill", 300, 400).g("sm").quality(80).extension("webp").build()).toBe(
    want,
  );
});

describe("builder", () => {
  const img = imgproxy({ baseUrl: "https://img.example.com/", unsignedSignature: "_" });

  test("base URL and placeholder", async () => {
    expect(await img.url({ source: src, options: { q: 80 } })).toBe(`https://img.example.com/_/q:80/${b64}`);
  });

  test("fluent and object forms agree", async () => {
    const fluent = await img.from(src).resize("fill", 300, 400).q(80).chain().blur(2).extension("avif").build();
    const object = await img.url({ source: src, options: [{ resize: ["fill", 300, 400], q: 80 }, { blur: 2 }], extension: "avif" });
    expect(fluent).toBe(object);
    expect(fluent).toBe(`https://img.example.com/_/rs:fill:300:400/q:80/-/bl:2/${b64}.avif`);
  });

  test("options() and encoding() in the fluent form", async () => {
    expect(await img.from(src).options({ w: 1, h: 2 }).encoding("plain").build()).toBe(
      `https://img.example.com/_/w:1/h:2/plain/${src}`,
    );
  });

  test("every option name and alias is a fluent method", () => {
    const p = img.from(src) as unknown as Record<string, unknown>;
    for (const n of [...imgproxyProcessingOptionNames, ...Object.keys(imgproxyProcessingAliases)]) {
      expect(typeof p[n]).toBe("function");
    }
    const i = img.info(src) as unknown as Record<string, unknown>;
    for (const n of [...imgproxyInfoOptionNames, ...Object.keys(imgproxyInfoAliases)]) {
      expect(typeof i[n]).toBe("function");
    }
  });

  test("a builder is reusable: each from() starts fresh", async () => {
    const a = img.from(src).w(1);
    const b = img.from(src).h(2);
    expect(await a.build()).toBe(`https://img.example.com/_/w:1/${b64}`);
    expect(await b.build()).toBe(`https://img.example.com/_/h:2/${b64}`);
  });
});

describe("/info", () => {
  test("info aliases differ from processing aliases", async () => {
    // "a" is alpha in /info and adjust in processing URLs.
    expect(await buildImgproxyInfoUrl({ source: src, options: { a: true, s: true } }, { optionNames: "canonical" })).toBe(
      `/info/insecure/alpha:1/size:1/${b64}`,
    );
  });

  test("signed info URL signs the path after /info/<signature>", async () => {
    const k = { key: "736563726574", salt: "68656C6C6F" };
    const url = await buildImgproxyInfoUrl({ source: src, options: { size: true } }, { signing: k });
    const plainUrl = await buildImgproxyUrl({ source: src, options: { w: 1 } }, { signing: k });
    expect(url).toMatch(new RegExp(`^/info/[A-Za-z0-9_-]{43}/s:1/${b64}$`));
    expect(plainUrl).toMatch(new RegExp(`^/[A-Za-z0-9_-]{43}/w:1/${b64}$`));
  });

  test("no chained pipelines", async () => {
    await expect(buildImgproxyInfoUrl({ source: src, options: [{ s: true }, { f: true }] as never })).rejects.toThrow();
  });
});
