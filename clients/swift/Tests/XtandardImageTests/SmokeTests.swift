// Smoke test against a running server, skipped unless XI_SMOKE_URL is set.
//
// The playground demo (compose.playground.yml, unsigned URLs allowed) works:
//
//   XI_SMOKE_URL=http://127.0.0.1:8080 task test
//
// It needs the demo's local fixtures (xi://local/…) and, for the /video
// cases, a full or gpl image with XI_VIDEO_TRANSFORM_ENABLED.

import Foundation
import Testing
import XtandardImage

#if canImport(FoundationNetworking)
  import FoundationNetworking
#endif

private let smokeURL = ProcessInfo.processInfo.environment["XI_SMOKE_URL"].flatMap {
  $0.isEmpty ? nil : $0
}

@Suite(.enabled(if: smokeURL != nil, "set XI_SMOKE_URL to run against a server"))
struct SmokeTests {
  let base = smokeURL ?? ""
  var image: String { try! xiSource("local", "jpeg/baseline.jpg") }
  var video: String { try! xiSource("local", "video/bbb-h264-aac.mp4") }

  func client(_ encoding: SourceEncoding = .base64) -> XtandardClient {
    XtandardClient(
      baseURL: base, sourceEncoding: encoding, session: URLSession(configuration: .ephemeral))
  }

  @Test func healthImageInfoAndErrors() async throws {
    let xi = client(.plain)
    try await xi.healthz()
    #expect(try await xi.readyz().ready)
    let res = try await xi.image(image, options: ["rs": ["fit", 64, 64]], extension: "webp")
    #expect(res.contentType == "image/webp")
    #expect(!res.body.isEmpty)
    let info = try await xi.info(
      image, options: ["size": true, "format": true, "dimensions": true])
    #expect(info.format == "jpeg")
    #expect((info.size ?? 0) > 0)
    let missing = try xiSource("local", "jpeg/missing.jpg")
    do {
      _ = try await xi.image(missing)
      Issue.record("no error")
    } catch {
      #expect(error.httpError?.category == .notFound)
    }
  }

  @Test func queryDialect() async throws {
    let url = try queryURLs(baseURL: base).url(image, params: ["w": 32, "f": "png"])
    #expect(try await client().media(url).contentType == "image/png")
  }

  @Test func videoRangeAndStream() async throws {
    let xi = client()
    let clip: Options = ["vd": 1, "w": 160]
    let res = try await xi.video(
      video, options: clip, extension: "webm", media: MediaOptions(range: .bytes(0, 99)))
    #expect([200, 206].contains(res.status))
    let head = try await xi.video(
      video, options: clip, extension: "webm", media: MediaOptions(method: .head))
    #expect(head.contentType?.hasPrefix("video/webm") == true)
    let m = try await xi.stream(video, protocol: .hls, renditions: [240])
    #expect(m.body.hasPrefix("#EXTM3U"))
    let child = try #require(
      m.body.split(separator: "\n").first { !$0.isEmpty && !$0.hasPrefix("#") })
    #expect(try await xi.media(String(child)).status == 200)
  }
}
