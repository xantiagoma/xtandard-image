// Query dialect URLs (docs/URL-DIALECTS.md §4), mounted at /q/.
//
//   {baseURL}{mount}/?url=<source>&<native key>=…&o=<imgproxy option path>[&xi_sig=…]
//   {baseURL}{mount}/video?url=<source>&<video option>=<arg,arg>…[&xi_sig=…]
//   {baseURL}{mount}/<imaginary endpoint>?url=<source>&<param>=…[&xi_sig=…]
//
// xi_sig covers the escaped path after the mount plus the query, so it is appended last.

/// Builds query dialect URLs (the server needs `XI_DIALECTS_QUERY_ENABLED=true`).
///
/// With a signing key, every URL ends in an `xi_sig` over the path after the mount and the query.
///
/// ```swift
/// let q = queryURLs(baseURL: "https://img.example.com")
/// try q.url("s3://bucket/cat.jpg", params: ["w": 300, "f": "webp"], options: ["bl": 2])
/// // "https://img.example.com/q/?url=s3://bucket/cat.jpg&w=300&f=webp&o=bl:2"
/// try q.image("s3://bucket/cat.jpg").param("w", 300).param("f", "webp").bl(2).build()
/// // the same URL
/// ```
public struct QueryURLBuilder: Sendable {
  /// The server origin (and any path before the mount).
  public var baseURL: String
  /// The query dialect's mount prefix (`XI_DIALECTS_QUERY_PREFIX`). Default `/q`.
  public var mount: String
  /// Signs with `xi_sig` (the imgproxy key and salt); without a key URLs are unsigned.
  public var signing: SigningKey?
  /// The key of PDF passwords (`XI_ENCRYPTION_KEYS`).
  public var encryption: EncryptionKey?
  /// The argument separator inside `o=`. Default `:`.
  public var separator: String
  /// How option names are written.
  public var optionNames: OptionNames

  /// A builder; the defaults make root-relative unsigned URLs under `/q` with `:` separators
  /// in `o=` and short option names.
  public init(
    baseURL: String = "",
    mount: String = SpecConstants.queryMount,
    signing: SigningKey? = nil,
    encryption: EncryptionKey? = nil,
    separator: String = SpecConstants.defaultSeparator,
    optionNames: OptionNames = .short
  ) {
    self.baseURL = baseURL
    self.mount = mount
    self.signing = signing
    self.encryption = encryption
    self.separator = separator
    self.optionNames = optionNames
  }

  /// Builds a native `/q/?url=…` URL: native keys, then the processing options as `o=`.
  ///
  /// - Parameters:
  ///   - source: The source URL or reference, written as `url=`.
  ///   - params: Native keys (`w`, `fit`, `g`, `f`, `q`, …), written before `o=`; `nil` values are skipped.
  ///   - options: Processing options, written as `o=`.
  ///   - ext: The output format (`format=`).
  ///   - pdfPassword: The plain-text password of a protected PDF, encrypted into `pdp=` (needs both keys).
  /// - Throws: ``URLBuildError`` whose code says what is wrong.
  public func url(
    _ source: String, params: Params = [:], options: Options = [:], extension ext: String? = nil,
    pdfPassword: String? = nil
  ) throws(URLBuildError) -> String {
    try buildImage([options.entries], source, params.pairs, ext, pdfPassword)
  }

  /// Builds a native `/q/?url=…` URL with chained pipelines in `o=` (`/-/`).
  ///
  /// - Throws: ``URLBuildError`` whose code says what is wrong.
  public func url(
    _ source: String, params: Params = [:], pipelines: [Options], extension ext: String? = nil,
    pdfPassword: String? = nil
  ) throws(URLBuildError) -> String {
    try buildImage(pipelines.map(\.entries), source, params.pairs, ext, pdfPassword)
  }

  /// Builds a `/q/video` URL; `crop` and `flip` are written `c` and `fl`.
  ///
  /// Several pipelines are written in order, so a key may repeat (later wins argument by argument).
  ///
  /// - Throws: ``URLBuildError`` whose code says what is wrong; an argument with `:` is
  ///   ``URLBuildError/Code/invalidArgument``.
  public func videoURL(_ source: String, options: Options = [:], extension ext: String? = nil)
    throws(URLBuildError) -> String
  {
    try buildVideo(options.entries, source, ext)
  }

  /// Builds a `/q/video` URL from several pipelines, written in order (keys may repeat).
  ///
  /// - Throws: ``URLBuildError`` whose code says what is wrong.
  public func videoURL(_ source: String, pipelines: [Options], extension ext: String? = nil)
    throws(URLBuildError) -> String
  {
    try buildVideo(pipelines.flatMap(\.entries), source, ext)
  }

  /// Builds a `/q/video` HLS or DASH manifest URL (`format=m3u8|mpd`).
  ///
  /// - Throws: ``URLBuildError`` whose code says what is wrong.
  public func streamURL(
    _ source: String, protocol proto: StreamProtocol, renditions: [Int] = [],
    segmentDuration: Int? = nil, segmentFormat: SegmentFormat? = nil, options: Options = [:]
  ) throws(URLBuildError) -> String {
    let (entries, ext) = try streamParts(
      proto, renditions: renditions, segmentDuration: segmentDuration,
      segmentFormat: segmentFormat, options: options)
    return try buildVideo(entries, source, ext)
  }

  /// Builds an imaginary endpoint URL (`/q/resize?url=…&width=300`).
  ///
  /// ```swift
  /// try queryURLs().imaginaryURL("resize", "https://e.com/a.jpg", params: ["width": 300])
  /// // "/q/resize?url=https://e.com/a.jpg&width=300"
  /// ```
  ///
  /// - Throws: ``URLBuildError/Code/invalidEndpoint`` when the endpoint is not lower-case
  ///   letters or is `video`.
  public func imaginaryURL(_ endpoint: String, _ source: String, params: Params = [:])
    throws(URLBuildError) -> String
  {
    let isEndpoint =
      !endpoint.isEmpty && endpoint != "video"
      && endpoint.utf8.allSatisfy { $0 >= UInt8(ascii: "a") && $0 <= UInt8(ascii: "z") }
    guard isEndpoint else {
      throw URLBuildError(.invalidEndpoint, "invalid imaginary endpoint \"\(endpoint)\"")
    }
    try checkText(source, .invalidSource, "source")
    let pairs = [("url", source)] + (try paramPairs(params.pairs))
    return try finish("/" + endpoint, pairs)
  }

  /// Builds an imaginary endpoint URL.
  ///
  /// - Throws: As ``imaginaryURL(_:_:params:)-(String,_,_)``.
  public func imaginaryURL(_ endpoint: ImaginaryEndpoint, _ source: String, params: Params = [:])
    throws(URLBuildError) -> String
  {
    try imaginaryURL(endpoint.rawValue, source, params: params)
  }

  /// Starts a fluent `/q/?url=…` URL for a source.
  public func image(_ source: String) -> QueryURLChain {
    QueryURLChain(builder: self, state: ChainState(source: source))
  }

  /// Starts a fluent `/q/video` URL for a source.
  public func video(_ source: String) -> QueryVideoURLChain {
    QueryVideoURLChain(builder: self, state: ChainState(source: source))
  }

  private func finish(_ path: String, _ pairs: [(String, String)]) throws(URLBuildError) -> String {
    var base = baseURL
    while base.hasSuffix("/") { base.removeLast() }
    var m = mount
    while m.hasSuffix("/") { m.removeLast() }
    var query = pairs.map { escapeQueryValue($0.0) + "=" + escapeQueryValue($0.1) }.joined(
      separator: "&")
    if let signing {
      query = try signXiQuery(path: path, rawQuery: query, key: signing)
    }
    return "\(base)\(m)\(path)?\(query)"
  }

  func buildImage(
    _ pipelines: [[OptionEntry]], _ source: String, _ params: [Params.Pair], _ ext: String?,
    _ pdfPassword: String?
  ) throws(URLBuildError) -> String {
    let sep = try checkSeparator(separator, forbidden: SpecConstants.forbiddenQuerySeparators)
    try checkText(source, .invalidSource, "source")
    var pairs = [("url", source)] + (try paramPairs(params))
    var piped: [String] = []
    for p in pipelines where !p.isEmpty {
      var segs: [String] = []
      for e in p {
        segs.append(try ProcessingOption.catalogue.format(e, separator: sep, style: optionNames))
      }
      piped.append(segs.joined(separator: "/"))
    }
    if !piped.isEmpty {
      pairs.append(("o", piped.joined(separator: "/" + SpecConstants.pipelineSeparator + "/")))
    }
    if let ext, !ext.isEmpty {
      pairs.append(("format", try checkExtension(ext)))
    }
    if let pdfPassword {
      let token = try passwordToken(encryption, signing, pdfPassword)
      let name =
        optionNames == .canonical
        ? SpecConstants.pdfPasswordName : SpecConstants.pdfPasswordShortName
      pairs.append((name, token))
    }
    return try finish("/", pairs)
  }

  func buildVideo(_ entries: [OptionEntry], _ source: String, _ ext: String?) throws(URLBuildError)
    -> String
  {
    try checkText(source, .invalidSource, "source")
    var pairs = [("url", source)]
    for e in entries {
      var key = try VideoOption.catalogue.writtenName(e.name, optionNames)
      if e.arguments.isEmpty {
        throw URLBuildError(.invalidArgument, "option \(e.name): at least one argument is required")
      }
      key = SpecConstants.nativeVideoKeys[key] ?? key
      var values: [String] = []
      for (i, a) in e.arguments.enumerated() {
        let location = "option \(e.name) argument \(i + 1)"
        let s = try formatArgument(a, separator: ",", location)
        if s.contains(":") {
          throw URLBuildError(.invalidArgument, "\(location): \"\(s)\" contains ':'")
        }
        values.append(s)
      }
      pairs.append((key, values.joined(separator: ",")))
    }
    if let ext, !ext.isEmpty {
      pairs.append(("format", try checkExtension(ext)))
    }
    return try finish("/video", pairs)
  }
}

/// A query URL builder: the TypeScript client's `queryUrls({…})` factory.
public func queryURLs(
  baseURL: String = "",
  mount: String = SpecConstants.queryMount,
  signing: SigningKey? = nil,
  encryption: EncryptionKey? = nil,
  separator: String = SpecConstants.defaultSeparator,
  optionNames: OptionNames = .short
) -> QueryURLBuilder {
  QueryURLBuilder(
    baseURL: baseURL, mount: mount, signing: signing, encryption: encryption,
    separator: separator, optionNames: optionNames)
}

/// The non-null native values as strings: booleans `true`/`false`, numbers as ECMAScript writes them.
private func paramPairs(_ params: [Params.Pair]) throws(URLBuildError) -> [(String, String)] {
  var out: [(String, String)] = []
  for p in params {
    let location = "query key \(p.key)"
    switch p.value {
    case .null: continue
    case .bool(let b): out.append((p.key, b ? "true" : "false"))
    case .int(let i): out.append((p.key, String(i)))
    case .double(let d): out.append((p.key, try formatNumber(d, location)))
    case .string(let s): out.append((p.key, s))
    }
  }
  return out
}

/// A fluent `/q/?url=…` URL (``QueryURLBuilder/image(_:)``): the option methods
/// write `o=`, ``param(_:_:)`` a native key.
public struct QueryURLChain: ProcessingOptionMethods {
  let builder: QueryURLBuilder
  var state: ChainState

  public func option(_ name: String, arguments: [Arg]) -> Self {
    var c = self
    c.state.push(name, arguments)
    return c
  }

  /// Appends a native key (`w`, `fit`, `g`, …), written before `o=`; `nil` skips it.
  public func param(_ key: String, _ value: (any ArgConvertible)?) -> Self {
    var c = self
    c.state.params.append(Params.Pair(key: key, value: Arg(value)))
    return c
  }

  /// Starts the next chained pipeline (`/-/` inside `o=`).
  public func pipeline() -> Self {
    var c = self
    c.state.pipelines.append([])
    return c
  }

  /// Sets the output format (`format=`).
  public func `extension`(_ ext: String) -> Self {
    var c = self
    c.state.ext = ext
    return c
  }

  /// Opens a password-protected PDF (`pdp=`; needs both keys).
  public func withPDFPassword(_ password: String) -> Self {
    var c = self
    c.state.pdfPassword = password
    return c
  }

  /// Builds the URL.
  ///
  /// - Throws: ``URLBuildError`` whose code says what is wrong.
  public func build() throws(URLBuildError) -> String {
    try builder.buildImage(
      state.pipelines, state.source, state.params, state.ext, state.pdfPassword)
  }
}

/// A fluent `/q/video` URL (``QueryURLBuilder/video(_:)``): one method per video option name and alias.
public struct QueryVideoURLChain: VideoOptionMethods {
  let builder: QueryURLBuilder
  var state: ChainState

  public func option(_ name: String, arguments: [Arg]) -> Self {
    var c = self
    c.state.push(name, arguments)
    return c
  }

  /// Sets the output container (`format=`).
  public func `extension`(_ ext: String) -> Self {
    var c = self
    c.state.ext = ext
    return c
  }

  /// Builds the URL.
  ///
  /// - Throws: ``URLBuildError`` whose code says what is wrong.
  public func build() throws(URLBuildError) -> String {
    try builder.buildVideo(state.pipelines.flatMap { $0 }, state.source, state.ext)
  }
}
