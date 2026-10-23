/// How option names are written.
public enum OptionNames: String, Sendable, CaseIterable, ParsableEnum {
  /// The shortest alias (`rs`, `q`): the default.
  case short
  /// The canonical name (`resize`, `quality`).
  case canonical
  /// The name as the caller wrote it (a name or an alias).
  case given

  public static let parseError: URLBuildError.Code = .invalidRequest
  public static let aliases: [String: OptionNames] = [:]
}

/// One option catalogue: every name and alias to its canonical name, and canonical to short names.
struct Catalogue: Sendable {
  let kind: String
  let names: [String: String]
  let shortNames: [String: String]

  /// The canonical name of a name or alias.
  func canonical(_ name: String) -> String? { names[name] }

  /// The name an option is written with.
  func writtenName(_ name: String, _ style: OptionNames) throws(URLBuildError) -> String {
    guard let canonical = names[name] else {
      throw URLBuildError(.unknownOption, "unknown \(kind) option \"\(name)\"")
    }
    switch style {
    case .given: return name
    case .canonical: return canonical
    case .short: return shortNames[canonical] ?? canonical
    }
  }

  /// Formats one option segment (`rs:fill:300:400`).
  func format(_ entry: OptionEntry, separator: Character, style: OptionNames) throws(URLBuildError)
    -> String
  {
    var out = try writtenName(entry.name, style)
    if entry.arguments.isEmpty {
      throw URLBuildError(
        .invalidArgument, "option \(entry.name): at least one argument is required")
    }
    for (i, a) in entry.arguments.enumerated() {
      out.append(separator)
      out += try formatArgument(a, separator: separator, "option \(entry.name) argument \(i + 1)")
    }
    return out
  }
}

/// Whether a string argument contains a character that cannot appear in an option.
private func isBadArgument(_ s: String, separator: Character) -> Bool {
  if s.contains(separator) { return true }
  return s.unicodeScalars.contains { u in
    switch u {
    case "/", "?", "#", "\u{feff}": true
    default: u.value < 0x20 || u.value == 0x7f || u.properties.isWhitespace
    }
  }
}

/// Formats one argument. Strings are checked, never escaped: the server does
/// not unescape option arguments.
func formatArgument(_ a: Arg, separator: Character, _ location: String) throws(URLBuildError)
  -> String
{
  switch a {
  case .null: return ""
  case .bool(let b): return b ? "1" : "0"
  case .int(let i): return String(i)
  case .double(let d): return try formatNumber(d, location)
  case .string(let s):
    if isBadArgument(s, separator: separator) {
      throw URLBuildError(
        .invalidArgument,
        "\(location): argument \"\(s)\" contains a character that cannot appear in an option")
    }
    return s
  }
}

/// The one character of a separator, rejecting what the grammar forbids.
func checkSeparator(_ separator: String, forbidden: [String]) throws(URLBuildError) -> Character {
  guard separator.count == 1, let c = separator.first, !forbidden.contains(separator) else {
    throw URLBuildError(.invalidSeparator, "invalid argument separator \"\(separator)\"")
  }
  return c
}
