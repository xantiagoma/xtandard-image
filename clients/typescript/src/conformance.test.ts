// The official clients' conformance fixture, clients/spec/conformance.json
// (ADR 0013): language-neutral inputs, the values every client must
// produce, and the error code it must raise. This package is the reference
// implementation: XI_UPDATE_FIXTURES=1 writes expect.value from its output
// (task clients:fixtures); internal/dialect/imgproxy TestConformanceFixture
// then parses every URL with the server's parsers. Without the variable
// this test asserts the recorded values and that every error case throws.

import { describe, expect, test } from "bun:test";
import { rangeHeader } from "./client.ts";
import {
  type Arg,
  buildImaginaryUrl,
  buildImgproxyInfoUrl,
  buildImgproxyStreamUrl,
  buildImgproxyUrl,
  buildImgproxyVideoUrl,
  buildQueryStreamUrl,
  buildQueryUrl,
  buildQueryVideoUrl,
  encryptPdfPassword,
  encryptSource,
  escapePlainSource,
  escapeQueryValue,
  type ImgproxyUrlConfig,
  queryMethodRequest,
  type QueryUrlConfig,
  sign,
  signXiUrl,
  sourceUrl,
  xiSource,
} from "./index.ts";

type KeyPair = { key: string; salt: string };
type Cipher = { key: string; iv?: string };

interface CaseConfig {
  base_url?: string;
  signing?: true | KeyPair;
  signature_size?: number;
  unsigned_signature?: string;
  encryption?: true | Cipher;
  source_encoding?: "base64" | "plain" | "encrypted";
  split_source?: number;
  separator?: string;
  option_names?: "short" | "canonical" | "given";
  mount?: string;
}

interface Case {
  name: string;
  op: string;
  config?: CaseConfig;
  input: Record<string, unknown>;
  expect?: { value?: string; error?: string };
}

interface Fixture {
  keys: { signing: KeyPair; encryption: Required<Cipher> };
  cases: Case[];
}

const file = new URL("../../../clients/spec/conformance.json", import.meta.url);
const fixture = (await Bun.file(file).json()) as Fixture;
const update = process.env["XI_UPDATE_FIXTURES"] === "1";

function imgproxyConfig(c: CaseConfig = {}): ImgproxyUrlConfig & QueryUrlConfig {
  const signing = c.signing === true ? fixture.keys.signing : c.signing;
  const encryption = c.encryption === true ? fixture.keys.encryption : c.encryption;
  return {
    ...(c.base_url !== undefined ? { baseUrl: c.base_url } : {}),
    ...(signing ? { signing: { ...signing, ...(c.signature_size !== undefined ? { size: c.signature_size } : {}) } } : {}),
    ...(c.unsigned_signature !== undefined ? { unsignedSignature: c.unsigned_signature } : {}),
    ...(encryption ? { encryption } : {}),
    ...(c.source_encoding !== undefined ? { sourceEncoding: c.source_encoding } : {}),
    ...(c.split_source !== undefined ? { splitSource: c.split_source } : {}),
    ...(c.separator !== undefined ? { separator: c.separator } : {}),
    ...(c.option_names !== undefined ? { optionNames: c.option_names } : {}),
    ...(c.mount !== undefined ? { mount: c.mount } : {}),
  };
}

/** The fixture's pipelines ([[name, args], …] per pipeline) as option objects. */
function optionObjects(input: Record<string, unknown>): Record<string, Arg[]>[] | undefined {
  const pipelines = input["options"] as [string, Arg[]][][] | undefined;
  return pipelines?.map((pl) => Object.fromEntries(pl));
}

function common(input: Record<string, unknown>) {
  const out: Record<string, unknown> = { source: input["source"] };
  for (const [from, to] of [
    ["extension", "extension"],
    ["source_encoding", "sourceEncoding"],
    ["pdf_password", "pdfPassword"],
    ["protocol", "protocol"],
    ["renditions", "renditions"],
    ["segment_duration", "segmentDuration"],
    ["segment_format", "segmentFormat"],
  ] as const) {
    if (input[from] !== undefined) out[to] = input[from];
  }
  return out;
}

async function run(c: Case): Promise<string> {
  const cfg = imgproxyConfig(c.config);
  const input = c.input;
  const opts = optionObjects(input);
  const params = input["params"] as [string, string | number | boolean | null][] | undefined;
  const single = opts && opts.length === 1 ? opts[0] : opts;
  switch (c.op) {
    case "imgproxy.url":
      return buildImgproxyUrl({ ...common(input), ...(opts ? { options: opts } : {}) } as never, cfg);
    case "imgproxy.info_url":
      return buildImgproxyInfoUrl({ ...common(input), ...(opts ? { options: single } : {}) } as never, cfg);
    case "imgproxy.video_url":
      return buildImgproxyVideoUrl({ ...common(input), ...(opts ? { options: single } : {}) } as never, cfg);
    case "imgproxy.stream_url":
      return buildImgproxyStreamUrl({ ...common(input), ...(opts ? { options: opts } : {}) } as never, cfg);
    case "query.url":
      return buildQueryUrl({ ...common(input), ...(opts ? { options: opts } : {}), ...(params ? { params } : {}) } as never, cfg);
    case "query.video_url":
      return buildQueryVideoUrl({ ...common(input), ...(opts ? { options: opts } : {}) } as never, cfg);
    case "query.stream_url":
      return buildQueryStreamUrl({ ...common(input), ...(opts ? { options: opts } : {}) } as never, cfg);
    case "query.imaginary_url":
      return buildImaginaryUrl({ endpoint: input["endpoint"], source: input["source"], ...(params ? { params } : {}) } as never, cfg);
    case "query.query_request": {
      // input.build names the query op that builds the URL from the other inputs.
      const q = queryMethodRequest(await run({ ...c, op: input["build"] as string }));
      return `${q.method} ${q.url}\nContent-Type: ${q.contentType}\n\n${q.body}`;
    }
    case "sign":
      return sign(input["message"] as string, cfg.signing!);
    case "sign_xi_url":
      return signXiUrl(input["url"] as string, cfg.signing!, input["mount"] !== undefined ? { mount: input["mount"] as string } : {});
    case "encrypt_source":
      return encryptSource(input["source"] as string, cfg.encryption!);
    case "encrypt_pdf_password":
      return encryptPdfPassword(input["password"] as string, cfg.encryption!);
    case "xi_source":
      return xiSource(input["name"] as string, input["key"] as string);
    case "source_url":
      return sourceUrl(input["scheme"] as never, input["authority"] as string, input["key"] as string);
    case "range_header":
      return rangeHeader(input["range"] as never);
    case "escape_plain_source":
      return escapePlainSource(input["value"] as string);
    case "escape_query_value":
      return escapeQueryValue(input["value"] as string);
    default:
      throw new Error(`unknown op ${c.op}`);
  }
}

describe("conformance.json", () => {
  test("has cases for every op", () => {
    const ops = new Set(fixture.cases.map((c) => c.op));
    expect(ops.size).toBeGreaterThanOrEqual(17);
  });

  for (const c of fixture.cases) {
    test(`${c.op} ${c.name}`, async () => {
      if (c.expect?.error !== undefined) {
        let threw = false;
        try {
          await run(c);
        } catch {
          threw = true;
        }
        expect(threw).toBe(true);
        return;
      }
      const got = await run(c);
      if (update) {
        c.expect = { value: got };
        return;
      }
      expect(c.expect?.value).toBeDefined();
      expect(got).toBe(c.expect!.value!);
    });
  }
});

if (update) {
  const { afterAll } = await import("bun:test");
  afterAll(async () => {
    await Bun.write(file, `${JSON.stringify(fixture, null, 2)}\n`);
  });
}
