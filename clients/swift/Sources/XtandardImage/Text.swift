import Foundation

/// Decodes a hex string (either case, surrounding whitespace ignored).
func decodeHex(_ text: String) -> [UInt8]? {
  let s = Array(text.trimmingCharacters(in: .whitespacesAndNewlines).utf8)
  guard s.count.isMultiple(of: 2) else { return nil }
  func digit(_ c: UInt8) -> UInt8? {
    switch c {
    case UInt8(ascii: "0")...UInt8(ascii: "9"): c - UInt8(ascii: "0")
    case UInt8(ascii: "a")...UInt8(ascii: "f"): c - UInt8(ascii: "a") + 10
    case UInt8(ascii: "A")...UInt8(ascii: "F"): c - UInt8(ascii: "A") + 10
    default: nil
    }
  }
  var out: [UInt8] = []
  out.reserveCapacity(s.count / 2)
  var i = 0
  while i < s.count {
    guard let hi = digit(s[i]), let lo = digit(s[i + 1]) else { return nil }
    out.append(hi << 4 | lo)
    i += 2
  }
  return out
}

/// Unpadded base64url (RFC 4648 §5).
func base64URL(_ bytes: some Sequence<UInt8>) -> String {
  var s = Data(bytes).base64EncodedString()
  s = s.replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_")
  while s.hasSuffix("=") { s.removeLast() }
  return s
}

/// Whether `s` contains an ASCII control character (C0 or DEL).
func hasControlCharacter(_ s: String) -> Bool {
  s.utf8.contains { $0 < 0x20 || $0 == 0x7f }
}

/// Rejects an empty string or one with control characters.
func checkText(_ s: String, _ code: URLBuildError.Code, _ what: String) throws(URLBuildError) {
  if s.isEmpty || hasControlCharacter(s) {
    throw URLBuildError(code, "\(what) must be a non-empty string without control characters")
  }
}

/// Rejects an extension that is not ASCII letters and digits (`url.extension_pattern`).
func checkExtension(_ ext: String) throws(URLBuildError) -> String {
  if ext.isEmpty || !ext.utf8.allSatisfy(isASCIIAlphanumeric) {
    throw URLBuildError(.invalidExtension, "invalid extension \"\(ext)\"")
  }
  return ext
}

func isASCIIAlphanumeric(_ b: UInt8) -> Bool {
  (b >= 0x30 && b <= 0x39) || (b >= 0x41 && b <= 0x5a) || (b >= 0x61 && b <= 0x7a)
}

private let plainSafeBytes = Set(SpecConstants.plainSafe.utf8)
private let querySafeBytes = Set(SpecConstants.querySafe.utf8)

private func escape(_ s: String, _ safe: Set<UInt8>) -> String {
  let hex = Array("0123456789ABCDEF".utf8)
  var out: [UInt8] = []
  out.reserveCapacity(s.utf8.count)
  for b in s.utf8 {
    if isASCIIAlphanumeric(b) || safe.contains(b) {
      out.append(b)
    } else {
      out.append(UInt8(ascii: "%"))
      out.append(hex[Int(b >> 4)])
      out.append(hex[Int(b & 0x0f)])
    }
  }
  return String(decoding: out, as: UTF8.self)
}

/// Percent-escapes a source for a `plain/` URL; the server path-unescapes it.
///
/// ASCII letters, digits and `url.plain_safe` (RFC 3986 unreserved characters,
/// the sub-delims, `:` and `/`) are kept; everything else (`%`, `@`, `?`, `#`,
/// spaces, non-ASCII) is percent-encoded as UTF-8 in upper case.
///
/// ```swift
/// escapePlainSource("https://e.com/a b@c%d?e#f;g=h")
/// // "https://e.com/a%20b%40c%25d%3Fe%23f;g=h"
/// ```
public func escapePlainSource(_ source: String) -> String {
  escape(source, plainSafeBytes)
}

/// Percent-escapes a query key or value, keeping `:`, `/`, `,` and the rest of
/// `url.query_safe` readable.
///
/// ```swift
/// escapeQueryValue("a&b=c+d e/f:g")  // "a%26b%3Dc%2Bd%20e/f:g"
/// ```
public func escapeQueryValue(_ value: String) -> String {
  escape(value, querySafeBytes)
}

/// Formats a number like ECMAScript `Number::toString` (`300`, `1.5`, `0.1`,
/// `1e+21`, `1e-7`), so URLs and their signatures match across languages.
func formatNumber(_ v: Double, _ location: String) throws(URLBuildError) -> String {
  guard v.isFinite else {
    throw URLBuildError(.invalidArgument, "\(location): \(v) is not a finite number")
  }
  if v == 0 { return "0" }
  let sign = v < 0 ? "-" : ""
  // Swift's description has the shortest round-trip digits: "1.5", "1e-07", "1.5e+22".
  let text = String(v.magnitude.description)
  let parts = text.split(separator: "e", maxSplits: 1)
  let mantissa = parts[0]
  let exponent = parts.count > 1 ? Int(parts[1]) ?? 0 : 0
  let pieces = mantissa.split(separator: ".", maxSplits: 1, omittingEmptySubsequences: false)
  let whole = Array(pieces[0])
  let fraction = pieces.count > 1 ? Array(pieces[1]) : []
  var digits = whole + fraction
  // |v| = 0.<digits> × 10^point (ECMAScript's k digits and exponent n).
  var point = whole.count + exponent
  while digits.count > 1, digits.first == "0" {
    digits.removeFirst()
    point -= 1
  }
  while digits.count > 1, digits.last == "0" { digits.removeLast() }
  let k = digits.count
  let d = String(digits)
  let zeros = { (n: Int) in String(repeating: "0", count: max(n, 0)) }
  let out: String
  if k <= point && point <= 21 {
    out = d + zeros(point - k)
  } else if 0 < point && point <= 21 {
    out = String(digits[..<point]) + "." + String(digits[point...])
  } else if -6 < point && point <= 0 {
    out = "0." + zeros(-point) + d
  } else {
    let e = point - 1
    let suffix = "e" + (e >= 0 ? "+" : "-") + String(abs(e))
    out = k == 1 ? d + suffix : String(digits[0]) + "." + String(digits[1...]) + suffix
  }
  return sign + out
}
