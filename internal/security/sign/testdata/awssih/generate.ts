// Generates vectors.json: AWS Serverless Image Handler ("Dynamic Image
// Transformation for Amazon CloudFront") URL signatures computed by the
// solution's own algorithm, for sign.AWSSIHMessage and the awssih scheme
// (docs/URL-DIALECTS.md §13).
//
//   bun internal/security/sign/testdata/awssih/generate.ts \
//     > internal/security/sign/testdata/awssih/vectors.json
//
// recreateQueryString and the hash computation are copied verbatim from
// source/image-handler/image-request.ts (validateRequestSignature,
// recreateQueryString), Copyright Amazon.com, Inc. or its affiliates,
// Apache-2.0; see NOTICE. The solution's module cannot be imported directly
// (it needs the AWS SDK), so the two functions are inlined. API Gateway's
// queryStringParameters is modelled with URLSearchParams (form decoding,
// last value wins for repeated keys).

import { createHmac } from "crypto";

// --- verbatim from image-request.ts -------------------------------------
function recreateQueryString(queryStringParameters: Record<string, string>): string {
  return Object.entries(queryStringParameters)
    .filter(([key]) => key !== "signature")
    .sort()
    .map(([key, value]) => [key, value].join("="))
    .join("&");
}

function sign(key: string, path: string, queryStringParameters: Record<string, string>): string {
  const queryString = recreateQueryString(queryStringParameters);
  const stringToSign = queryString !== "" ? [path, queryString].join("?") : path;
  return createHmac("sha256", key).update(stringToSign).digest("hex");
}
// ------------------------------------------------------------------------

function queryStringParameters(rawQuery: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of new URLSearchParams(rawQuery)) out[k] = v;
  return out;
}

function stringToSign(path: string, rawQuery: string): string {
  const q = recreateQueryString(queryStringParameters(rawQuery));
  return q !== "" ? [path, q].join("?") : path;
}

const b64 = (o: unknown) => "/" + Buffer.from(JSON.stringify(o)).toString("base64");

type Case = { name: string; secret: string; path: string; query: string };
const fixturePath =
  "/eyJidWNrZXQiOiJ2YWxpZEJ1Y2tldCIsImtleSI6InZhbGlkS2V5IiwiZWRpdHMiOnsidG9Gb3JtYXQiOiJwbmcifX0=";
const cases: Case[] = [
  // test/image-request/setup.spec.ts "Should pass when the image signature is correct".
  { name: "setup_spec_fixture", secret: "secret", path: fixturePath, query: "" },
  { name: "fixture_signature_only", secret: "secret", path: fixturePath, query: "signature=abc" },
  { name: "expires", secret: "secret", path: fixturePath, query: "expires=20991231T235959Z" },
  {
    name: "query_mapper", secret: "secret", path: fixturePath,
    query: "width=100&height=100&fit=cover&format=webp&signature=x&rotate=90&flip=true&flop=true&grayscale=true",
  },
  // test/image-request/recreate-query-string.spec.ts inputs.
  {
    name: "recreate_spec_1", secret: "secret", path: "/test.jpg",
    query: "signature=test-signature&expires=test-expires&format=png",
  },
  { name: "recreate_spec_2", secret: "secret", path: "/test.jpg", query: "grayscale=true&expires=test-expires&format=png" },
  { name: "thumbor_path", secret: "my-secret-key", path: "/fit-in/400x400/filters:grayscale()/image.jpg", query: "" },
  { name: "repeated_key_last_wins", secret: "secret", path: "/a.jpg", query: "width=1&width=2" },
  { name: "form_decoding", secret: "secret", path: "/a.jpg", query: "a=b+c&d=%2C%26&e" },
  { name: "sort_is_key_comma_value", secret: "secret", path: "/a.jpg", query: "a=1&a!=2&a-=3&ab=4" },
  { name: "utf16_sort", secret: "secret", path: "/a.jpg", query: "%F0%9F%98%80=1&%EF%BD%81=2&%C3%A9=3" },
  {
    name: "unicode_secret", secret: "sécrèt",
    path: b64({ bucket: "b", key: "k", edits: { resize: { width: 300 } } }), query: "",
  },
  {
    name: "json_edits", secret: "secret",
    path: b64({ key: "photos/cat.jpg", edits: { resize: { width: 200, height: 200, fit: "cover" }, toFormat: "webp" } }),
    query: "",
  },
];

const vectors = cases.map((c) => ({
  ...c,
  message: stringToSign(c.path, c.query),
  signature: sign(c.secret, c.path, queryStringParameters(c.query)),
}));
console.log(JSON.stringify(vectors, null, 2));
