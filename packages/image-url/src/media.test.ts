import { afterAll, describe, expect, test } from "bun:test";
import { buildImgproxyStreamUrl, buildImgproxyVideoUrl } from "./builder.ts";
import { createClient, rangeHeader, XtandardError } from "./client.ts";

const key = { key: "736563726574", salt: "68656C6C6F" };
const body = new Uint8Array(1000).map((_, i) => i % 256);

// A stand-in server: /video answers Range and HEAD like the real one, a
// stream manifest lists ticket URLs, the playground serves api/meta.
const seen: { method: string; path: string; range: string | null; accept: string | null }[] = [];
const server = Bun.serve({
  port: 0,
  async fetch(req) {
    const url = new URL(req.url);
    seen.push({ method: req.method, path: url.pathname, range: req.headers.get("range"), accept: req.headers.get("accept") });
    if (url.pathname.startsWith("/video/") && url.pathname.endsWith("m3u8")) {
      return new Response("#EXTM3U\n/_stream/T/v0.m3u8\n", { headers: { "Content-Type": "application/vnd.apple.mpegurl" } });
    }
    if (url.pathname.startsWith("/video/") || url.pathname.startsWith("/_stream/")) {
      const expected = await buildImgproxyVideoUrl({ source: "s3://m/clip.mov", options: { vd: 2 }, extension: "webm" }, { signing: key });
      if (url.pathname.startsWith("/video/") && url.pathname !== expected) {
        return Response.json({ error: { kind: "unauthorized", message: "invalid signature" } }, { status: 403 });
      }
      const headers = { "Content-Type": "video/webm", "Accept-Ranges": "bytes" };
      const m = /^bytes=(\d+)-(\d*)$/.exec(req.headers.get("range") ?? "");
      if (m) {
        const start = Number(m[1]);
        const end = m[2] ? Number(m[2]) : body.length - 1;
        return new Response(req.method === "HEAD" ? null : body.slice(start, end + 1), {
          status: 206,
          headers: { ...headers, "Content-Range": `bytes ${start}-${end}/${body.length}` },
        });
      }
      return new Response(req.method === "HEAD" ? null : body, { headers: { ...headers, "Content-Length": String(body.length) } });
    }
    if (url.pathname === "/_admin/play/api/meta") return Response.json({ version: "test" });
    return new Response("not found\n", { status: 404 });
  },
});
afterAll(() => server.stop(true));
const base = `http://localhost:${server.port}`;

describe("media", () => {
  const xi = createClient({ baseUrl: base, imgproxy: { signing: key }, playground: { path: "/_admin/play/" } });
  const req = { source: "s3://m/clip.mov", options: { vd: 2 }, extension: "webm" } as const;

  test("video: whole body", async () => {
    const res = await xi.video(req);
    expect(res.status).toBe(200);
    expect(new Uint8Array(await res.arrayBuffer())).toEqual(body);
  });

  test("video: a range is a 206", async () => {
    const res = await xi.video(req, { range: { start: 10, end: 19 } });
    expect([res.status, res.headers.get("content-range")]).toEqual([206, "bytes 10-19/1000"]);
    expect(new Uint8Array(await res.arrayBuffer())).toEqual(body.slice(10, 20));
    expect(seen.at(-1)?.range).toBe("bytes=10-19");
  });

  test("video: HEAD", async () => {
    const res = await xi.video(req, { method: "HEAD" });
    expect([seen.at(-1)?.method, res.headers.get("content-type")]).toEqual(["HEAD", "video/webm"]);
  });

  test("video: a bad signature is an XtandardError", async () => {
    const err = await xi.video({ ...req, options: { vd: 3 } }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(XtandardError);
    expect((err as XtandardError).kind).toBe("unauthorized");
  });

  test("stream: the manifest and its unsigned children", async () => {
    const m = await xi.stream({ source: "s3://m/clip.mov", protocol: "hls", renditions: [360, 720] });
    expect(m.url).toBe(await buildImgproxyStreamUrl({ source: "s3://m/clip.mov", protocol: "hls", renditions: [360, 720] }, { baseUrl: base, signing: key }));
    expect(m.url).toContain("/st:hls:360:720/");
    expect(m.contentType).toBe("application/vnd.apple.mpegurl");
    const child = m.body.split("\n")[1]!;
    const part = await xi.media(child, { range: { suffix: 4 } });
    expect(seen.at(-1)?.path).toBe("/_stream/T/v0.m3u8");
    expect(part.status).toBe(200); // the stand-in only answers start-end ranges
  });

  test("playgroundMeta uses the playground path", async () => {
    expect(await xi.playgroundMeta()).toEqual({ version: "test" } as never);
  });
});

test("rangeHeader", () => {
  expect(rangeHeader({ start: 0 })).toBe("bytes=0-");
  expect(rangeHeader({ start: 5, end: 9 })).toBe("bytes=5-9");
  expect(rangeHeader({ suffix: 100 })).toBe("bytes=-100");
  expect(rangeHeader("bytes=1-2")).toBe("bytes=1-2");
  expect(() => rangeHeader({ start: 5, end: 4 })).toThrow(RangeError);
  expect(() => rangeHeader({ start: -1 })).toThrow(RangeError);
});
