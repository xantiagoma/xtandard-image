import { describe, expect, test } from "bun:test";
import {
  buildImgproxyStreamUrl,
  escapeQueryValue,
  imgproxy,
  queryUrls,
  signXiUrl,
  sourceUrl,
  xiMessage,
  xiSource,
} from "./index.ts";

const signing = { key: "943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881", salt: "520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5" };
const encryption = { key: "1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1" };

describe("query dialect", () => {
  const q = queryUrls({ baseUrl: "https://img.example.com/" });

  test("native keys, then o=, then format", async () => {
    expect(
      await q.url({
        source: "https://e.com/a b.jpg?v=1&x=2",
        params: { w: 300, fit: "cover", g: "smart", enlarge: true, h: null },
        options: [{ bl: 2 }, { rs: ["fill", 100, 100] }],
        extension: "webp",
      }),
    ).toBe("https://img.example.com/q/?url=https://e.com/a%20b.jpg%3Fv%3D1%26x%3D2&w=300&fit=cover&g=smart&enlarge=true&o=bl:2/-/rs:fill:100:100&format=webp");
  });

  test("fluent form with param() and chain()", async () => {
    const u = await q.from("s3://b/k.jpg").param("w", 200).blur(3).chain().q(80).build();
    expect(u).toBe("https://img.example.com/q/?url=s3://b/k.jpg&w=200&o=bl:3/-/q:80");
  });

  test("video keys: arguments joined by ',', crop and flip under their aliases", async () => {
    expect(
      await q.videoUrl({
        source: "xi://media/talk.mov",
        options: [{ start: 1, duration: 5, resize: ["fill", 640, 360], crop: [100, 100, "nowe"], flip: [true, false] }, { vs: 2 }],
        extension: "webm",
      }),
    ).toBe("https://img.example.com/q/video?url=xi://media/talk.mov&vs=1&vd=5&rs=fill,640,360&c=100,100,nowe&fl=1,0&vs=2&format=webm");
    const canonical = queryUrls({ optionNames: "canonical" });
    expect(await canonical.video("a.mov").crop(10, 10).start(1).build()).toBe("/q/video?url=a.mov&c=10,10&start=1");
  });

  test("video arguments may not contain ':' or ','", async () => {
    await expect(q.video("a.mov").option("wmt", "a:b").build()).rejects.toThrow(TypeError);
    await expect(q.video("a.mov").option("wmt", "a,b").build()).rejects.toThrow(TypeError);
    await expect((q.video("a.mov") as unknown as { option(n: string): { build(): Promise<string> } }).option("nope").build()).rejects.toThrow(TypeError);
  });

  test("streams: format=m3u8 and st", async () => {
    expect(await q.streamUrl({ source: "a.mp4", protocol: "hls", renditions: [360, 720], segmentDuration: 4, options: { vs: 10 } })).toBe(
      "https://img.example.com/q/video?url=a.mp4&vs=10&st=hls,360,720&sgd=4&format=m3u8",
    );
    expect(await q.streamUrl({ source: "a.mp4", protocol: "dash" })).toBe("https://img.example.com/q/video?url=a.mp4&format=mpd");
  });

  test("imaginary endpoints", async () => {
    expect(await q.imaginaryUrl({ endpoint: "resize", source: "https://e.com/a.jpg", params: { width: 300, type: "webp", nocrop: true } })).toBe(
      "https://img.example.com/q/resize?url=https://e.com/a.jpg&width=300&type=webp&nocrop=true",
    );
  });

  test("xi_sig signs the path after the mount and the query", async () => {
    const s = queryUrls({ baseUrl: "https://img.example.com", signing });
    const u = await s.url({ source: "s3://b/k.jpg", params: { w: 300 } });
    const unsigned = u.replace(/&xi_sig=[^&]+$/, "");
    expect(u).toBe(await signXiUrl(unsigned, signing, { mount: "/q" }));
    const v = await s.video("a.mov").vd(2).build();
    const { msg } = xiMessage("/video", new URL(v).search.slice(1));
    expect(msg).toBe("/video?url=a.mov&vd=2");
    expect(v).toBe(await signXiUrl(v.replace(/&xi_sig=[^&]+$/, ""), signing, { mount: "/q" }));
  });

  test("pdfPassword needs signing and encryption", async () => {
    await expect(q.url({ source: "a.pdf", pdfPassword: "x" })).rejects.toThrow("encryption");
    await expect(queryUrls({ encryption }).url({ source: "a.pdf", pdfPassword: "x" })).rejects.toThrow("signing");
    const u = await queryUrls({ encryption, signing }).url({ source: "a.pdf", pdfPassword: "x" });
    expect(u).toMatch(/^\/q\/\?url=a\.pdf&pdp=[A-Za-z0-9_-]{43}&xi_sig=/);
  });

  test("a custom mount", async () => {
    expect(await queryUrls({ mount: "/query/" }).url({ source: "a.jpg" })).toBe("/query/?url=a.jpg");
  });

  test("escapeQueryValue", () => {
    expect(escapeQueryValue("a+b=c&d;e#f?g%h é,:/")).toBe("a%2Bb%3Dc%26d%3Be%23f%3Fg%25h%20%C3%A9,:/");
  });
});

describe("streams on /video", () => {
  const img = imgproxy({ sourceEncoding: "plain" });

  test("hls with renditions, dash with the ladder", async () => {
    expect(await img.streamUrl({ source: "s3://m/talk.mov", protocol: "hls", renditions: [360, 720], segmentDuration: 4 })).toBe(
      "/video/insecure/st:hls:360:720/sgd:4/plain/s3://m/talk.mov@m3u8",
    );
    expect(await img.streamUrl({ source: "s3://m/talk.mov", protocol: "dash", options: { vs: 60, vd: 300 } })).toBe(
      "/video/insecure/vs:60/vd:300/plain/s3://m/talk.mov@mpd",
    );
  });

  test("stream options belong to the request fields", async () => {
    await expect(buildImgproxyStreamUrl({ source: "a", protocol: "hls", options: { st: "dash" } })).rejects.toThrow(TypeError);
    await expect(buildImgproxyStreamUrl({ source: "a", protocol: "hls", options: { f: "mp4" } })).rejects.toThrow(TypeError);
    await expect(buildImgproxyStreamUrl({ source: "a", protocol: "smooth" as never })).rejects.toThrow(TypeError);
  });
});

test("source references", () => {
  expect(xiSource("photos", "/2026/cat.jpg")).toBe("xi://photos/2026/cat.jpg");
  expect(xiSource("web", "https://cdn.example.com/a.jpg")).toBe("xi://web/https://cdn.example.com/a.jpg");
  expect(sourceUrl("s3", "bucket", "k.jpg")).toBe("s3://bucket/k.jpg");
  expect(sourceUrl("gs", "bucket", "k.jpg")).toBe("gs://bucket/k.jpg");
  expect(sourceUrl("abs", "container", "k.jpg")).toBe("abs://container/k.jpg");
  expect(sourceUrl("local", "", "img/k.jpg")).toBe("local:///img/k.jpg");
  expect(sourceUrl("xi", "media", "a.mov")).toBe("xi://media/a.mov");
  expect(() => xiSource("bad name", "k")).toThrow(TypeError);
  expect(() => sourceUrl("local", "x", "k")).toThrow(TypeError);
  expect(() => sourceUrl("s3", "", "k")).toThrow(TypeError);
});
