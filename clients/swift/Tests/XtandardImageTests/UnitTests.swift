import Foundation
import Testing

@testable import XtandardImage

@Suite struct NumberTests {
  @Test(arguments: [
    (0.0, "0"), (-0.0, "0"), (300.0, "300"), (1.5, "1.5"), (-1.5, "-1.5"), (0.1, "0.1"),
    (0.000_001, "0.000001"), (1e-7, "1e-7"), (1.5e-9, "1.5e-9"), (123_456_789.125, "123456789.125"),
    (1e20, "100000000000000000000"), (1e21, "1e+21"), (1.5e22, "1.5e+22"),
    (0.300_000_000_000_000_04, "0.30000000000000004"), (-90.0, "-90"), (1e16, "10000000000000000"),
    (5e-324, "5e-324"), (1.7976931348623157e308, "1.7976931348623157e+308"),
  ])
  func numbersMatchECMAScript(_ value: Double, _ want: String) throws {
    #expect(try formatNumber(value, "x") == want)
  }

  @Test(arguments: [Double.infinity, -.infinity, .nan])
  func nonFiniteNumbersAreErrors(_ value: Double) {
    #expect(throws: URLBuildError.self) { try formatNumber(value, "x") }
  }

  @Test func floatsUseTheirShortestDecimal() {
    #expect((0.1 as Float).arg == .double(0.1))
    #expect(UInt64.max.arg == .double(Double(UInt64.max)))
  }

  @Test func hex() {
    #expect(decodeHex("0aFf") == [0x0a, 0xff])
    #expect(decodeHex(" 00 ") == [0])
    #expect(decodeHex("0") == nil)
    #expect(decodeHex("zz") == nil)
  }
}

@Suite struct APITests {
  let key = try! SigningKey(
    key: "943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881",
    salt: "520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5")

  @Test func readmeExample() throws {
    let img = imgproxy(baseURL: "https://img.example.com", signing: key)
    let url = try img.url(
      "https://example.com/images/cat.jpg",
      options: ["rs": [ProcessingOption.ResizeType.fit, 300, 300]],
      extension: "png")
    #expect(
      url
        == "https://img.example.com/3kUTYFkXjSOKZ0vB6jAnVMUscg3Qa5k63uXjN9ru1MQ/rs:fit:300:300/aHR0cHM6Ly9leGFtcGxlLmNvbS9pbWFnZXMvY2F0LmpwZw.png"
    )
    let fluent = try img.image("https://example.com/images/cat.jpg")
      .resize(ProcessingOption.ResizeType.fit, 300, 300).extension("png").build()
    #expect(fluent == url)
    let chained = try img.image("s3://photos/cat.jpg")
      .rs(ProcessingOption.ResizeType.fill, 600, 400)
      .g(ProcessingOption.GravityType.fp, 0.5, 0.25)
      .pipeline()
      .bl(2)
      .build()
    #expect(chained.contains("/rs:fill:600:400/g:fp:0.5:0.25/-/bl:2/"))
  }

  @Test func mappingSemantics() throws {
    var opts: Options = ["rs": ["fill", 300, 400], "q": 80, "bl": nil, "sh": [nil]]
    #expect(opts.count == 3)
    opts.set("rs", "fit", 1, 2)
    #expect(opts.entries.map(\.name) == ["rs", "q", "sh"])
    let url = try imgproxy().url("https://example.com/a.jpg", options: opts)
    #expect(url == "/insecure/rs:fit:1:2/q:80/sh:/aHR0cHM6Ly9leGFtcGxlLmNvbS9hLmpwZw")
    #expect(opts.setting("q", 90).entries[1].arguments == [.int(90)])
  }

  @Test func nullsBooleansAndNames() throws {
    let url = try imgproxy(sourceEncoding: .plain, optionNames: .given)
      .image("https://example.com/a.jpg").resize("fit", 1, 2).q(90).minWidth(10).sm(true).rs(
        "fill", 3, nil, true
      )
      .build()
    #expect(
      url
        == "/insecure/resize:fit:1:2/q:90/min-width:10/sm:1/rs:fill:3::1/plain/https://example.com/a.jpg"
    )
  }

  @Test func optionNameEnums() {
    #expect(ProcessingOption.resize.short == "rs")
    #expect(ProcessingOption(name: "min-width") == .minWidth)
    #expect(ProcessingOption(name: "icc") == .colorProfile)
    #expect(ProcessingOption(name: "nope") == nil)
    #expect(VideoOption.crop.aliases.contains("c") || VideoOption.crop.short == "c")
    #expect(InfoOption.allCases.count == InfoOption.shortNames.count)
  }

  @Test func valueEnumsParseAliases() throws {
    #expect(try SegmentFormat(parsing: "fmp4") == .fmp4)
    #expect(SegmentFormat.serverDefault == .fmp4)
    #expect(StreamProtocol.hls.manifestContainer == "m3u8")
    #expect(StreamProtocol.dash.contentType == "application/dash+xml")
    #expect(throws: URLBuildError.self) { try StreamProtocol(parsing: "smooth") }
    do {
      _ = try ImageFormat(parsing: "nope")
    } catch {
      #expect(error.code == .invalidExtension)
    }
    #expect(ErrorKind.notFound.status == 404)
  }

  @Test func errorsCarryTheCode() {
    do {
      _ = try imgproxy().image("s3://b/a.jpg").option("nope", 1).build()
      Issue.record("no error")
    } catch {
      #expect(error.code == .unknownOption)
      #expect(error.code.rawValue == "unknown_option")
    }
  }

  @Test func queryAndImaginary() throws {
    let q = queryURLs(baseURL: "https://img.example.com")
    let url = try q.url("s3://bucket/cat.jpg", params: ["w": 300, "f": "webp"], options: ["bl": 2])
    #expect(url == "https://img.example.com/q/?url=s3://bucket/cat.jpg&w=300&f=webp&o=bl:2")
    #expect(
      try q.image("s3://bucket/cat.jpg").param("w", 300).param("f", "webp").bl(2).build() == url)
    #expect(
      try queryURLs().imaginaryURL(.resize, "https://e.com/a.jpg", params: ["width": 300])
        == "/q/resize?url=https://e.com/a.jpg&width=300")
    #expect(
      try queryURLs().video("s3://m/a.mov").crop(10, 10).flip(true, false).extension("mp4").build()
        == "/q/video?url=s3://m/a.mov&c=10,10&fl=1,0&format=mp4")
  }

  @Test func streams() throws {
    let s = try imgproxy(signing: key).streamURL(
      "s3://bucket/talk.mov", protocol: .hls, renditions: [360, 720])
    #expect(s.hasSuffix(".m3u8"))
    #expect(s.contains("/st:hls:360:720/"))
    #expect(throws: URLBuildError.self) {
      try imgproxy().streamURL("s3://b/t.mov", protocol: .dash, segmentFormat: .ts)
    }
  }

  @Test func sourcesAndRanges() throws {
    #expect(try xiSource("photos", "2026/cat.jpg") == "xi://photos/2026/cat.jpg")
    #expect(try sourceURL(.s3, "photos", "/cat.jpg") == "s3://photos/cat.jpg")
    #expect(try rangeHeader(ByteRange(0..<100)) == "bytes=0-99")
    #expect(try rangeHeader(ByteRange(5...9)) == "bytes=5-9")
    #expect(try rangeHeader(ByteRange(100...)) == "bytes=100-")
    #expect(try rangeHeader("bytes=1-2") == "bytes=1-2")
    #expect(throws: URLBuildError.self) { try rangeHeader(.suffix(-1)) }
  }

  @Test func keysValidate() {
    #expect(throws: URLBuildError.self) { try SigningKey(key: "00", salt: "00", size: 0) }
    #expect(throws: URLBuildError.self) { try EncryptionKey(key: "0011") }
    #expect(throws: URLBuildError.self) {
      try EncryptionKey(keyBytes: [UInt8](repeating: 0, count: 32), ivBytes: [1])
    }
    #expect(throws: Never.self) { try EncryptionKey(keyBytes: [UInt8](repeating: 0, count: 24)) }
  }

  @Test func responsesDecode() throws {
    let json = #"""
      {"width": 10, "height": 20, "format": "png", "average": {"R": 1, "G": 2, "B": 3, "A": 255},
       "exif": {"Make": "X"}, "iptc": {"k": [1, "a", null]}, "objects": null,
       "video_streams": [{"type": "video", "codec": "h264", "fps": 29.97}]}
      """#
    let info = try JSONDecoder().decode(InfoResponse.self, from: Data(json.utf8))
    #expect(info.width == 10)
    #expect(info.average?.a == 255)
    #expect(info.exif?["Make"] == "X")
    #expect(info.iptc?["k"] == .array([.number(1), .string("a"), .null]))
    #expect(info.objects == nil)
    #expect(info.videoStreams?.first?.fps == 29.97)
    let err = try JSONDecoder().decode(
      ErrorResponse.self,
      from: Data(#"{"error":{"kind":"not_found","message":"m","request_id":"r"}}"#.utf8))
    #expect(err.error.requestID == "r")
  }
}
