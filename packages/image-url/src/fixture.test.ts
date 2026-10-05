// The cross-language fixture testdata/urls/imgproxy-builder.json: cases
// derived by the Go dialect tests from the WP-21a/21b golden tables
// (internal/dialect/imgproxy/tsbuilder_test.go). This test builds every
// case with the TS builder, in both the fluent and the object form, and
// asserts the URLs recorded in the file; the Go test parses those URLs and
// checks they yield the table case's request. XI_UPDATE_FIXTURES=1 writes
// the URLs instead.

import { describe, expect, test } from "bun:test";
import { type Arg, type ImgproxyUrlConfig, imgproxy, type SourceEncoding } from "./index.ts";

interface FixtureCase {
  name: string;
  config?: string;
  endpoint?: "info" | "video";
  separator?: string;
  signature_size?: number;
  encoding: SourceEncoding;
  source: string;
  extension?: string;
  pipelines: Record<string, Arg[]>[][];
  url?: string;
  url_short?: string;
}

interface Fixture {
  comment: string;
  signing: { key: string; salt: string };
  encryption: { key: string; iv: string };
  cases: FixtureCase[];
}

const file = new URL("../../../testdata/urls/imgproxy-builder.json", import.meta.url);
const fixture = (await Bun.file(file).json()) as Fixture;
const update = process.env["XI_UPDATE_FIXTURES"] === "1";

function config(c: FixtureCase, given: boolean): ImgproxyUrlConfig {
  return {
    unsignedSignature: "unsafe",
    encryption: fixture.encryption,
    ...(c.separator ? { separator: c.separator } : {}),
    ...(c.signature_size ? { signing: { ...fixture.signing, size: c.signature_size } } : {}),
    ...(given ? { optionNames: "given" as const } : {}),
  };
}

// The fixture's option names are runtime data, so option() is called untyped.
type Loose = { option(name: string, ...args: Arg[]): unknown };

/** Fluent form: one option() call per occurrence, chain() between pipelines. */
async function fluent(c: FixtureCase, given: boolean): Promise<string> {
  const b = imgproxy(config(c, given));
  if (c.endpoint === "info") {
    const chain = b.info(c.source).encoding(c.encoding);
    for (const entry of c.pipelines.flat()) {
      for (const [name, args] of Object.entries(entry)) (chain as unknown as Loose).option(name, ...args);
    }
    return chain.build();
  }
  if (c.endpoint === "video") {
    const chain = b.video(c.source).encoding(c.encoding);
    for (const entry of c.pipelines.flat()) {
      for (const [name, args] of Object.entries(entry)) (chain as unknown as Loose).option(name, ...args);
    }
    if (c.extension) chain.extension(c.extension);
    return chain.build();
  }
  const chain = b.from(c.source).encoding(c.encoding);
  c.pipelines.forEach((pl, i) => {
    if (i > 0) chain.chain();
    for (const entry of pl) {
      for (const [name, args] of Object.entries(entry)) (chain as unknown as Loose).option(name, ...args);
    }
  });
  if (c.extension) chain.extension(c.extension);
  return chain.build();
}

/** Object form, when no pipeline repeats an option (objects cannot). */
async function objectForm(c: FixtureCase, given: boolean): Promise<string | undefined> {
  const objects: Record<string, Arg[]>[] = [];
  for (const pl of c.pipelines) {
    const o: Record<string, Arg[]> = {};
    for (const entry of pl) {
      for (const [name, args] of Object.entries(entry)) {
        if (name in o) return undefined;
        o[name] = args;
      }
    }
    objects.push(o);
  }
  const b = imgproxy(config(c, given));
  if (c.endpoint === "info") {
    return b.infoUrl({ source: c.source, sourceEncoding: c.encoding, options: (objects[0] ?? {}) as never });
  }
  if (c.endpoint === "video") {
    return b.videoUrl({
      source: c.source,
      sourceEncoding: c.encoding,
      options: (objects[0] ?? {}) as never,
      ...(c.extension ? { extension: c.extension } : {}),
    });
  }
  return b.url({
    source: c.source,
    sourceEncoding: c.encoding,
    options: objects as never,
    ...(c.extension ? { extension: c.extension } : {}),
  });
}

describe("imgproxy-builder.json", () => {
  test("covers the table cases", () => expect(fixture.cases.length).toBeGreaterThanOrEqual(400));

  for (const c of fixture.cases) {
    test(c.name, async () => {
      const url = await fluent(c, true);
      const short = await fluent(c, false);
      const obj = await objectForm(c, true);
      if (obj !== undefined) expect(obj).toBe(url);
      if (update) {
        c.url = url;
        c.url_short = short;
        return;
      }
      expect(url).toBe(c.url!);
      expect(short).toBe(c.url_short!);
    });
  }
});

if (update) {
  // Runs after the tests above: bun runs top-level afterAll hooks last.
  const { afterAll } = await import("bun:test");
  afterAll(async () => {
    await Bun.write(file, `${JSON.stringify(fixture, null, 2)}\n`);
  });
}
