// The two primitives the client needs, from the platform:
//
// - Apple platforms: HMAC-SHA256 from CryptoKit and AES-CBC with PKCS#7
//   padding from CommonCrypto (CCCrypt), both in the OS; no dependency.
// - Linux and the other non-Apple platforms: swift-crypto (Apache-2.0), whose
//   `Crypto` module is the CryptoKit API on BoringSSL and whose `_CryptoExtras`
//   module has AES-CBC with PKCS#7 padding (`AES._CBC`).
//
// Random IVs come from SystemRandomNumberGenerator, which is cryptographically
// secure on every platform Swift supports.

#if canImport(CryptoKit)
  import CryptoKit
#else
  import Crypto
#endif

#if canImport(CommonCrypto)
  import CommonCrypto
#else
  import _CryptoExtras
#endif

/// HMAC-SHA256 of the concatenated parts.
func hmacSHA256(key: [UInt8], _ parts: [UInt8]...) -> [UInt8] {
  var mac = HMAC<SHA256>(key: SymmetricKey(data: key))
  for p in parts { mac.update(data: p) }
  return Array(mac.finalize())
}

/// AES-CBC encryption with PKCS#7 padding; the key is 16, 24 or 32 bytes and the IV 16.
func aesCBCEncrypt(key: [UInt8], iv: [UInt8], plaintext: [UInt8]) -> [UInt8] {
  #if canImport(CommonCrypto)
    guard let out = ccCrypt(CCOperation(kCCEncrypt), key: key, iv: iv, input: plaintext) else {
      preconditionFailure("CCCrypt failed with checked key and IV sizes")
    }
    return out
  #else
    do {
      let data = try AES._CBC.encrypt(
        plaintext, using: SymmetricKey(data: key), iv: AES._CBC.IV(ivBytes: iv))
      return Array(data)
    } catch {
      preconditionFailure("AES-CBC failed with checked key and IV sizes: \(error)")
    }
  #endif
}

/// AES-CBC decryption with PKCS#7 padding (the tests check tokens with it).
func aesCBCDecrypt(key: [UInt8], iv: [UInt8], ciphertext: [UInt8]) -> [UInt8]? {
  #if canImport(CommonCrypto)
    return ccCrypt(CCOperation(kCCDecrypt), key: key, iv: iv, input: ciphertext)
  #else
    return try? Array(
      AES._CBC.decrypt(ciphertext, using: SymmetricKey(data: key), iv: AES._CBC.IV(ivBytes: iv)))
  #endif
}

#if canImport(CommonCrypto)
  private func ccCrypt(_ op: CCOperation, key: [UInt8], iv: [UInt8], input: [UInt8]) -> [UInt8]? {
    var out = [UInt8](repeating: 0, count: input.count + kCCBlockSizeAES128)
    var moved = 0
    let outCount = out.count
    let status = CCCrypt(
      op, CCAlgorithm(kCCAlgorithmAES), CCOptions(kCCOptionPKCS7Padding),
      key, key.count, iv, input, input.count, &out, outCount, &moved)
    guard status == CCCryptorStatus(kCCSuccess) else { return nil }
    return Array(out.prefix(moved))
  }
#endif

/// Cryptographically secure random bytes.
func randomBytes(_ count: Int) -> [UInt8] {
  var rng = SystemRandomNumberGenerator()
  return (0..<count).map { _ in UInt8.random(in: .min ... .max, using: &rng) }
}
