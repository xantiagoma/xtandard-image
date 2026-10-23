// URL signing, byte-compatible with the server (internal/security/sign).
//
// - imgproxy: base64url(HMAC-SHA256(key, salt ‖ path)[..size]), the signature
//   segment of /{signature}/{options}/{source}; /video URLs sign "/video" + path.
// - xi_sig: the same construction over path ‖ "?" ‖ query-without-xi_sig,
//   appended as the last query parameter (docs/URL-DIALECTS.md §2).

/// A signing key: `XI_SIGNING_KEYS` / `XI_SIGNING_SALTS` (imgproxy `IMGPROXY_KEY` / `IMGPROXY_SALT`).
///
/// Its description and mirror never show the key or the salt.
///
/// ```swift
/// let key = try SigningKey(key: "736563726574", salt: "68656C6C6F")  // hex, as in the server config
/// let short = try SigningKey(key: "736563726574", salt: "68656C6C6F", size: 8)
/// sign("/rs:fit:300:300/plain/a.jpg", key: short).count  // 11
/// ```
///
/// > Important: A signing key signs every URL. Keep it on a server: a key
/// > embedded in an app binary can be extracted.
public struct SigningKey: Sendable, Equatable, CustomStringConvertible, CustomReflectable {
  let key: [UInt8]
  let salt: [UInt8]
  /// The signature size in bytes before encoding (1 to 32, `XI_SIGNING_SIZE`).
  public let size: Int

  /// A key and salt as hex strings, as in the server config.
  ///
  /// - Throws: ``URLBuildError/Code/invalidKey`` when a string is not hex or the size is outside 1–32.
  public init(key: String, salt: String, size: Int = SpecConstants.defaultSignatureSize)
    throws(URLBuildError)
  {
    guard let k = decodeHex(key) else {
      throw URLBuildError(.invalidKey, "signing key is not a hex string")
    }
    guard let s = decodeHex(salt) else {
      throw URLBuildError(.invalidKey, "signing salt is not a hex string")
    }
    try self.init(keyBytes: k, saltBytes: s, size: size)
  }

  /// A key and salt as raw bytes.
  ///
  /// - Throws: ``URLBuildError/Code/invalidKey`` when the size is outside 1–32.
  public init(
    keyBytes: some Sequence<UInt8>, saltBytes: some Sequence<UInt8>,
    size: Int = SpecConstants.defaultSignatureSize
  ) throws(URLBuildError) {
    guard (SpecConstants.minSignatureSize...SpecConstants.maxSignatureSize).contains(size) else {
      throw URLBuildError(
        .invalidKey,
        "signature size \(size) out of range [\(SpecConstants.minSignatureSize), \(SpecConstants.maxSignatureSize)]"
      )
    }
    key = Array(keyBytes)
    salt = Array(saltBytes)
    self.size = size
  }

  public var description: String { "SigningKey(size: \(size))" }
  public var customMirror: Mirror { Mirror(self, children: ["size": size]) }
}

/// Signs a message with the salted HMAC-SHA256 construction of the imgproxy and `xi_sig` schemes.
///
/// Like the server, a message without a leading `/` is signed as if it had one.
///
/// ```swift
/// let key = try SigningKey(key: "736563726574", salt: "68656C6C6F")
/// sign("/rs:fill:300:400:0/g:sm/aHR0cDovL2V4YW1w/bGUuY29tL2ltYWdl/cy9jdXJpb3NpdHku/anBn.png", key: key)
/// // "oKfUtW34Dvo2BGQehJFR4Nr0_rIjOtdtzJ3QFsUcXH8"
/// ```
public func sign(_ message: String, key: SigningKey) -> String {
  let slash: [UInt8] = message.hasPrefix("/") ? [] : [UInt8(ascii: "/")]
  let digest = hmacSHA256(key: key.key, key.salt, slash, Array(message.utf8))
  return base64URL(digest.prefix(key.size))
}

/// `"/" + signature + path` for an imgproxy path (everything after the signature segment).
public func signImgproxyPath(_ path: String, key: SigningKey) -> String {
  let p = path.hasPrefix("/") ? path : "/" + path
  return "/" + sign(p, key: key) + p
}

/// Splits a raw query into its `xi_sig` value and the message `xi_sig` signs.
///
/// The message is the path, then `?` and the remaining non-empty parameters
/// in their original order and encoding.
///
/// ```swift
/// try xiMessage(path: "/", rawQuery: "url=x&&xi_sig=s&w=1")  // ("s", "/?url=x&w=1")
/// ```
///
/// - Throws: ``URLBuildError/Code/invalidRequest`` when the query has more than one `xi_sig`
///   (the server answers 403).
public func xiMessage(path: String, rawQuery: String) throws(URLBuildError) -> (
  signature: String?, message: String
) {
  var signature: String?
  var kept: [Substring] = []
  for part in rawQuery.split(separator: "&", omittingEmptySubsequences: true) {
    let key = part.split(separator: "=", maxSplits: 1, omittingEmptySubsequences: false)[0]
    if key == SpecConstants.xiSigParam {
      if signature != nil {
        throw URLBuildError(
          .invalidRequest, "query has more than one \(SpecConstants.xiSigParam) parameter")
      }
      signature = String(part.dropFirst(key.count + 1))
      continue
    }
    kept.append(part)
  }
  let message = kept.isEmpty ? path : path + "?" + kept.joined(separator: "&")
  return (signature, message)
}

/// `rawQuery` without any `xi_sig` and with a fresh `xi_sig` for `path` appended last.
///
/// `path` is the escaped path the dialect sees, after its mount prefix.
///
/// - Throws: ``URLBuildError/Code/invalidRequest`` when the query has more than one `xi_sig`.
public func signXiQuery(path: String, rawQuery: String, key: SigningKey) throws(URLBuildError)
  -> String
{
  let raw = rawQuery.hasPrefix("?") ? String(rawQuery.dropFirst()) : rawQuery
  let (_, message) = try xiMessage(path: path, rawQuery: raw)
  let sig = sign(message, key: key)
  let query = message.dropFirst(path.count)
  let param = "\(SpecConstants.xiSigParam)=\(sig)"
  return query.isEmpty ? param : String(query.dropFirst()) + "&" + param
}

/// The length of a leading `scheme://`, if `s` has one.
private func absolutePrefixLength(_ s: Substring) -> Int? {
  guard let r = s.range(of: "://") else { return nil }
  let scheme = s[..<r.lowerBound]
  guard let first = scheme.unicodeScalars.first, first.isASCII, first.properties.isAlphabetic
  else { return nil }
  let ok = scheme.unicodeScalars.allSatisfy {
    $0.isASCII
      && ($0.properties.isAlphabetic || ("0"..."9").contains($0)
        || "+.-".unicodeScalars.contains($0))
  }
  return ok ? scheme.count + 3 : nil
}

/// Signs a URL (absolute, or a path with an optional query) with `xi_sig`.
///
/// `mount` is the dialect's mount prefix, which the signed path excludes
/// (`"/q"` for the query dialect; `""` for none). A fragment is dropped.
///
/// ```swift
/// let key = try SigningKey(key: "736563726574", salt: "68656C6C6F")
/// try signXiURL("https://img.example.com/q/?url=https://e.com/a.jpg&w=300", key: key, mount: "/q")
/// // "https://img.example.com/q/?url=https://e.com/a.jpg&w=300&xi_sig=…"
/// ```
///
/// - Throws: ``URLBuildError/Code/invalidRequest`` when the path is not under the mount
///   or the query has two `xi_sig`.
public func signXiURL(_ url: String, key: SigningKey, mount: String = "") throws(URLBuildError)
  -> String
{
  let noHash = url.split(separator: "#", maxSplits: 1, omittingEmptySubsequences: false)[0]
  let headQuery = noHash.split(separator: "?", maxSplits: 1, omittingEmptySubsequences: false)
  let head = headQuery[0]
  let query = headQuery.count > 1 ? String(headQuery[1]) : ""
  var origin: Substring = ""
  var path: Substring = head
  if let start = absolutePrefixLength(head) {
    let rest = head.dropFirst(start)
    if let slash = rest.firstIndex(of: "/") {
      origin = head[..<slash]
      path = head[slash...]
    } else {
      origin = head
      path = "/"
    }
  }
  var m = mount
  while m.hasSuffix("/") { m.removeLast() }
  if !m.isEmpty {
    let under = path == m || (path.hasPrefix(m) && path.dropFirst(m.count).hasPrefix("/"))
    if !under {
      throw URLBuildError(.invalidRequest, "path \(path) is not under the mount \(m)")
    }
    path = path.dropFirst(m.count)
    if path.isEmpty { path = "/" }
  }
  let signed = try signXiQuery(path: String(path), rawQuery: query, key: key)
  return "\(origin)\(m)\(path)?\(signed)"
}
