import Foundation
import XtandardImage

/// The repository root: this file is clients/swift/Tests/XtandardImageTests/Support.swift.
let repositoryRoot: URL = {
  var url = URL(fileURLWithPath: #filePath)
  for _ in 0..<5 { url.deleteLastPathComponent() }
  return url
}()

/// A JSON file of the repository, read by relative path.
func loadJSON(_ relativePath: String) throws -> JSONValue {
  let data = try Data(contentsOf: repositoryRoot.appendingPathComponent(relativePath))
  return try JSONDecoder().decode(JSONValue.self, from: data)
}

extension JSONValue {
  var string: String { stringValue! }
  var array: [JSONValue] {
    if case .array(let a) = self { return a }
    return []
  }
  var object: [String: JSONValue] {
    if case .object(let o) = self { return o }
    return [:]
  }
  var int: Int { Int(numberValue!) }
  var isTrue: Bool { self == .bool(true) }
}

/// A fixture argument as an ``Arg``: whole numbers are integers, other numbers doubles.
func fixtureArg(_ v: JSONValue) -> Arg {
  switch v {
  case .null: return .null
  case .bool(let b): return .bool(b)
  case .number(let n):
    return n == n.rounded() && abs(n) < 9e15 ? .int(Int64(n)) : .double(n)
  case .string(let s): return .string(s)
  default: fatalError("unsupported argument \(v)")
  }
}

/// The outcome of building something.
typealias Outcome = Result<String, URLBuildError>

func attempt(_ make: () throws(URLBuildError) -> String) -> Outcome {
  do { return .success(try make()) } catch { return .failure(error) }
}

/// Lower-case hex of bytes.
func hex(_ bytes: [UInt8]) -> String {
  bytes.map { String(format: "%02x", $0) }.joined()
}

/// Bytes of a hex string.
func unhex(_ s: String) -> [UInt8] {
  var out: [UInt8] = []
  var i = s.startIndex
  while i < s.endIndex {
    let j = s.index(i, offsetBy: 2)
    out.append(UInt8(s[i..<j], radix: 16)!)
    i = j
  }
  return out
}

/// Bytes of unpadded base64url.
func unbase64URL(_ s: String) -> [UInt8] {
  var b = s.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
  while b.count % 4 != 0 { b += "=" }
  return Array(Data(base64Encoded: b)!)
}
