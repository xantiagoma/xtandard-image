// testdata/security/vectors.json: the signing, enc/ and pdf_password vectors shared with Go and TypeScript.

import Testing

@testable import XtandardImage

private func vectors(_ kind: String) throws -> [JSONValue] {
  try loadJSON("testdata/security/vectors.json")[kind]!.array
}

/// Decrypts a token with the platform AES-CBC (CommonCrypto or swift-crypto).
private func decrypt(_ token: String, _ keyHex: String) -> String? {
  let raw = unbase64URL(token)
  guard
    let pt = aesCBCDecrypt(
      key: unhex(keyHex), iv: Array(raw.prefix(16)), ciphertext: Array(raw.dropFirst(16)))
  else { return nil }
  return String(decoding: pt, as: UTF8.self)
}

@Suite struct VectorTests {
  @Test func imgproxySignatures() throws {
    let vs = try vectors("imgproxy")
    #expect(vs.count >= 3)
    for v in vs {
      let key = try SigningKey(key: v["key"]!.string, salt: v["salt"]!.string, size: v["size"]!.int)
      let path = v["path"]!.string
      #expect(sign(path, key: key) == v["signature"]!.string, "\(v["name"]!.string)")
      let p = path.hasPrefix("/") ? path : "/" + path
      #expect(signImgproxyPath(path, key: key) == "/" + v["signature"]!.string + p)
      let raw = try SigningKey(
        keyBytes: unhex(v["key"]!.string), saltBytes: unhex(v["salt"]!.string), size: v["size"]!.int
      )
      #expect(sign(path, key: raw) == v["signature"]!.string, "\(v["name"]!.string) (raw bytes)")
    }
    print("imgproxy vectors: \(vs.count)")
  }

  @Test func xiSig() throws {
    let vs = try vectors("xi_sig")
    #expect(vs.count >= 3)
    for v in vs {
      let key = try SigningKey(key: v["key"]!.string, salt: v["salt"]!.string, size: v["size"]!.int)
      let (path, query, signed) = (v["path"]!.string, v["query"]!.string, v["signed_query"]!.string)
      #expect(
        try signXiQuery(path: path, rawQuery: query, key: key) == signed, "\(v["name"]!.string)")
      #expect(try signXiQuery(path: path, rawQuery: signed, key: key) == signed, "re-signed")
      #expect(
        try signXiURL("https://img.example.com\(path)?\(query)", key: key)
          == "https://img.example.com\(path)?\(signed)")
    }
    print("xi_sig vectors: \(vs.count)")
  }

  @Test func encryption() throws {
    let vs = try vectors("encryption")
    #expect(vs.count >= 3)
    for v in vs {
      let key = try EncryptionKey(key: v["key"]!.string, iv: v["iv"]!.string)
      let token = try encryptSource(v["plaintext"]!.string, key: key)
      #expect(token == v["token"]!.string, "\(v["name"]!.string)")
      #expect(decrypt(token, v["key"]!.string) == v["plaintext"]!.string)
    }
    print("encryption vectors: \(vs.count)")
  }

  @Test func pdfPassword() throws {
    let vs = try vectors("pdf_password")
    #expect(vs.count >= 3)
    for v in vs {
      let key = try EncryptionKey(key: v["key"]!.string, iv: v["iv"]!.string)
      #expect(
        try encryptPDFPassword(v["password"]!.string, key: key) == v["token"]!.string,
        "\(v["name"]!.string)")
    }
    print("pdf_password vectors: \(vs.count)")
  }

  @Test func randomIV() throws {
    let keyHex = try vectors("pdf_password")[0]["key"]!.string
    let key = try EncryptionKey(key: keyHex)
    let a = try encryptPDFPassword("s3cret", key: key)
    let b = try encryptPDFPassword("s3cret", key: key)
    #expect(a != b)
    #expect(decrypt(a, keyHex) == "s3cret")
    #expect(decrypt(b, keyHex) == "s3cret")
  }

  @Test func xiMessageSplitsTheSignature() throws {
    let (sig, msg) = try xiMessage(path: "/", rawQuery: "url=x&&xi_sig=s&w=1")
    #expect(sig == "s")
    #expect(msg == "/?url=x&w=1")
    let (none, root) = try xiMessage(path: "/", rawQuery: "")
    #expect(none == nil)
    #expect(root == "/")
    #expect(throws: URLBuildError(.invalidRequest, "query has more than one xi_sig parameter")) {
      try xiMessage(path: "/", rawQuery: "xi_sig=a&xi_sig=b")
    }
  }

  @Test func keysAreNotPrinted() throws {
    let k = try SigningKey(
      key: String(repeating: "00", count: 32), salt: String(repeating: "11", count: 32))
    for printed in ["\(k)", String(reflecting: k), dumped(k)] {
      #expect(!printed.contains("00") && !printed.contains("11"), "\(printed)")
    }
    let e = try EncryptionKey(
      key: String(repeating: "22", count: 32), iv: String(repeating: "33", count: 16))
    for printed in ["\(e)", String(reflecting: e), dumped(e)] {
      #expect(
        !printed.contains("22") && !printed.contains("33") && !printed.contains("34"), "\(printed)")
    }
  }
}

private func dumped(_ v: some Any) -> String {
  var s = ""
  dump(v, to: &s)
  return s
}
