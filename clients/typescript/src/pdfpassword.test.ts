import { describe, expect, test } from "bun:test";
import { fromHex } from "./bytes.ts";
import { buildImgproxyInfoUrl, encryptPdfPassword, imgproxy, MAX_PDF_PASSWORD_BYTES, signImgproxyPath } from "./index.ts";

// testdata/security/vectors.json, shared with internal/security/crypt
// (TestSharedPasswordVectors decrypts the same tokens and pins the digest).
const vectors = (await Bun.file(new URL("../../../testdata/security/vectors.json", import.meta.url)).json()) as {
  pdf_password: { name: string; key: string; iv: string; password: string; token: string }[];
};

const [secret] = vectors.pdf_password;
const signing = {
  key: "943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881",
  salt: "520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5",
};

async function decrypt(token: string, key: string): Promise<string> {
  const raw = Uint8Array.from(atob(token.replaceAll("-", "+").replaceAll("_", "/")), (c) => c.charCodeAt(0));
  const k = await crypto.subtle.importKey("raw", fromHex(key), "AES-CBC", false, ["decrypt"]);
  const pt = await crypto.subtle.decrypt({ name: "AES-CBC", iv: raw.subarray(0, 16) }, k, raw.subarray(16));
  return new TextDecoder().decode(pt);
}

describe("pdf_password vectors", () => {
  test("there are vectors", () => expect(vectors.pdf_password.length).toBeGreaterThanOrEqual(3));
  for (const v of vectors.pdf_password) {
    test(v.name, async () => {
      expect(await encryptPdfPassword(v.password, { key: v.key, iv: v.iv })).toBe(v.token);
      expect(await decrypt(v.token, v.key)).toBe(v.password);
    });
  }
});

describe("encryptPdfPassword", () => {
  test("random IV per call", async () => {
    const a = await encryptPdfPassword("s3cret", { key: secret!.key });
    const b = await encryptPdfPassword("s3cret", { key: secret!.key });
    expect(a).not.toBe(b);
    expect(await decrypt(a, secret!.key)).toBe("s3cret");
    expect(a).toMatch(/^[A-Za-z0-9_-]+$/);
  });
  test("refuses what the server refuses", async () => {
    await expect(encryptPdfPassword("", { key: secret!.key })).rejects.toThrow(TypeError);
    await expect(encryptPdfPassword("a\nb", { key: secret!.key })).rejects.toThrow(TypeError);
    await expect(encryptPdfPassword("é".repeat(MAX_PDF_PASSWORD_BYTES / 2 + 1), { key: secret!.key })).rejects.toThrow(
      RangeError,
    );
  });
});

describe("builder pdfPassword", () => {
  const encryption = { key: secret!.key, iv: secret!.iv };
  const img = imgproxy({ signing, encryption });

  test("object form: signed pdp segment first", async () => {
    const url = await img.url({ source: "s3://docs/a.pdf", options: { rs: ["fit", 800, 0] }, pdfPassword: "secret" });
    const path = `/pdp:${secret!.token}/rs:fit:800:0/czM6Ly9kb2NzL2EucGRm`;
    expect(url).toBe(await signImgproxyPath(path, signing));
  });

  test("fluent form and canonical names", async () => {
    const url = await imgproxy({ signing, encryption, optionNames: "canonical", sourceEncoding: "plain" })
      .from("s3://docs/a.pdf")
      .pdfPassword("secret")
      .width(300)
      .build();
    expect(url).toBe(await signImgproxyPath(`/pdf_password:${secret!.token}/width:300/plain/s3://docs/a.pdf`, signing));
  });

  test("info URLs", async () => {
    const url = await buildImgproxyInfoUrl(
      { source: "s3://docs/a.pdf", options: { size: true }, pdfPassword: "secret" },
      { signing, encryption },
    );
    expect(url.startsWith("/info/")).toBe(true);
    expect(url).toContain(`/pdp:${secret!.token}/s:1/`);
  });

  test("a separator that base64url uses is written verbatim", async () => {
    const url = await imgproxy({ signing, encryption: { key: secret!.key }, separator: "_" })
      .from("s3://docs/a.pdf")
      .pdfPassword("secret")
      .build();
    expect(url).toMatch(/\/pdp_[A-Za-z0-9_-]+\/czM6/);
  });

  test("needs encryption and signing", async () => {
    await expect(imgproxy({ signing }).url({ source: "s3://d/a.pdf", pdfPassword: "x" })).rejects.toThrow(
      /encryption key/,
    );
    await expect(imgproxy({ encryption }).url({ source: "s3://d/a.pdf", pdfPassword: "x" })).rejects.toThrow(/signing/);
  });
});
