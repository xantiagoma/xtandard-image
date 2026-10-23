// Source URL and PDF password encryption, byte-compatible with the server
// (internal/security/crypt):
//
//   token = base64url_unpadded(IV[16] ‖ AES-CBC(key, IV, PKCS#7(utf8(plaintext))))

/// An encryption key: the first of `XI_ENCRYPTION_KEYS` (imgproxy `IMGPROXY_SOURCE_URL_ENCRYPTION_KEY`).
///
/// Without a fixed IV every token gets a fresh random one, as the server's own
/// encoder does. A fixed IV makes URLs deterministic and reveals when two URLs
/// share a source; use it only for tests or cache keys. Its description and
/// mirror never show the key or the IV.
///
/// ```swift
/// let key = try EncryptionKey(
///     key: "1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1",
///     iv: "30313233343536373839616263646566")
/// try encryptSource("http://img.example.com/pretty/image.jpg", key: key)
/// // "MDEyMzQ1Njc4OWFiY2RlZj206ZwwVKiBydg5ip7OI2oVBFCdcMXXGze1K8CjwiQuFTiJkTDmRJeJccTs0wH06w"
/// ```
///
/// > Important: The key decrypts every `enc/` source and PDF password. Keep it on a
/// > server: a key embedded in an app binary can be extracted.
public struct EncryptionKey: Sendable, Equatable, CustomStringConvertible, CustomReflectable {
  let key: [UInt8]
  let iv: [UInt8]?

  /// An AES key (16, 24 or 32 bytes) and an optional fixed IV (16 bytes), as hex strings.
  ///
  /// - Throws: ``URLBuildError/Code/invalidKey`` when a string is not hex or has the wrong size.
  public init(key: String, iv: String? = nil) throws(URLBuildError) {
    guard let k = decodeHex(key) else {
      throw URLBuildError(.invalidKey, "encryption key is not a hex string")
    }
    var ivBytes: [UInt8]?
    if let iv {
      guard let v = decodeHex(iv) else {
        throw URLBuildError(.invalidKey, "iv is not a hex string")
      }
      ivBytes = v
    }
    try self.init(keyBytes: k, ivBytes: ivBytes)
  }

  /// An AES key (16, 24 or 32 bytes) and an optional fixed IV (16 bytes), as raw bytes.
  ///
  /// - Throws: ``URLBuildError/Code/invalidKey`` when a size is wrong.
  public init(keyBytes: some Sequence<UInt8>, ivBytes: [UInt8]? = nil) throws(URLBuildError) {
    let k = Array(keyBytes)
    guard SpecConstants.encryptionKeySizes.contains(k.count) else {
      throw URLBuildError(.invalidKey, "encryption key is \(k.count) bytes, want 16, 24 or 32")
    }
    if let ivBytes, ivBytes.count != SpecConstants.ivSize {
      throw URLBuildError(.invalidKey, "iv is not \(SpecConstants.ivSize) bytes")
    }
    key = k
    iv = ivBytes
  }

  /// The AES key size in bits.
  public var bits: Int { key.count * 8 }
  /// Whether every token uses the same IV.
  public var hasFixedIV: Bool { iv != nil }

  public var description: String { "EncryptionKey(bits: \(bits), fixedIV: \(hasFixedIV))" }
  public var customMirror: Mirror { Mirror(self, children: ["bits": bits, "fixedIV": hasFixedIV]) }

  /// Seals a plaintext into a token.
  func seal(_ plaintext: String) -> String {
    let iv = self.iv ?? randomBytes(SpecConstants.ivSize)
    return base64URL(iv + aesCBCEncrypt(key: key, iv: iv, plaintext: Array(plaintext.utf8)))
  }
}

/// Encrypts a source URL into an `enc/` token.
///
/// - Throws: ``URLBuildError/Code/invalidSource`` when the source is empty or has control characters.
public func encryptSource(_ source: String, key: EncryptionKey) throws(URLBuildError) -> String {
  try checkText(source, .invalidSource, "source")
  return key.seal(source)
}

/// Encrypts the password of a password-protected PDF into a `pdf_password` (`pdp`) token.
///
/// The token is the `enc/` format over the password, with the same key. The
/// server accepts the option on signed URLs only.
///
/// - Throws: ``URLBuildError/Code/invalidPassword`` when the password is empty, has control
///   characters or is longer than 1024 UTF-8 bytes.
public func encryptPDFPassword(_ password: String, key: EncryptionKey) throws(URLBuildError)
  -> String
{
  try checkText(password, .invalidPassword, "password")
  if password.utf8.count > SpecConstants.maxPDFPasswordBytes {
    throw URLBuildError(
      .invalidPassword, "password is longer than \(SpecConstants.maxPDFPasswordBytes) bytes")
  }
  return key.seal(password)
}
