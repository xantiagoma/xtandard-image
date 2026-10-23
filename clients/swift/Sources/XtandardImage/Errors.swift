/// Why a URL, token, key or header could not be built.
///
/// ``code`` is the language-neutral error code of `clients/spec/conformance.json`,
/// shared by every official client; ``message`` is for humans.
///
/// ```swift
/// do {
///     _ = try imgproxy().image("s3://b/a.jpg").option("nope", 1).build()
/// } catch {
///     print(error.code)  // .unknownOption
/// }
/// ```
public struct URLBuildError: Error, Sendable, Equatable, CustomStringConvertible {
  /// One code per class of fault.
  public enum Code: String, Sendable, CaseIterable {
    /// An option name that is not in the catalogue.
    case unknownOption = "unknown_option"
    /// No arguments, a forbidden character, a non-finite number, or `:` in a `/q/video` argument.
    case invalidArgument = "invalid_argument"
    /// An empty source, or a source with control characters.
    case invalidSource = "invalid_source"
    /// An extension that is not ASCII letters and digits.
    case invalidExtension = "invalid_extension"
    /// A separator that is not one character, or is forbidden.
    case invalidSeparator = "invalid_separator"
    /// A PDF password without a signing key.
    case missingSigning = "missing_signing"
    /// An encrypted source or a PDF password without an encryption key.
    case missingEncryption = "missing_encryption"
    /// Key material that is not hex, a key of the wrong size, an IV that is not
    /// 16 bytes, a signature size outside 1–32.
    case invalidKey = "invalid_key"
    /// An empty password, a password with control characters, or one over 1024 UTF-8 bytes.
    case invalidPassword = "invalid_password"
    /// Chained `/info` or `/video` pipelines, a PDF password on `/video`, a stream
    /// option given as an option, an unknown protocol, `ts` on DASH, a URL outside
    /// the mount, two `xi_sig` parameters.
    case invalidRequest = "invalid_request"
    /// A bad `xi://` source name or source authority.
    case invalidName = "invalid_name"
    /// An imaginary endpoint that is not lower-case letters, or is `video`.
    case invalidEndpoint = "invalid_endpoint"
    /// A negative range value, or a range whose end is before its start.
    case invalidRange = "invalid_range"
  }

  /// The language-neutral error code.
  public let code: Code
  /// What is wrong.
  public let message: String

  /// An error with a code and a message.
  public init(_ code: Code, _ message: String) {
    self.code = code
    self.message = message
  }

  public var description: String { "\(code.rawValue): \(message)" }
}

/// A String-backed enum of the spec that parses its values and aliases.
///
/// Every generated value enum (``ImageFormat``, ``StreamProtocol``,
/// ``ProcessingOption/ResizeType``, …) conforms.
public protocol ParsableEnum: RawRepresentable, CaseIterable, Sendable where RawValue == String {
  /// The error code of a value this enum does not know.
  static var parseError: URLBuildError.Code { get }
  /// Other spellings ``init(parsing:)`` accepts.
  static var aliases: [String: Self] { get }
}

extension ParsableEnum {
  /// The case of a value or an alias.
  ///
  /// - Throws: ``URLBuildError`` with ``parseError`` for an unknown value.
  public init(parsing value: String) throws(URLBuildError) {
    guard let parsed = Self(rawValue: value) ?? Self.aliases[value] else {
      throw URLBuildError(Self.parseError, "\"\(value)\" is not a \(Self.self)")
    }
    self = parsed
  }
}
