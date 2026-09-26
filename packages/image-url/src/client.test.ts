import { afterAll, describe, expect, test } from "bun:test";
import { createClient, type InfoResponse, toXtandardError, XtandardError } from "./client.ts";
import { buildImgproxyInfoUrl } from "./builder.ts";

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

test("toXtandardError on a raw Response", async () => {
  const e = await toXtandardError(
    Response.json({ error: { kind: "overloaded", message: "too many requests" } }, { status: 429 }),
  );
  expect([e.status, e.kind, e.message, e.requestId]).toEqual([429, "overloaded", "too many requests", undefined]);
  expect(e.name).toBe("XtandardError");
});
