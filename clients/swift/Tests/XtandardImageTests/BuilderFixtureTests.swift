// testdata/urls/imgproxy-builder.json: 400+ cases derived from the server's golden tables.
//
// The URLs were written by the TypeScript client and parsed back by the Go
// dialect tests (TestBuilderFixture); every client must write the same ones:
// `url` with option names as given, `url_short` with the default short names,
// and `query_url` (query dialect) where the case has one. Both the fluent
// chains and the mapping form (Options) are checked.

import Testing
import XtandardImage

private typealias Pipeline = [OptionEntry]

private func pipelines(_ c: JSONValue) -> [Pipeline] {
  c["pipelines"]!.array.map { pl in
    pl.array.flatMap { entry in
      entry.object.map { OptionEntry(name: $0.key, arguments: $0.value.array.map(fixtureArg)) }
    }
  }
}

/// Whether a pipeline repeats an option name: an ``Options`` mapping cannot express that.
private func repeats(_ pls: [Pipeline]) -> Bool {
  pls.contains { pl in Set(pl.map(\.name)).count != pl.count }
}

private func asOptions(_ pl: Pipeline) -> Options {
  var o = Options()
  for e in pl { o.set(e.name, arguments: e.arguments) }
  return o
}

private func signing(_ fx: JSONValue, _ c: JSONValue) -> SigningKey? {
  guard let size = c["signature_size"]?.int, size > 0 else { return nil }
  return try! SigningKey(
    key: fx["signing"]!["key"]!.string, salt: fx["signing"]!["salt"]!.string, size: size)
}

private func builder(_ fx: JSONValue, _ c: JSONValue, _ style: OptionNames) -> ImgproxyURLBuilder {
  let enc = fx["encryption"]!
  return ImgproxyURLBuilder(
    signing: signing(fx, c),
    unsignedSignature: "unsafe",
    encryption: try! EncryptionKey(key: enc["key"]!.string, iv: enc["iv"]!.string),
    separator: c["separator"]?.string ?? ":",
    optionNames: style)
}

private func ext(_ c: JSONValue) -> String? {
  guard let e = c["extension"]?.stringValue, !e.isEmpty else { return nil }
  return e
}

private func fluent(_ fx: JSONValue, _ c: JSONValue, _ style: OptionNames) -> Outcome {
  let b = builder(fx, c, style)
  let source = c["source"]!.string
  let encoding = try! SourceEncoding(parsing: c["encoding"]!.string)
  let pls = pipelines(c)
  return attempt { () throws(URLBuildError) -> String in
    switch c["endpoint"]?.stringValue {
    case "info":
      var ch = b.info(source).encoding(encoding)
      for e in pls.joined() { ch = ch.option(e.name, arguments: e.arguments) }
      return try ch.build()
    case "video":
      var ch = b.video(source).encoding(encoding)
      for e in pls.joined() { ch = ch.option(e.name, arguments: e.arguments) }
      if let x = ext(c) { ch = ch.extension(x) }
      return try ch.build()
    default:
      var ch = b.image(source).encoding(encoding)
      for (i, pl) in pls.enumerated() {
        if i > 0 { ch = ch.pipeline() }
        for e in pl { ch = ch.option(e.name, arguments: e.arguments) }
      }
      if let x = ext(c) { ch = ch.extension(x) }
      return try ch.build()
    }
  }
}

private func mapping(_ fx: JSONValue, _ c: JSONValue, _ style: OptionNames) -> Outcome {
  let b = builder(fx, c, style)
  let source = c["source"]!.string
  let encoding = try! SourceEncoding(parsing: c["encoding"]!.string)
  let opts = pipelines(c).map(asOptions)
  return attempt { () throws(URLBuildError) -> String in
    switch c["endpoint"]?.stringValue {
    case "info":
      precondition(opts.count <= 1)
      return try b.infoURL(source, options: opts.first ?? [:], sourceEncoding: encoding)
    case "video":
      precondition(opts.count <= 1)
      return try b.videoURL(
        source, options: opts.first ?? [:], extension: ext(c), sourceEncoding: encoding)
    default:
      return try b.url(source, pipelines: opts, extension: ext(c), sourceEncoding: encoding)
    }
  }
}

private func query(_ fx: JSONValue, _ c: JSONValue) -> (fluent: Outcome, mapping: Outcome?) {
  let q = QueryURLBuilder(signing: signing(fx, c))
  let source = c["source"]!.string
  let pls = pipelines(c)
  if c["endpoint"]?.stringValue == "video" {
    let fluent = attempt { () throws(URLBuildError) -> String in
      var ch = q.video(source)
      for e in pls.joined() { ch = ch.option(e.name, arguments: e.arguments) }
      if let x = ext(c) { ch = ch.extension(x) }
      return try ch.build()
    }
    let mapping =
      repeats(pls)
      ? nil
      : attempt { () throws(URLBuildError) -> String in
        try q.videoURL(source, pipelines: pls.map(asOptions), extension: ext(c))
      }
    return (fluent, mapping)
  }
  let fluent = attempt { () throws(URLBuildError) -> String in
    var ch = q.image(source)
    for (i, pl) in pls.enumerated() {
      if i > 0 { ch = ch.pipeline() }
      for e in pl { ch = ch.option(e.name, arguments: e.arguments) }
    }
    if let x = ext(c) { ch = ch.extension(x) }
    return try ch.build()
  }
  let mapping =
    repeats(pls)
    ? nil
    : attempt { () throws(URLBuildError) -> String in
      try q.url(source, pipelines: pls.map(asOptions), extension: ext(c))
    }
  return (fluent, mapping)
}

@Suite struct BuilderFixtureTests {
  @Test func builderFixture() throws {
    let fx = try loadJSON("testdata/urls/imgproxy-builder.json")
    let cases = fx["cases"]!.array
    #expect(cases.count >= 400)
    var urls = 0
    var mappings = 0
    var queries = 0
    var failures: [String] = []
    for c in cases {
      let name = c["name"]!.string
      var checks: [(String, Outcome, JSONValue?)] = [
        ("url (given, fluent)", fluent(fx, c, .given), c["url"]),
        ("url_short (fluent)", fluent(fx, c, .short), c["url_short"]),
      ]
      if !repeats(pipelines(c)) {
        checks.append(("url (given, mapping)", mapping(fx, c, .given), c["url"]))
        checks.append(("url_short (mapping)", mapping(fx, c, .short), c["url_short"]))
        mappings += 1
      }
      if c["query"]?.isTrue == true {
        let (f, m) = query(fx, c)
        checks.append(("query_url (fluent)", f, c["query_url"]))
        if let m { checks.append(("query_url (mapping)", m, c["query_url"])) }
        queries += 1
      }
      for (label, got, want) in checks where (try? got.get()) != want?.stringValue {
        failures.append("\(name) \(label): want \(want ?? .null), got \(got)")
      }
      urls += 1
    }
    print(
      "builder fixture: \(urls) cases (url and url_short through the fluent form; \(mappings) also through the mapping form, the rest repeat an option name), \(queries) query URLs"
    )
    #expect(
      failures.isEmpty,
      "\(failures.count) failures:\n\(failures.prefix(30).joined(separator: "\n"))")
  }
}
