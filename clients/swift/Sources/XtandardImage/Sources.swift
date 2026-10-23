// Source references (docs/URL-DIALECTS.md §3.1): the string a URL carries as
// its source. These helpers only join strings; the URL builders escape or
// encode the result:
//
//   https://host/path         an http source
//   s3://bucket/key           gs://bucket/key   abs://container/key
//   local:///path             ftp://host/path   sftp://host/path
//   xi://<source>/<key>       a named source (XI_SOURCES)

/// Whether `name` matches `sources.xi_name_pattern` (`^[A-Za-z0-9][A-Za-z0-9_.-]*$`).
private func isSourceName(_ name: String) -> Bool {
  guard let first = name.utf8.first, isASCIIAlphanumeric(first) else { return false }
  return name.utf8.allSatisfy {
    isASCIIAlphanumeric($0) || $0 == UInt8(ascii: "_") || $0 == UInt8(ascii: ".")
      || $0 == UInt8(ascii: "-")
  }
}

private func trimLeadingSlashes(_ s: String) -> String {
  String(s.drop { $0 == "/" })
}

/// A named source reference, `xi://<name>/<key>`.
///
/// The key may be relative or, for an http source, an absolute URL; leading
/// slashes are dropped.
///
/// ```swift
/// try xiSource("photos", "2026/cat.jpg")  // "xi://photos/2026/cat.jpg"
/// ```
///
/// - Throws: ``URLBuildError/Code/invalidName`` when the name is not a source name.
public func xiSource(_ name: String, _ key: String) throws(URLBuildError) -> String {
  guard isSourceName(name) else {
    throw URLBuildError(.invalidName, "invalid source name \"\(name)\"")
  }
  return "xi://\(name)/\(trimLeadingSlashes(key))"
}

/// A source reference for a scheme: the bucket, container, host or source
/// name, then the key. `local` takes only a path (`local:///<path>`).
///
/// ```swift
/// try sourceURL(.s3, "photos", "cat.jpg")      // "s3://photos/cat.jpg"
/// try sourceURL(.local, "", "img/cat.jpg")     // "local:///img/cat.jpg"
/// ```
///
/// - Throws: ``URLBuildError/Code/invalidName`` when the authority is missing, has `/`, `?`,
///   `#` or whitespace, or is given for `local`.
public func sourceURL(_ scheme: SourceScheme, _ authority: String, _ key: String)
  throws(URLBuildError) -> String
{
  let key = trimLeadingSlashes(key)
  switch scheme {
  case .xi:
    return try xiSource(authority, key)
  case .local:
    guard authority.isEmpty else {
      throw URLBuildError(.invalidName, "local sources have no authority: local:///<path>")
    }
    return "local:///\(key)"
  default:
    let bad = authority.unicodeScalars.contains {
      $0 == "/" || $0 == "?" || $0 == "#" || $0.properties.isWhitespace
    }
    if authority.isEmpty || bad {
      throw URLBuildError(.invalidName, "invalid \(scheme.rawValue) authority \"\(authority)\"")
    }
    return "\(scheme.rawValue)://\(authority)/\(key)"
  }
}

/// A byte range of a `Range` request header.
///
/// ```swift
/// try rangeHeader(.from(0))        // "bytes=0-"
/// try rangeHeader(.bytes(5, 9))    // "bytes=5-9"
/// try rangeHeader(.suffix(500))    // "bytes=-500"
/// try rangeHeader(.raw("bytes=1-2"))
/// ```
public enum ByteRange: Sendable, Hashable, ExpressibleByStringLiteral {
  /// Bytes `start` to `end` (inclusive), or to the end when `end` is `nil`.
  case range(start: Int, end: Int?)
  /// The last `length` bytes.
  case suffix(Int)
  /// A header value written verbatim.
  case raw(String)

  /// Bytes `start` to `end`, inclusive.
  public static func bytes(_ start: Int, _ end: Int) -> ByteRange { .range(start: start, end: end) }
  /// Bytes from `start` to the end.
  public static func from(_ start: Int) -> ByteRange { .range(start: start, end: nil) }

  /// A closed range: `0...99` is `bytes=0-99`.
  public init(_ r: ClosedRange<Int>) { self = .range(start: r.lowerBound, end: r.upperBound) }
  /// A half-open range: `0..<100` is `bytes=0-99`.
  public init(_ r: Range<Int>) { self = .range(start: r.lowerBound, end: r.upperBound - 1) }
  /// A range from a start: `100...` is `bytes=100-`.
  public init(_ r: PartialRangeFrom<Int>) { self = .range(start: r.lowerBound, end: nil) }
  public init(stringLiteral value: String) { self = .raw(value) }
}

/// Formats a byte range as a `Range` header value (`bytes=0-1023`, `bytes=-500`).
///
/// - Throws: ``URLBuildError/Code/invalidRange`` for a negative value or an end before the start.
public func rangeHeader(_ range: ByteRange) throws(URLBuildError) -> String {
  switch range {
  case .raw(let s):
    return s
  case .suffix(let n):
    guard n >= 0 else { throw URLBuildError(.invalidRange, "range suffix \(n) is negative") }
    return "bytes=-\(n)"
  case .range(let start, let end):
    guard start >= 0 else { throw URLBuildError(.invalidRange, "range start \(start) is negative") }
    guard let end else { return "bytes=\(start)-" }
    guard end >= start else {
      throw URLBuildError(.invalidRange, "range end \(end) is before start \(start)")
    }
    return "bytes=\(start)-\(end)"
  }
}
