// clients/spec/conformance.json: the cases every official client runs (ADR 0013).
//
// Positive cases must produce expect.value byte for byte; error cases must
// fail with expect.error as the error code. The fixture's options are
// pipelines of [name, args] pairs; they are built twice here, as Options
// (the mapping form) and through the fluent chains, and both must agree.
// Cases the typed API cannot express are skipped with the reason.

import Foundation
import Testing
import XtandardImage

private struct Fixture {
  let keys: JSONValue

  func signing(_ cfg: JSONValue) throws(URLBuildError) -> SigningKey? {
    guard let sg = cfg["signing"] else { return nil }
    let k = sg.isTrue ? keys["signing"]! : sg
    let size = cfg["signature_size"]?.int ?? SpecConstants.defaultSignatureSize
    return try SigningKey(key: k["key"]!.string, salt: k["salt"]!.string, size: size)
  }

  func encryption(_ cfg: JSONValue) throws(URLBuildError) -> EncryptionKey? {
    guard let en = cfg["encryption"] else { return nil }
    let k = en.isTrue ? keys["encryption"]! : en
    return try EncryptionKey(key: k["key"]!.string, iv: k["iv"]?.string)
  }

  func imgproxyBuilder(_ cfg: JSONValue) throws(URLBuildError) -> ImgproxyURLBuilder {
    var b = ImgproxyURLBuilder(signing: try signing(cfg), encryption: try encryption(cfg))
    if let v = cfg["base_url"] { b.baseURL = v.string }
    if let v = cfg["unsigned_signature"] { b.unsignedSignature = v.string }
    if let v = cfg["source_encoding"] { b.sourceEncoding = try SourceEncoding(parsing: v.string) }
    if let v = cfg["split_source"] { b.splitSource = v.int }
    if let v = cfg["separator"] { b.separator = v.string }
    if let v = cfg["option_names"] { b.optionNames = try OptionNames(parsing: v.string) }
    return b
  }

  func queryBuilder(_ cfg: JSONValue) throws(URLBuildError) -> QueryURLBuilder {
    var b = QueryURLBuilder(signing: try signing(cfg), encryption: try encryption(cfg))
    if let v = cfg["base_url"] { b.baseURL = v.string }
    if let v = cfg["mount"] { b.mount = v.string }
    if let v = cfg["separator"] { b.separator = v.string }
    if let v = cfg["option_names"] { b.optionNames = try OptionNames(parsing: v.string) }
    return b
  }
}

/// The fixture's pipelines: lists of `[name, [args…]]`.
private func pipelines(_ input: JSONValue) -> [[OptionEntry]] {
  (input["options"]?.array ?? []).map { pl in
    pl.array.map { e in
      OptionEntry(name: e.array[0].string, arguments: e.array[1].array.map(fixtureArg))
    }
  }
}

/// The pipelines as ``Options`` (mapping form).
private func options(_ input: JSONValue) -> [Options] {
  pipelines(input).map { pl in
    var o = Options()
    for e in pl { o.set(e.name, arguments: e.arguments) }
    return o
  }
}

private func params(_ input: JSONValue) -> Params {
  Params((input["params"]?.array ?? []).map { ($0.array[0].string, fixtureArg($0.array[1])) })
}

private struct StreamInput {
  var proto: StreamProtocol
  var renditions: [Int]
  var segmentDuration: Int?
  var segmentFormat: SegmentFormat?
  var options: Options
  var sourceEncoding: SourceEncoding?
}

private func stream(_ input: JSONValue) throws(URLBuildError) -> StreamInput {
  let opts = options(input)
  precondition(opts.count <= 1, "stream cases have one pipeline")
  return StreamInput(
    proto: try StreamProtocol(parsing: input["protocol"]!.string),
    renditions: input["renditions"]?.array.map(\.int) ?? [],
    segmentDuration: input["segment_duration"]?.int,
    segmentFormat: try input["segment_format"].map { v throws(URLBuildError) in
      try SegmentFormat(parsing: v.string)
    },
    options: opts.first ?? [:],
    sourceEncoding: try input["source_encoding"].map { v throws(URLBuildError) in
      try SourceEncoding(parsing: v.string)
    })
}

private func range(_ v: JSONValue) -> ByteRange {
  if let raw = v.stringValue { return .raw(raw) }
  if let suffix = v["suffix"] { return .suffix(suffix.int) }
  return .range(start: v["start"]!.int, end: v["end"]?.int)
}

private func encoding(_ input: JSONValue) throws(URLBuildError) -> SourceEncoding? {
  guard let e = input["source_encoding"] else { return nil }
  return try SourceEncoding(parsing: e.string)
}

/// The case through the mapping form (``Options``, parameters, free functions), or the reason it is skipped.
private func runMapping(_ f: Fixture, _ c: JSONValue) -> Run {
  let cfg = c["config"] ?? .object([:])
  let input = c["input"]!
  let source = input["source"]?.stringValue ?? ""
  let ext = input["extension"]?.stringValue
  let pdf = input["pdf_password"]?.stringValue
  switch c["op"]!.string {
  case "imgproxy.url":
    return .success(
      attempt { () throws(URLBuildError) -> String in
        try f.imgproxyBuilder(cfg).url(
          source, pipelines: options(input), extension: ext, sourceEncoding: try encoding(input),
          pdfPassword: pdf)
      })
  case "imgproxy.info_url":
    if pipelines(input).count > 1 {
      return .skip("not expressible: infoURL takes one pipeline (Options), so it cannot chain")
    }
    return .success(
      attempt { () throws(URLBuildError) -> String in
        let opts = options(input)
        return try f.imgproxyBuilder(cfg).infoURL(
          source, options: opts.first ?? [:], sourceEncoding: try encoding(input), pdfPassword: pdf)
      })
  case "imgproxy.video_url":
    if pdf != nil {
      return .skip(
        "not expressible: videoURL has no PDF password parameter (the type system rejects it)")
    }
    if pipelines(input).count > 1 {
      return .skip("not expressible: videoURL takes one pipeline (Options), so it cannot chain")
    }
    return .success(
      attempt { () throws(URLBuildError) -> String in
        let opts = options(input)
        return try f.imgproxyBuilder(cfg).videoURL(
          source, options: opts.first ?? [:], extension: ext, sourceEncoding: try encoding(input))
      })
  case "imgproxy.stream_url":
    return .success(
      attempt { () throws(URLBuildError) -> String in
        let s = try stream(input)
        return try f.imgproxyBuilder(cfg).streamURL(
          source, protocol: s.proto, renditions: s.renditions, segmentDuration: s.segmentDuration,
          segmentFormat: s.segmentFormat, options: s.options, sourceEncoding: s.sourceEncoding)
      })
  case "query.url":
    return .success(
      attempt { () throws(URLBuildError) -> String in
        try f.queryBuilder(cfg).url(
          source, params: params(input), pipelines: options(input), extension: ext, pdfPassword: pdf
        )
      })
  case "query.video_url":
    return .success(
      attempt { () throws(URLBuildError) -> String in
        try f.queryBuilder(cfg).videoURL(source, pipelines: options(input), extension: ext)
      })
  case "query.stream_url":
    return .success(
      attempt { () throws(URLBuildError) -> String in
        let s = try stream(input)
        return try f.queryBuilder(cfg).streamURL(
          source, protocol: s.proto, renditions: s.renditions, segmentDuration: s.segmentDuration,
          segmentFormat: s.segmentFormat, options: s.options)
      })
  case "query.imaginary_url":
    return .success(
      attempt { () throws(URLBuildError) -> String in
        try f.queryBuilder(cfg).imaginaryURL(
          input["endpoint"]!.string, source, params: params(input))
      })
  case "sign":
    return .success(
      attempt { () throws(URLBuildError) -> String in
        sign(input["message"]!.string, key: try f.signing(cfg)!)
      })
  case "sign_xi_url":
    return .success(
      attempt { () throws(URLBuildError) -> String in
        try signXiURL(
          input["url"]!.string, key: try f.signing(cfg)!, mount: input["mount"]?.stringValue ?? "")
      })
  case "encrypt_source":
    return .success(
      attempt { () throws(URLBuildError) -> String in
        try encryptSource(source, key: try f.encryption(cfg)!)
      })
  case "encrypt_pdf_password":
    return .success(
      attempt { () throws(URLBuildError) -> String in
        try encryptPDFPassword(input["password"]!.string, key: try f.encryption(cfg)!)
      })
  case "xi_source":
    return .success(
      attempt { () throws(URLBuildError) -> String in
        try xiSource(input["name"]!.string, input["key"]!.string)
      })
  case "source_url":
    return .success(
      attempt { () throws(URLBuildError) -> String in
        try sourceURL(
          try SourceScheme(parsing: input["scheme"]!.string), input["authority"]!.string,
          input["key"]!.string)
      })
  case "range_header":
    return .success(
      attempt { () throws(URLBuildError) -> String in try rangeHeader(range(input["range"]!)) })
  case "escape_plain_source":
    return .success(.success(escapePlainSource(input["value"]!.string)))
  case "escape_query_value":
    return .success(.success(escapeQueryValue(input["value"]!.string)))
  case let op:
    fatalError("unknown op \(op)")
  }
}

/// Applies pipelines to a chain through `option(_:arguments:)`, and `pipeline` between them.
private func apply<C: OptionChain>(_ chain: C, _ pls: [[OptionEntry]], pipeline: ((C) -> C)? = nil)
  -> C
{
  var c = chain
  for (i, pl) in pls.enumerated() {
    if i > 0, let pipeline { c = pipeline(c) }
    for e in pl { c = c.option(e.name, arguments: e.arguments) }
  }
  return c
}

/// The case through the fluent chains, for the ops that have one (`nil` otherwise).
private func runFluent(_ f: Fixture, _ c: JSONValue) -> Outcome? {
  let cfg = c["config"] ?? .object([:])
  let input = c["input"]!
  let source = input["source"]?.stringValue ?? ""
  let pls = pipelines(input)
  let chained = pls.count > 1
  let ext = input["extension"]?.stringValue
  let pdf = input["pdf_password"]?.stringValue
  switch c["op"]!.string {
  case "imgproxy.url":
    return attempt { () throws(URLBuildError) -> String in
      var ch = apply(try f.imgproxyBuilder(cfg).image(source), pls) { $0.pipeline() }
      if let e = try encoding(input) { ch = ch.encoding(e) }
      if let ext { ch = ch.extension(ext) }
      if let pdf { ch = ch.withPDFPassword(pdf) }
      return try ch.build()
    }
  // /info and /video chains cannot chain pipelines; the mapping form covers that error.
  case "imgproxy.info_url" where !chained:
    return attempt { () throws(URLBuildError) -> String in
      var ch = apply(try f.imgproxyBuilder(cfg).info(source), pls)
      if let e = try encoding(input) { ch = ch.encoding(e) }
      if let pdf { ch = ch.withPDFPassword(pdf) }
      return try ch.build()
    }
  case "imgproxy.video_url" where !chained && pdf == nil:
    return attempt { () throws(URLBuildError) -> String in
      var ch = apply(try f.imgproxyBuilder(cfg).video(source), pls)
      if let e = try encoding(input) { ch = ch.encoding(e) }
      if let ext { ch = ch.extension(ext) }
      return try ch.build()
    }
  case "query.url":
    return attempt { () throws(URLBuildError) -> String in
      var ch = try f.queryBuilder(cfg).image(source)
      for p in params(input) { ch = ch.param(p.key, p.value) }
      ch = apply(ch, pls) { $0.pipeline() }
      if let ext { ch = ch.extension(ext) }
      if let pdf { ch = ch.withPDFPassword(pdf) }
      return try ch.build()
    }
  case "query.video_url" where !chained:
    return attempt { () throws(URLBuildError) -> String in
      var ch = apply(try f.queryBuilder(cfg).video(source), pls)
      if let ext { ch = ch.extension(ext) }
      return try ch.build()
    }
  default:
    return nil
  }
}

/// A case's outcome, or why the typed API cannot express it.
private enum Run {
  case success(Outcome)
  case skip(String)
}

@Suite struct ConformanceTests {
  @Test func conformanceFixture() throws {
    let fixture = try loadJSON("clients/spec/conformance.json")
    let f = Fixture(keys: fixture["keys"]!)
    let cases = fixture["cases"]!.array
    #expect(cases.count >= 100)
    #expect(Set(cases.map { $0["op"]!.string }).count >= 17)

    var passed = 0
    var fluentChecked = 0
    var skipped: [String] = []
    var failures: [String] = []
    for c in cases {
      let id = "\(c["op"]!.string):\(c["name"]!.string)"
      let expect = c["expect"]!
      let check = { (label: String, got: Outcome) in
        switch (expect["value"], got) {
        case (let want?, .success(let url)) where want.string == url: break
        case (nil, .failure(let e)) where expect["error"]!.string == e.code.rawValue: break
        default: failures.append("\(id) (\(label)): want \(expect), got \(got)")
        }
      }
      switch runMapping(f, c) {
      case .skip(let reason):
        skipped.append("\(id): \(reason)")
        continue
      case .success(let got):
        check("mapping form", got)
      }
      if let fluent = runFluent(f, c) {
        check("fluent form", fluent)
        fluentChecked += 1
      }
      passed += 1
    }
    print(
      "conformance: \(passed) cases run (\(fluentChecked) also through the fluent form), \(skipped.count) skipped"
    )
    for s in skipped { print("  skipped \(s)") }
    #expect(failures.isEmpty, "\(failures.count) failures:\n\(failures.joined(separator: "\n"))")
  }
}
