import { afterAll, describe, expect, test } from "bun:test";
import { createClient, type InfoResponse, toXtandardError, XtandardError } from "./client.ts";
import { buildImgproxyInfoUrl } from "./builder.ts";
import { queryMethodRequest } from "./query.ts";

const key = { key: "736563726574", salt: "68656C6C6F" };
const infoBody: InfoResponse = { format: "jpeg", width: 320, height: 240 };

// A stand-in server with the real endpoints' status codes and bodies.
let ready = "ready";
const seen: { path: string; accept: string | null; custom: string | null }[] = [];
const server = Bun.serve({
  port: 0,
  async fetch(req) {
    const url = new URL(req.url);
    seen.push({ path: url.pathname, accept: req.headers.get("accept"), custom: req.headers.get("x-custom") });
    if (url.pathname === "/healthz") return new Response("ok\n");
    if (url.pathname === "/readyz") return new Response(`${ready}\n`, { status: ready === "ready" ? 200 : 503 });
    if (url.pathname.startsWith("/info/")) {
      const expected = await buildImgproxyInfoUrl({ source: "s3://b/k.jpg", options: { size: true } }, { signing: key });
      if (url.pathname !== expected) {
        return Response.json(
          { error: { kind: "unauthorized", message: "invalid signature", request_id: "req-1" } },
          { status: 403 },
        );
      }
      return Response.json(infoBody);
    }
    if (url.pathname === "/proxy-error") return new Response("<html>bad gateway</html>", { status: 502 });
    return new Response("not found\n", { status: 404 });
  },
});
afterAll(() => server.stop(true));
const base = `http://localhost:${server.port}`;

describe("client", () => {
  const client = createClient({ baseUrl: `${base}/`, imgproxy: { signing: key }, headers: { "X-Custom": "1" } });

  test("healthz", async () => {
    await client.healthz();
    expect(seen.at(-1)?.custom).toBe("1");
  });

  test("readyz: ready, then draining is a state, not an error", async () => {
    expect(await client.readyz()).toEqual({ ready: true, state: "ready" });
    ready = "draining";
    expect(await client.readyz()).toEqual({ ready: false, state: "draining" });
    ready = "ready";
  });

  test("info builds and signs the URL and asks for JSON", async () => {
    expect(await client.info({ source: "s3://b/k.jpg", options: { size: true } })).toEqual(infoBody);
    expect(seen.at(-1)?.accept).toBe("application/json");
  });

  test("info errors carry the server's envelope", async () => {
    const err = await client.info({ source: "s3://b/other.jpg" }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(XtandardError);
    const x = err as XtandardError;
    expect([x.status, x.kind, x.message, x.requestId]).toEqual([403, "unauthorized", "invalid signature", "req-1"]);
    expect(x.body?.error.kind).toBe("unauthorized");
  });

  test("infoUrl accepts a prebuilt path", async () => {
    const path = await buildImgproxyInfoUrl({ source: "s3://b/k.jpg", options: { size: true } }, { signing: key });
    expect(await client.infoUrl(path)).toEqual(infoBody);
    expect(await client.infoUrl(`${base}${path}`)).toEqual(infoBody);
  });

  test("a non-envelope error body is kind unknown", async () => {
    const err = await client.infoUrl("/proxy-error").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(XtandardError);
    expect([(err as XtandardError).status, (err as XtandardError).kind]).toEqual([502, "unknown"]);
  });

  test("a custom fetch and an abort signal", async () => {
    const calls: string[] = [];
    const c = createClient({
      baseUrl: "http://xi.invalid",
      fetch: async (input, init) => {
        calls.push(String(input));
        init?.signal?.throwIfAborted();
        return new Response("ok\n");
      },
    });
    await c.healthz();
    expect(calls).toEqual(["http://xi.invalid/healthz"]);
    const ac = new AbortController();
    ac.abort();
    await expect(c.healthz({ signal: ac.signal })).rejects.toThrow();
  });
});

describe("HTTP QUERY (opt-in)", () => {
  type Sent = { url: string; method: string; type: string | null; body: string };
  const recorder = () => {
    const sent: Sent[] = [];
    const fetch = async (input: Request | string | URL, init?: RequestInit) => {
      const h = new Headers(init?.headers);
      sent.push({ url: String(input), method: init?.method ?? "GET", type: h.get("content-type"), body: String(init?.body ?? "") });
      return new Response("img");
    };
    return { sent, fetch };
  };
  const q = "/q/?url=https://e.com/a.jpg&w=300&xi_sig=abc";

  test("off by default: query dialect URLs go out as GET", async () => {
    const r = recorder();
    await createClient({ baseUrl: "http://xi.invalid", fetch: r.fetch }).media(q);
    expect(r.sent).toEqual([{ url: `http://xi.invalid${q}`, method: "GET", type: null, body: "" }]);
  });

  test("queryMethod sends URLs under the mount as QUERY, others as GET", async () => {
    const r = recorder();
    const c = createClient({ baseUrl: "http://xi.invalid", fetch: r.fetch, queryMethod: true });
    await c.media(q);
    await c.media("/q/video?url=s3://m/a.mov&vd=5");
    await c.media("/insecure/rs:fit:300:300/plain/https://e.com/a.jpg");
    await c.media(q, { method: "HEAD" });
    expect(r.sent).toEqual([
      { url: "http://xi.invalid/q/", method: "QUERY", type: "application/x-www-form-urlencoded", body: "url=https://e.com/a.jpg&w=300&xi_sig=abc" },
      { url: "http://xi.invalid/q/video", method: "QUERY", type: "application/x-www-form-urlencoded", body: "url=s3://m/a.mov&vd=5" },
      { url: "http://xi.invalid/insecure/rs:fit:300:300/plain/https://e.com/a.jpg", method: "GET", type: null, body: "" },
      { url: `http://xi.invalid${q}`, method: "HEAD", type: null, body: "" },
    ]);
  });

  test("method QUERY on one call, with a custom mount", async () => {
    const r = recorder();
    const c = createClient({ baseUrl: "http://xi.invalid", fetch: r.fetch, queryMount: "/img/q/" });
    await c.media("/img/q/resize?url=x&width=1", { method: "QUERY" });
    expect(r.sent[0]).toEqual({ url: "http://xi.invalid/img/q/resize", method: "QUERY", type: "application/x-www-form-urlencoded", body: "url=x&width=1" });
  });

  test("queryMethodRequest refuses a URL without a query", () => {
    expect(() => queryMethodRequest("/q/")).toThrow(TypeError);
    expect(() => queryMethodRequest("/q/?")).toThrow(TypeError);
    expect(queryMethodRequest("https://h/q/?url=x")).toEqual({
      method: "QUERY", url: "https://h/q/", contentType: "application/x-www-form-urlencoded", body: "url=x",
    });
  });
});

test("toXtandardError on a raw Response", async () => {
  const e = await toXtandardError(
    Response.json({ error: { kind: "overloaded", message: "too many requests" } }, { status: 429 }),
  );
  expect([e.status, e.kind, e.message, e.requestId]).toEqual([429, "overloaded", "too many requests", undefined]);
  expect(e.name).toBe("XtandardError");
});
