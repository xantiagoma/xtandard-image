// The HTTP client against the stand-in server (StandIn.swift).

import Foundation
import Testing
import XtandardImage

#if canImport(FoundationNetworking)
  import FoundationNetworking
#endif

private func client(_ base: String) -> XtandardClient {
  XtandardClient(
    baseURL: base, signing: StandIn.key, headers: ["X-Test": "1"],
    session: URLSession(configuration: .ephemeral), playgroundPath: "/_admin/play/")
}

@Suite struct ClientTests {
  @Test func health() async throws {
    let srv = StandIn.start()
    defer { srv.stop() }
    let xi = client(srv.base)
    try await xi.healthz()
    #expect(srv.last.headers["accept"] == "text/plain")
    #expect(srv.last.headers["x-test"] == "1")
    let r = try await xi.readyz()
    #expect(!r.ready)
    #expect(r.state == .draining)
  }

  @Test func info() async throws {
    let srv = StandIn.start()
    defer { srv.stop() }
    let xi = client(srv.base)
    let info = try await xi.info("s3://b/cat.jpg", options: ["size": true, "format": true])
    #expect(info.width == 640)
    #expect(info.exif?["Make"] == "X")
    let seen = srv.last
    #expect(seen.headers["accept"] == "application/json")
    #expect(seen.target.hasPrefix("/info/"))
    #expect(
      try xi.infoURL("s3://b/cat.jpg", options: ["size": true, "format": true]).hasSuffix(
        seen.target))
    #expect(try await xi.info(at: seen.target).format == "jpeg")
  }

  @Test func videoWholeRangeAndHead() async throws {
    let srv = StandIn.start()
    defer { srv.stop() }
    let xi = client(srv.base)
    let whole = try await xi.video("s3://m/clip.mov", options: ["vd": 2], extension: "webm")
    #expect(Array(whole.body) == StandIn.body)
    #expect(whole.status == 200)

    let part = try await xi.video(
      "s3://m/clip.mov", options: ["vd": 2], extension: "webm",
      media: MediaOptions(range: .bytes(10, 19)))
    #expect(part.status == 206)
    #expect(part.header("Content-Range") == "bytes 10-19/1000")
    #expect(Array(part.body) == Array(StandIn.body[10...19]))
    #expect(srv.last.headers["range"] == "bytes=10-19")

    let head = try await xi.video(
      "s3://m/clip.mov", options: ["vd": 2], extension: "webm", media: MediaOptions(method: .head))
    #expect(srv.last.method == "HEAD")
    #expect(head.contentType == "video/webm")
    #expect(head.body.isEmpty)
  }

  #if !canImport(FoundationNetworking)
    @Test func streamedBody() async throws {
      let srv = StandIn.start()
      defer { srv.stop() }
      let xi = client(srv.base)
      let (bytes, head) = try await xi.bytes(StandIn.videoPath)
      #expect(head.status == 200)
      var got: [UInt8] = []
      for try await b in bytes { got.append(b) }
      #expect(got == StandIn.body)
      do {
        _ = try await xi.bytes("/busy")
        Issue.record("no error")
      } catch {
        #expect(error.httpError?.kind == "overloaded")
      }
    }
  #endif

  @Test func stream() async throws {
    let srv = StandIn.start()
    defer { srv.stop() }
    let xi = client(srv.base)
    let m = try await xi.stream("s3://m/clip.mov", protocol: .hls, renditions: [360])
    #expect(m.body.hasPrefix("#EXTM3U"))
    #expect(m.contentType == "application/vnd.apple.mpegurl")
    #expect(srv.last.headers["accept"] == "application/vnd.apple.mpegurl, */*;q=0.5")
    let child = m.body.split(separator: "\n").first { !$0.hasPrefix("#") }!
    let part = try await xi.media(String(child))
    #expect(part.status == 200)
  }

  @Test func playgroundMeta() async throws {
    let srv = StandIn.start()
    defer { srv.stop() }
    let meta = try await client(srv.base).playgroundMeta()
    #expect(meta["version"] == .string("test"))
  }

  @Test func httpErrors() async throws {
    let srv = StandIn.start()
    defer { srv.stop() }
    let xi = client(srv.base)
    do {
      _ = try await xi.media("/busy")
      Issue.record("no error")
    } catch {
      let e = try #require(error.httpError)
      #expect(e.status == 429)
      #expect(e.kind == "overloaded")
      #expect(e.errorKind == .overloaded)
      #expect(e.requestID == "r1")
      #expect(e.category == .tooManyRequests)
    }
    do {
      _ = try await xi.media("/proxy-error")
      Issue.record("no error")
    } catch {
      let e = try #require(error.httpError)
      #expect(e.kind == "unknown")
      #expect(e.message == "<html>bad gateway</html>")
      #expect(e.category == .gateway)
      #expect(e.isServerError)
    }
    do {
      _ = try await xi.image("s3://b/missing.jpg")
      Issue.record("no error")
    } catch {
      #expect(error.httpError?.category == .notFound)
    }
    // A /video URL with another key is refused by the stand-in.
    let other = XtandardClient(baseURL: srv.base, session: URLSession(configuration: .ephemeral))
    do {
      _ = try await other.video("s3://m/clip.mov", options: ["vd": 2], extension: "webm")
      Issue.record("no error")
    } catch {
      #expect(error.httpError?.kind == "unauthorized")
    }
  }

  @Test func decodeBuildAndTransportErrors() async throws {
    let srv = StandIn.start()
    defer { srv.stop() }
    let xi = client(srv.base)
    do {
      _ = try await xi.info(at: "/not-json")
      Issue.record("no error")
    } catch {
      guard case .decode = error else {
        Issue.record("want decode, got \(error)")
        return
      }
    }
    do {
      _ = try await xi.image("s3://b/a.jpg", options: ["nope": 1])
      Issue.record("no error")
    } catch {
      guard case .build(let e) = error else {
        Issue.record("want build, got \(error)")
        return
      }
      #expect(e.code == .unknownOption)
    }
    let closed = XtandardClient(
      baseURL: StandIn.closedBase(), timeout: 5, session: URLSession(configuration: .ephemeral))
    do {
      try await closed.healthz()
      Issue.record("no error")
    } catch {
      guard case .transport(let t) = error else {
        Issue.record("want transport, got \(error)")
        return
      }
      #expect(t.method == "GET")
    }
  }
}

@Suite struct QueryMethodTests {
  private static let q = "/q/?url=https://e.com/a.jpg&w=300&xi_sig=abc"
  private static let form = QueryMethodRequest.formContentType

  private func sent(_ srv: StandInServer) -> [String] {
    srv.requests.map { "\($0.method) \($0.target) \($0.headers["content-type"] ?? "-") \($0.body)" }
  }

  @Test func offByDefault() async throws {
    let srv = StandInServer { _ in Reply(200, "image/webp", "img") }
    defer { srv.stop() }
    let xi = XtandardClient(baseURL: srv.base, session: URLSession(configuration: .ephemeral))
    _ = try await xi.media(Self.q)
    #expect(sent(srv) == ["GET \(Self.q) - "])
  }

  @Test func queryMethodSendsURLsUnderTheMountAsQuery() async throws {
    let srv = StandInServer { _ in Reply(200, "image/webp", "img") }
    defer { srv.stop() }
    let xi = XtandardClient(
      baseURL: srv.base, session: URLSession(configuration: .ephemeral), queryMethod: true)
    _ = try await xi.media(Self.q)
    _ = try await xi.media("/q/video?url=s3://m/a.mov&vd=5")
    _ = try await xi.media("/insecure/rs:fit:300:300/plain/https://e.com/a.jpg")
    _ = try await xi.media(Self.q, MediaOptions(method: .head))
    _ = try await xi.media(Self.q, MediaOptions(method: .get))
    #expect(
      sent(srv) == [
        "QUERY /q/ \(Self.form) url=https://e.com/a.jpg&w=300&xi_sig=abc",
        "QUERY /q/video \(Self.form) url=s3://m/a.mov&vd=5",
        "GET /insecure/rs:fit:300:300/plain/https://e.com/a.jpg - ",
        "HEAD \(Self.q) - ",
        "GET \(Self.q) - ",
      ])
  }

  @Test func methodQueryOnOneCallWithACustomMount() async throws {
    let srv = StandInServer { _ in Reply(200, "image/webp", "img") }
    defer { srv.stop() }
    let xi = XtandardClient(
      baseURL: srv.base, session: URLSession(configuration: .ephemeral), queryMount: "/img/q/")
    _ = try await xi.media("/img/q/resize?url=x&width=1", MediaOptions(method: .query))
    #expect(sent(srv) == ["QUERY /img/q/resize \(Self.form) url=x&width=1"])
    do {
      _ = try await xi.media("/img/q/", MediaOptions(method: .query))
      Issue.record("no error")
    } catch {
      guard case .build(let e) = error else {
        Issue.record("want build, got \(error)")
        return
      }
      #expect(e.code == .invalidRequest)
    }
  }

  @Test func queryMethodRequestSplitsTheURL() throws {
    let q = try queryMethodRequest("https://h/q/?url=x&w=1")
    #expect(q.method == "QUERY")
    #expect(q.url == "https://h/q/")
    #expect(q.contentType == Self.form)
    #expect(q.body == "url=x&w=1")
    for bad in ["/q/", "/q/?", "/q/?url=x#f"] {
      do {
        _ = try queryMethodRequest(bad)
        Issue.record("no error for \(bad)")
      } catch {
        #expect(error.code == .invalidRequest)
      }
    }
  }
}
