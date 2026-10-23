/// One option argument or query value, as written in a URL.
///
/// Booleans are written `1`/`0` in options and `true`/`false` in native query
/// keys; numbers as ECMAScript writes them (`300`, `1.5`, `1e-7`);
/// ``null`` is an empty argument (`rs:fill:300::1`) and a skipped query key.
public enum Arg: Sendable, Hashable, ArgConvertible, ExpressibleByNilLiteral,
  ExpressibleByBooleanLiteral, ExpressibleByIntegerLiteral, ExpressibleByFloatLiteral,
  ExpressibleByStringLiteral
{
  /// An empty argument; a skipped query key.
  case null
  /// A boolean.
  case bool(Bool)
  /// An integer.
  case int(Int64)
  /// A floating-point number; it must be finite.
  case double(Double)
  /// A string, written verbatim in options (never escaped) and percent-encoded in query values.
  case string(String)

  /// A value's argument.
  public init(_ value: (any ArgConvertible)?) {
    self = value?.arg ?? .null
  }

  public init(nilLiteral: ()) { self = .null }
  public init(booleanLiteral value: Bool) { self = .bool(value) }
  public init(integerLiteral value: Int64) { self = .int(value) }
  public init(floatLiteral value: Double) { self = .double(value) }
  public init(stringLiteral value: String) { self = .string(value) }

  public var arg: Arg { self }

  /// The arguments of a variadic list; `nil` is ``null``.
  static func list(_ values: [(any ArgConvertible)?]) -> [Arg] {
    values.map { Arg($0) }
  }
}

/// A value that can be an option argument or a query value: numbers, booleans,
/// strings, ``Arg`` and every generated value enum.
public protocol ArgConvertible: Sendable {
  /// The argument.
  var arg: Arg { get }
}

extension ArgConvertible where Self: ParsableEnum {
  public var arg: Arg { .string(rawValue) }
}

extension Bool: ArgConvertible { public var arg: Arg { .bool(self) } }
extension String: ArgConvertible { public var arg: Arg { .string(self) } }
extension Substring: ArgConvertible { public var arg: Arg { .string(String(self)) } }
extension Int: ArgConvertible { public var arg: Arg { .int(Int64(self)) } }
extension Int8: ArgConvertible { public var arg: Arg { .int(Int64(self)) } }
extension Int16: ArgConvertible { public var arg: Arg { .int(Int64(self)) } }
extension Int32: ArgConvertible { public var arg: Arg { .int(Int64(self)) } }
extension Int64: ArgConvertible { public var arg: Arg { .int(self) } }
extension UInt8: ArgConvertible { public var arg: Arg { .int(Int64(self)) } }
extension UInt16: ArgConvertible { public var arg: Arg { .int(Int64(self)) } }
extension UInt32: ArgConvertible { public var arg: Arg { .int(Int64(self)) } }
extension UInt: ArgConvertible {
  public var arg: Arg { Int64(exactly: self).map(Arg.int) ?? .double(Double(self)) }
}
extension UInt64: ArgConvertible {
  public var arg: Arg { Int64(exactly: self).map(Arg.int) ?? .double(Double(self)) }
}
extension Double: ArgConvertible { public var arg: Arg { .double(self) } }
extension Float: ArgConvertible {
  /// Converts through the shortest decimal of the `Float`, so `0.1 as Float` is written `0.1`.
  public var arg: Arg { .double(Double(description) ?? Double(self)) }
}

/// The arguments of one option in an ``Options`` mapping: a bare value
/// (`80`), a list (`["fill", 300, 400]`), or `nil` to skip the option.
public struct Args: Sendable, Hashable, ExpressibleByArrayLiteral, ExpressibleByNilLiteral,
  ExpressibleByBooleanLiteral, ExpressibleByIntegerLiteral, ExpressibleByFloatLiteral,
  ExpressibleByStringLiteral
{
  /// The arguments, or `nil` when the option is skipped.
  public let values: [Arg]?

  /// The arguments of a list.
  public init(_ values: [Arg]) { self.values = values }
  /// The arguments of a variadic list; `nil` is an empty argument.
  public init(_ values: (any ArgConvertible)?...) { self.values = Arg.list(values) }
  public init(arrayLiteral elements: (any ArgConvertible)?...) { values = Arg.list(elements) }
  public init(nilLiteral: ()) { values = nil }
  public init(booleanLiteral value: Bool) { values = [.bool(value)] }
  public init(integerLiteral value: Int64) { values = [.int(value)] }
  public init(floatLiteral value: Double) { values = [.double(value)] }
  public init(stringLiteral value: String) { values = [.string(value)] }

  public static func == (a: Args, b: Args) -> Bool { a.values == b.values }
  public func hash(into hasher: inout Hasher) { hasher.combine(values) }
}

/// One option occurrence: a name (canonical or alias) and its arguments.
public struct OptionEntry: Sendable, Hashable {
  /// The name as given.
  public var name: String
  /// The arguments.
  public var arguments: [Arg]

  /// An option occurrence.
  public init(name: String, arguments: [Arg]) {
    self.name = name
    self.arguments = arguments
  }
}

/// The options of one pipeline, keyed by canonical name or alias, in URL order.
///
/// Setting a name that is already present replaces its arguments in place, as a
/// mapping does; a `nil` value skips the option.
///
/// ```swift
/// let opts: Options = ["rs": [ProcessingOption.ResizeType.fill, 300, 400], "q": 80, "bl": nil]
/// try imgproxy().url("https://example.com/a.jpg", options: opts)
/// // "/insecure/rs:fill:300:400/q:80/aHR0cHM6Ly9leGFtcGxlLmNvbS9hLmpwZw"
/// ```
public struct Options: Sendable, Hashable, ExpressibleByDictionaryLiteral, Sequence {
  /// The options in order.
  public private(set) var entries: [OptionEntry] = []

  /// No options.
  public init() {}

  public init(dictionaryLiteral elements: (String, Args)...) {
    for (name, args) in elements {
      if let values = args.values { set(name, arguments: values) }
    }
  }

  /// Sets an option (replacing the arguments of the same name in place).
  public mutating func set(_ name: String, arguments: [Arg]) {
    if let i = entries.firstIndex(where: { $0.name == name }) {
      entries[i].arguments = arguments
    } else {
      entries.append(OptionEntry(name: name, arguments: arguments))
    }
  }

  /// Sets an option (replacing the arguments of the same name in place).
  public mutating func set(_ name: String, _ args: (any ArgConvertible)?...) {
    set(name, arguments: Arg.list(args))
  }

  /// A copy with an option set.
  public func setting(_ name: String, _ args: (any ArgConvertible)?...) -> Options {
    var copy = self
    copy.set(name, arguments: Arg.list(args))
    return copy
  }

  /// Removes an option by the name it was set with.
  public mutating func remove(_ name: String) {
    entries.removeAll { $0.name == name }
  }

  /// Whether there are no options.
  public var isEmpty: Bool { entries.isEmpty }
  /// The number of options.
  public var count: Int { entries.count }

  public func makeIterator() -> IndexingIterator<[OptionEntry]> { entries.makeIterator() }
}

/// Query parameters in order (native `/q/` keys, imaginary parameters); keys may
/// repeat. A ``Arg/null`` value skips its key.
///
/// ```swift
/// let p: Params = ["w": 300, "f": "webp", "h": nil]
/// ```
public struct Params: Sendable, Hashable, ExpressibleByDictionaryLiteral, Sequence {
  /// One parameter.
  public struct Pair: Sendable, Hashable {
    /// The key.
    public var key: String
    /// The value.
    public var value: Arg
  }

  /// The parameters in order.
  public private(set) var pairs: [Pair] = []

  /// No parameters.
  public init() {}

  /// Parameters from key–value pairs.
  public init(_ pairs: [(String, Arg)]) {
    self.pairs = pairs.map { Pair(key: $0.0, value: $0.1) }
  }

  public init(dictionaryLiteral elements: (String, Arg)...) {
    self.init(elements)
  }

  /// Appends a parameter.
  public mutating func add(_ key: String, _ value: (any ArgConvertible)?) {
    pairs.append(Pair(key: key, value: Arg(value)))
  }

  /// A copy with a parameter appended.
  public func adding(_ key: String, _ value: (any ArgConvertible)?) -> Params {
    var copy = self
    copy.add(key, value)
    return copy
  }

  /// Whether there are no parameters.
  public var isEmpty: Bool { pairs.isEmpty }
  /// The number of parameters (skipped ones included).
  public var count: Int { pairs.count }

  public func makeIterator() -> IndexingIterator<[Pair]> { pairs.makeIterator() }
}
