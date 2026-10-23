// imgproxy-dialect URL builder (docs/URL-DIALECTS.md §3.1).
//
//   {baseURL}[/info]/{signature}/{option}/…[/-/{option}/…]/{source}
//   {baseURL}/video/{signature}/{video option}/…/{source}   (signs "/video" + path)
//
// Sources are base64url (default), plain/ (percent-escaped, @ext) or enc/
// (AES-CBC); base64 and enc sources take .ext.

/// Builds imgproxy-dialect URLs for an xtandard-image (or imgproxy) server.
///
/// ```swift
/// let img = try imgproxy(
///     baseURL: "https://img.example.com",
///     signing: SigningKey(key: "736563726574", salt: "68656C6C6F"))
///
/// // Mapping form: options keyed by name or alias, in URL order.
/// let a = try img.url("s3://bucket/cat.jpg", options: ["rs": ["fill", 300, 400], "q": 80], extension: "webp")
///
/// // Fluent form: one method per option name and alias.
/// let b = try img.image("s3://bucket/cat.jpg")
///     .resize(ProcessingOption.ResizeType.fill, 300, 400).quality(80).extension("webp").build()
/// // a == b
/// ```
public struct ImgproxyURLBuilder: Sendable {
  /// Prepended to every path: the server origin and the dialect's mount prefix, if any.
  public var baseURL: String
  /// Signs URLs with the imgproxy scheme; without a key the signature segment is ``unsignedSignature``.
  public var signing: SigningKey?
  /// The signature segment of unsigned URLs: `insecure` (default), `unsafe` or `_`.
  public var unsignedSignature: String
  /// The key of encrypted sources and PDF passwords.
  public var encryption: EncryptionKey?
  /// The default source encoding.
  public var sourceEncoding: SourceEncoding
  /// Splits base64 and encrypted sources into `/`-separated chunks of this many characters (0: no split).
  public var splitSource: Int
  /// The argument separator (`XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR`), one character.
  public var separator: String
  /// How option names are written.
  public var optionNames: OptionNames

  /// A builder; the defaults make root-relative unsigned URLs (`/insecure/…`) with base64
  /// sources, `:` separators and short option names.
  public init(
    baseURL: String = "",
    signing: SigningKey? = nil,
    unsignedSignature: String = SpecConstants.defaultUnsigned,
    encryption: EncryptionKey? = nil,
    sourceEncoding: SourceEncoding = .base64,
    splitSource: Int = 0,
    separator: String = SpecConstants.defaultSeparator,
    optionNames: OptionNames = .short
  ) {
    self.baseURL = baseURL
    self.signing = signing
    self.unsignedSignature = unsignedSignature
    self.encryption = encryption
    self.sourceEncoding = sourceEncoding
    self.splitSource = splitSource
    self.separator = separator
    self.optionNames = optionNames
  }

  /// Builds a processing URL from one pipeline of options.
  ///
  /// - Parameters:
  ///   - source: The source URL or reference (`https://…`, `s3://…`, `xi://…`).
  ///   - options: The options, keyed by name or alias.
  ///   - ext: The output format (`@webp` for plain sources, `.webp` otherwise).
  ///   - sourceEncoding: Overrides the builder's source encoding.
  ///   - pdfPassword: The plain-text password of a protected PDF, encrypted into `pdp` (needs both keys).
  /// - Throws: ``URLBuildError`` whose code says what is wrong.
  public func url(
    _ source: String, options: Options = [:], extension ext: String? = nil,
    sourceEncoding: SourceEncoding? = nil, pdfPassword: String? = nil
  ) throws(URLBuildError) -> String {
    try url(
      source, pipelines: [options], extension: ext, sourceEncoding: sourceEncoding,
      pdfPassword: pdfPassword)
  }

  /// Builds a processing URL from chained pipelines (`/-/`).
  ///
  /// - Throws: ``URLBuildError`` whose code says what is wrong.
  public func url(
    _ source: String, pipelines: [Options], extension ext: String? = nil,
    sourceEncoding: SourceEncoding? = nil, pdfPassword: String? = nil
  ) throws(URLBuildError) -> String {
    try build(
      .process, pipelines.map(\.entries), source, sourceEncoding, ext, pdfPassword)
  }

  /// Builds an `/info` URL (one pipeline, no extension).
  ///
  /// - Throws: ``URLBuildError`` whose code says what is wrong.
  public func infoURL(
    _ source: String, options: Options = [:], sourceEncoding: SourceEncoding? = nil,
    pdfPassword: String? = nil
  ) throws(URLBuildError) -> String {
    try build(.info, [options.entries], source, sourceEncoding, nil, pdfPassword)
  }

  /// Builds a `/video` URL: a video transformation, audio or a waveform; the extension is the
  /// container. `/video` takes no PDF password.
  ///
  /// - Throws: ``URLBuildError`` whose code says what is wrong.
  public func videoURL(
    _ source: String, options: Options = [:], extension ext: String? = nil,
    sourceEncoding: SourceEncoding? = nil
  ) throws(URLBuildError) -> String {
    try build(.video, [options.entries], source, sourceEncoding, ext, nil)
  }

  /// Builds an HLS or DASH manifest URL (`/video/…@m3u8` or `.mpd`).
  ///
  /// - Parameters:
  ///   - source: The source URL or reference.
  ///   - proto: HLS or DASH.
  ///   - renditions: The rungs, by the short side of the frame (at most 8); empty for the server's ladder.
  ///   - segmentDuration: The segment length in whole seconds (1 to 30).
  ///   - segmentFormat: `fmp4` (CMAF, the default, never written) or `ts` (MPEG-TS, HLS only).
  ///   - options: Other `/video` options (trim, crop, quality, audio, …); `stream`,
  ///     `segment_duration`, `segment_format` and `format` are refused.
  ///   - sourceEncoding: Overrides the builder's source encoding.
  /// - Throws: ``URLBuildError`` whose code says what is wrong.
  public func streamURL(
    _ source: String, protocol proto: StreamProtocol, renditions: [Int] = [],
    segmentDuration: Int? = nil, segmentFormat: SegmentFormat? = nil, options: Options = [:],
    sourceEncoding: SourceEncoding? = nil
  ) throws(URLBuildError) -> String {
    let (entries, ext) = try streamParts(
      proto, renditions: renditions, segmentDuration: segmentDuration,
      segmentFormat: segmentFormat, options: options)
    return try build(.video, [entries], source, sourceEncoding, ext, nil)
  }

  /// Starts a fluent processing URL for a source.
  public func image(_ source: String) -> ImgproxyURLChain {
    ImgproxyURLChain(builder: self, state: ChainState(source: source))
  }

  /// Starts a fluent `/info` URL for a source.
  public func info(_ source: String) -> ImgproxyInfoURLChain {
    ImgproxyInfoURLChain(builder: self, state: ChainState(source: source))
  }

  /// Starts a fluent `/video` URL for a source.
  public func video(_ source: String) -> ImgproxyVideoURLChain {
    ImgproxyVideoURLChain(builder: self, state: ChainState(source: source))
  }

  enum Endpoint {
    case process, info, video

    var catalogue: Catalogue {
      switch self {
      case .process: ProcessingOption.catalogue
      case .info: InfoOption.catalogue
      case .video: VideoOption.catalogue
      }
    }
  }

  private func split(_ token: String) -> String {
    let n = splitSource
    guard n > 0, token.count > n else { return token }
    // Base64url is ASCII, so byte chunks are character chunks.
    let bytes = Array(token.utf8)
    return stride(from: 0, to: bytes.count, by: n)
      .map { String(decoding: bytes[$0..<min($0 + n, bytes.count)], as: UTF8.self) }
      .joined(separator: "/")
  }

  private func sourceSegment(_ source: String, _ encoding: SourceEncoding, _ ext: String?)
    throws(URLBuildError) -> String
  {
    try checkText(source, .invalidSource, "source")
    var ext = ext
    if let e = ext, e.isEmpty { ext = nil }
    if let e = ext { ext = try checkExtension(e) }
    switch encoding {
    case .plain:
      return "plain/" + escapePlainSource(source) + (ext.map { "@" + $0 } ?? "")
    case .base64:
      return split(base64URL(Array(source.utf8))) + (ext.map { "." + $0 } ?? "")
    case .encrypted:
      guard let key = encryption else {
        throw URLBuildError(.missingEncryption, "encrypted sources need an encryption key")
      }
      return SpecConstants.encryptedSourcePrefix + split(try encryptSource(source, key: key))
        + (ext.map { "." + $0 } ?? "")
    }
  }

  private func finish(_ endpoint: Endpoint, _ path: String) -> String {
    var base = baseURL
    while base.hasSuffix("/") { base.removeLast() }
    if endpoint == .video {
      let sig = signing.map { sign("/video" + path, key: $0) } ?? unsignedSignature
      return "\(base)/video/\(sig)\(path)"
    }
    let signed = signing.map { signImgproxyPath(path, key: $0) } ?? "/\(unsignedSignature)\(path)"
    return base + (endpoint == .info ? "/info" : "") + signed
  }

  /// Builds a URL from option pipelines and a source, checking in the order of clients/README.md.
  func build(
    _ endpoint: Endpoint, _ pipelines: [[OptionEntry]], _ source: String,
    _ encoding: SourceEncoding?, _ ext: String?, _ pdfPassword: String?
  ) throws(URLBuildError) -> String {
    let sep = try checkSeparator(separator, forbidden: SpecConstants.forbiddenSeparators)
    let cat = endpoint.catalogue
    let nonEmpty = pipelines.filter { !$0.isEmpty }
    if endpoint != .process && nonEmpty.count > 1 {
      throw URLBuildError(.invalidRequest, "/\(cat.kind) URLs have no chained pipelines")
    }
    if endpoint == .video && pdfPassword != nil {
      throw URLBuildError(.invalidRequest, "/video URLs take no pdf_password")
    }
    var segments: [String] = []
    if let pdfPassword {
      let token = try passwordToken(encryption, signing, pdfPassword)
      let name =
        optionNames == .canonical
        ? SpecConstants.pdfPasswordName : SpecConstants.pdfPasswordShortName
      segments.append("\(name)\(sep)\(token)")
    }
    for (i, p) in nonEmpty.enumerated() {
      if i > 0 { segments.append(SpecConstants.pipelineSeparator) }
      for e in p { segments.append(try cat.format(e, separator: sep, style: optionNames)) }
    }
    segments.append(try sourceSegment(source, encoding ?? sourceEncoding, ext))
    return finish(endpoint, "/" + segments.joined(separator: "/"))
  }
}

/// An imgproxy URL builder: the TypeScript client's `imgproxy({…})` factory.
public func imgproxy(
  baseURL: String = "",
  signing: SigningKey? = nil,
  unsignedSignature: String = SpecConstants.defaultUnsigned,
  encryption: EncryptionKey? = nil,
  sourceEncoding: SourceEncoding = .base64,
  splitSource: Int = 0,
  separator: String = SpecConstants.defaultSeparator,
  optionNames: OptionNames = .short
) -> ImgproxyURLBuilder {
  ImgproxyURLBuilder(
    baseURL: baseURL, signing: signing, unsignedSignature: unsignedSignature,
    encryption: encryption, sourceEncoding: sourceEncoding, splitSource: splitSource,
    separator: separator, optionNames: optionNames)
}

/// The `pdf_password` token, checking that both keys are present.
func passwordToken(_ encryption: EncryptionKey?, _ signing: SigningKey?, _ password: String)
  throws(URLBuildError) -> String
{
  guard let encryption else {
    throw URLBuildError(.missingEncryption, "pdf_password needs an encryption key")
  }
  guard signing != nil else {
    throw URLBuildError(
      .missingSigning, "pdf_password needs signing: the server refuses it on unsigned URLs")
  }
  return try encryptPDFPassword(password, key: encryption)
}

/// The video options and the manifest container (the extension) of a stream request.
func streamParts(
  _ proto: StreamProtocol, renditions: [Int], segmentDuration: Int?,
  segmentFormat: SegmentFormat?, options: Options
) throws(URLBuildError) -> ([OptionEntry], String) {
  var entries = options.entries
  for e in entries {
    switch VideoOption.catalogue.canonical(e.name) {
    case "stream", "segment_duration", "segment_format", "format":
      throw URLBuildError(
        .invalidRequest,
        "stream requests set \(e.name) through protocol, renditions and the segment settings")
    default: break
    }
  }
  if !renditions.isEmpty {
    entries.append(
      OptionEntry(name: "stream", arguments: [proto.arg] + renditions.map { .int(Int64($0)) }))
  }
  if let segmentDuration {
    entries.append(OptionEntry(name: "segment_duration", arguments: [.int(Int64(segmentDuration))]))
  }
  if let segmentFormat {
    guard segmentFormat.protocols.contains(proto) else {
      throw URLBuildError(
        .invalidRequest,
        "segment format \(segmentFormat.rawValue) is not available for \(proto.rawValue)")
    }
    if segmentFormat != .serverDefault {
      entries.append(OptionEntry(name: "segment_format", arguments: [segmentFormat.arg]))
    }
  }
  return (entries, proto.manifestContainer)
}

/// What a fluent chain collected.
struct ChainState: Sendable {
  var source: String
  var pipelines: [[OptionEntry]] = [[]]
  var params: [Params.Pair] = []
  var encoding: SourceEncoding?
  var ext: String?
  var pdfPassword: String?

  init(source: String) { self.source = source }

  mutating func push(_ name: String, _ args: [Arg]) {
    pipelines[pipelines.count - 1].append(OptionEntry(name: name, arguments: args))
  }
}

/// A fluent URL chain: the per-option methods (generated in protocol extensions) append
/// options through ``option(_:arguments:)``.
public protocol OptionChain: Sendable {
  /// Appends one option occurrence by name or alias (as the per-option methods do).
  func option(_ name: String, arguments: [Arg]) -> Self
}

extension OptionChain {
  /// Appends one option occurrence by name or alias: `option("rs", "fill", 300, 400)`.
  public func option(_ name: String, _ args: (any ArgConvertible)?...) -> Self {
    option(name, arguments: Arg.list(args))
  }

  /// Appends every option of an ``Options``, in order.
  public func options(_ options: Options) -> Self {
    options.reduce(self) { $0.option($1.name, arguments: $1.arguments) }
  }
}

/// A fluent processing URL (``ImgproxyURLBuilder/image(_:)``): one method per option
/// name and alias, ``pipeline()`` for `/-/`, then ``build()``.
///
/// ```swift
/// let url = try imgproxy(sourceEncoding: .plain, optionNames: .given)
///     .image("https://example.com/a.jpg").resize("fit", 1, 2).q(90).minWidth(10).build()
/// // "/insecure/resize:fit:1:2/q:90/min-width:10/plain/https://example.com/a.jpg"
/// ```
public struct ImgproxyURLChain: ProcessingOptionMethods {
  let builder: ImgproxyURLBuilder
  var state: ChainState

  public func option(_ name: String, arguments: [Arg]) -> Self {
    var c = self
    c.state.push(name, arguments)
    return c
  }

  /// Starts the next chained pipeline (`/-/`).
  public func pipeline() -> Self {
    var c = self
    c.state.pipelines.append([])
    return c
  }

  /// Overrides the source encoding.
  public func encoding(_ encoding: SourceEncoding) -> Self {
    var c = self
    c.state.encoding = encoding
    return c
  }

  /// Sets the output extension (`@webp` / `.webp`).
  public func `extension`(_ ext: String) -> Self {
    var c = self
    c.state.ext = ext
    return c
  }

  /// Opens a password-protected PDF: the plain-text password is encrypted into `pdp` (needs both keys).
  public func withPDFPassword(_ password: String) -> Self {
    var c = self
    c.state.pdfPassword = password
    return c
  }

  /// Builds the URL.
  ///
  /// - Throws: ``URLBuildError`` whose code says what is wrong.
  public func build() throws(URLBuildError) -> String {
    try builder.build(
      .process, state.pipelines, state.source, state.encoding, state.ext, state.pdfPassword)
  }
}

/// A fluent `/info` URL (``ImgproxyURLBuilder/info(_:)``).
public struct ImgproxyInfoURLChain: InfoOptionMethods {
  let builder: ImgproxyURLBuilder
  var state: ChainState

  public func option(_ name: String, arguments: [Arg]) -> Self {
    var c = self
    c.state.push(name, arguments)
    return c
  }

  /// Overrides the source encoding.
  public func encoding(_ encoding: SourceEncoding) -> Self {
    var c = self
    c.state.encoding = encoding
    return c
  }

  /// Opens a password-protected PDF (needs both keys).
  public func withPDFPassword(_ password: String) -> Self {
    var c = self
    c.state.pdfPassword = password
    return c
  }

  /// Builds the URL.
  ///
  /// - Throws: ``URLBuildError`` whose code says what is wrong.
  public func build() throws(URLBuildError) -> String {
    try builder.build(.info, state.pipelines, state.source, state.encoding, nil, state.pdfPassword)
  }
}

/// A fluent `/video` URL (``ImgproxyURLBuilder/video(_:)``): video transformations, audio and waveforms.
public struct ImgproxyVideoURLChain: VideoOptionMethods {
  let builder: ImgproxyURLBuilder
  var state: ChainState

  public func option(_ name: String, arguments: [Arg]) -> Self {
    var c = self
    c.state.push(name, arguments)
    return c
  }

  /// Overrides the source encoding.
  public func encoding(_ encoding: SourceEncoding) -> Self {
    var c = self
    c.state.encoding = encoding
    return c
  }

  /// Sets the output container (`webm`, `mp4`, `mp3`, `png`, …).
  public func `extension`(_ ext: String) -> Self {
    var c = self
    c.state.ext = ext
    return c
  }

  /// Builds the URL.
  ///
  /// - Throws: ``URLBuildError`` whose code says what is wrong.
  public func build() throws(URLBuildError) -> String {
    try builder.build(.video, state.pipelines, state.source, state.encoding, state.ext, nil)
  }
}
