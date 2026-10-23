// A stand-in HTTP/1.1 server on 127.0.0.1 for the client tests: POSIX sockets,
// one thread, `Connection: close`, so the same code runs on macOS and Linux.

import Foundation
import XtandardImage

#if canImport(Glibc)
  import Glibc
  private let sockStream = Int32(SOCK_STREAM.rawValue)
#elseif canImport(Musl)
  import Musl
  private let sockStream = SOCK_STREAM
#else
  import Darwin
  private let sockStream = SOCK_STREAM
#endif

/// A request the stand-in server received.
struct Seen: Sendable {
  var method: String
  /// Path and query, as sent.
  var target: String
  /// Lower-case header names.
  var headers: [String: String]
  /// The request content (`Content-Length` bytes), as UTF-8.
  var body = ""
}

/// A response of the stand-in server.
struct Reply: Sendable {
  var status: Int
  var headers: [(String, String)]
  var body: [UInt8]

  init(_ status: Int, _ contentType: String, _ body: [UInt8]) {
    self.status = status
    headers = [("Content-Type", contentType)]
    self.body = body
  }

  init(_ status: Int, _ contentType: String, _ text: String) {
    self.init(status, contentType, Array(text.utf8))
  }

  static func json(_ status: Int, _ text: String) -> Reply {
    Reply(status, "application/json", text)
  }

  func header(_ k: String, _ v: String) -> Reply {
    var r = self
    r.headers.append((k, v))
    return r
  }
}

final class StandInServer: @unchecked Sendable {
  let base: String
  private let fd: Int32
  private let lock = NSLock()
  private var seen: [Seen] = []
  private let handler: @Sendable (Seen) -> Reply

  init(_ handler: @escaping @Sendable (Seen) -> Reply) {
    self.handler = handler
    let sock = socket(AF_INET, sockStream, 0)
    precondition(sock >= 0, "socket")
    var one: Int32 = 1
    setsockopt(sock, SOL_SOCKET, SO_REUSEADDR, &one, socklen_t(MemoryLayout<Int32>.size))
    var addr = sockaddr_in()
    addr.sin_family = sa_family_t(AF_INET)
    addr.sin_port = 0
    addr.sin_addr.s_addr = inet_addr("127.0.0.1")
    var len = socklen_t(MemoryLayout<sockaddr_in>.size)
    let bound = withUnsafePointer(to: &addr) {
      $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(sock, $0, len) }
    }
    precondition(bound == 0, "bind")
    precondition(listen(sock, 16) == 0, "listen")
    _ = withUnsafeMutablePointer(to: &addr) {
      $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { getsockname(sock, $0, &len) }
    }
    fd = sock
    base = "http://127.0.0.1:\(UInt16(bigEndian: addr.sin_port))"
    let thread = Thread { [self] in self.acceptLoop() }
    thread.start()
  }

  /// Stops listening (the accept thread keeps the server alive until then).
  func stop() {
    shutdown(fd, Int32(SHUT_RDWR))
    close(fd)
  }

  var requests: [Seen] { lock.withLock { seen } }
  var last: Seen { requests.last! }

  private func acceptLoop() {
    while true {
      let c = accept(fd, nil, nil)
      if c < 0 { return }
      serve(c)
      close(c)
    }
  }

  private func serve(_ c: Int32) {
    var buf: [UInt8] = []
    var chunk = [UInt8](repeating: 0, count: 4096)
    let end = Array("\r\n\r\n".utf8)
    while true {
      let n = recv(c, &chunk, chunk.count, 0)
      if n <= 0 { return }
      buf += chunk[0..<n]
      if buf.count >= 4, (0...(buf.count - 4)).contains(where: { Array(buf[$0..<$0 + 4]) == end }) {
        break
      }
    }
    let text = String(decoding: buf, as: UTF8.self)
    let lines = text.components(separatedBy: "\r\n")
    let parts = lines[0].split(separator: " ")
    guard parts.count >= 2 else { return }
    var headers: [String: String] = [:]
    for line in lines.dropFirst() {
      if line.isEmpty { break }
      guard let colon = line.firstIndex(of: ":") else { continue }
      headers[line[..<colon].lowercased()] = line[line.index(after: colon)...]
        .trimmingCharacters(in: .whitespaces)
    }
    var req = Seen(method: String(parts[0]), target: String(parts[1]), headers: headers)
    if let length = headers["content-length"].flatMap({ Int($0) }), length > 0,
      let start = (0...(buf.count - 4)).first(where: { Array(buf[$0..<$0 + 4]) == end })
    {
      var content = Array(buf[(start + 4)...])
      while content.count < length {
        let n = recv(c, &chunk, chunk.count, 0)
        if n <= 0 { break }
        content += chunk[0..<n]
      }
      req.body = String(decoding: content.prefix(length), as: UTF8.self)
    }
    lock.withLock { seen.append(req) }
    let reply = handler(req)
    var head =
      "HTTP/1.1 \(reply.status) X\r\nConnection: close\r\nContent-Length: \(reply.body.count)\r\n"
    for (k, v) in reply.headers { head += "\(k): \(v)\r\n" }
    head += "\r\n"
    var out = Array(head.utf8)
    if req.method != "HEAD" { out += reply.body }
    var sent = 0
    while sent < out.count {
      let n = out[sent...].withUnsafeBytes { send(c, $0.baseAddress, $0.count, 0) }
      if n <= 0 { return }
      sent += n
    }
  }
}

/// The stand-in xtandard-image server of the client tests (as the Python client's `MockTransport`).
enum StandIn {
  static let key = try! SigningKey(key: "736563726574", salt: "68656C6C6F")

  /// 1000 bytes: 0, 1, …, 255, 0, 1, …
  static let body: [UInt8] = (0..<1000).map { UInt8($0 % 256) }

  /// The path of the one signed `/video` URL the stand-in accepts.
  static let videoPath = try! imgproxy(signing: key).videoURL(
    "s3://m/clip.mov", options: ["vd": 2], extension: "webm")

  static func range(_ req: Seen, _ length: Int) -> (Int, Int)? {
    guard let spec = req.headers["range"], spec.hasPrefix("bytes=") else { return nil }
    let ab = spec.dropFirst(6).split(separator: "-", omittingEmptySubsequences: false)
    guard ab.count == 2, let start = Int(ab[0]) else { return nil }
    let end = ab[1].isEmpty ? length - 1 : Int(ab[1])!
    return (start, end)
  }

  static func start() -> StandInServer {
    StandInServer { req in
      let path = String(req.target.split(separator: "?", maxSplits: 1)[0])
      switch path {
      case "/healthz": return Reply(200, "text/plain", "ok\n")
      case "/readyz": return Reply(503, "text/plain", "draining\n")
      case "/_admin/play/api/meta": return .json(200, #"{"version": "test"}"#)
      case "/proxy-error": return Reply(502, "text/html", "<html>bad gateway</html>")
      case "/busy":
        return .json(
          429,
          #"{"error": {"kind": "overloaded", "message": "too many requests", "request_id": "r1"}}"#)
      case "/not-json": return .json(200, "{")
      default: break
      }
      if path.hasPrefix("/info/") {
        return .json(
          200, #"{"width": 640, "height": 480, "format": "jpeg", "exif": {"Make": "X"}}"#)
      }
      if path.hasPrefix("/video/") && path.hasSuffix(".m3u8") {
        return Reply(200, "application/vnd.apple.mpegurl", "#EXTM3U\n/_stream/T/v0.m3u8\n")
      }
      if path.hasPrefix("/video/") || path.hasPrefix("/_stream/") {
        if path.hasPrefix("/video/") && req.target != videoPath {
          return .json(403, #"{"error": {"kind": "unauthorized", "message": "invalid signature"}}"#)
        }
        if let (start, end) = range(req, body.count) {
          return Reply(206, "video/webm", Array(body[start...end]))
            .header("Content-Range", "bytes \(start)-\(end)/\(body.count)")
        }
        return Reply(200, "video/webm", body)
      }
      return .json(404, #"{"error": {"kind": "not_found", "message": "source image not found"}}"#)
    }
  }

  /// A base URL nothing listens on.
  static func closedBase() -> String {
    let s = StandInServer { _ in Reply(200, "text/plain", "") }
    s.stop()
    return s.base
  }
}
