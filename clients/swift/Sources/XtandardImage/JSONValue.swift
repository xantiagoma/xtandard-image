/// An untyped JSON value: the free-form parts of `/info` (`iptc`, `photoshop`,
/// `video_meta`, `xmp`) and the playground's `api/meta`.
public enum JSONValue: Sendable, Hashable, Codable {
  /// `null`.
  case null
  /// `true` or `false`.
  case bool(Bool)
  /// A number.
  case number(Double)
  /// A string.
  case string(String)
  /// An array.
  case array([JSONValue])
  /// An object.
  case object([String: JSONValue])

  public init(from decoder: any Decoder) throws {
    let c = try decoder.singleValueContainer()
    if c.decodeNil() {
      self = .null
    } else if let b = try? c.decode(Bool.self) {
      self = .bool(b)
    } else if let n = try? c.decode(Double.self) {
      self = .number(n)
    } else if let s = try? c.decode(String.self) {
      self = .string(s)
    } else if let a = try? c.decode([JSONValue].self) {
      self = .array(a)
    } else {
      self = .object(try c.decode([String: JSONValue].self))
    }
  }

  public func encode(to encoder: any Encoder) throws {
    var c = encoder.singleValueContainer()
    switch self {
    case .null: try c.encodeNil()
    case .bool(let b): try c.encode(b)
    case .number(let n): try c.encode(n)
    case .string(let s): try c.encode(s)
    case .array(let a): try c.encode(a)
    case .object(let o): try c.encode(o)
    }
  }

  /// The value of a key of an object.
  public subscript(key: String) -> JSONValue? {
    if case .object(let o) = self { return o[key] }
    return nil
  }

  /// The string, if this is one.
  public var stringValue: String? {
    if case .string(let s) = self { return s }
    return nil
  }

  /// The number, if this is one.
  public var numberValue: Double? {
    if case .number(let n) = self { return n }
    return nil
  }
}
