import Foundation

#if canImport(FoundationNetworking)
  import FoundationNetworking
#endif

/// An async client of an xtandard-image server, on `URLSession`.
///
/// It covers the JSON and health endpoints (`/info`, `/healthz`, `/readyz`, the
/// playground's `api/meta`) and media fetches (images, `/video` results with
/// Range and HEAD, HLS and DASH manifests). URLs are built (and signed) with
/// the client's ``urls`` builder. A non-2xx status throws
/// ``ClientError/http(_:)``; a connection failure ``ClientError/transport(_:)``.
///
/// ```swift
/// let xi = XtandardClient(baseURL: "http://localhost:8080")
/// try await xi.healthz()
/// let info = try await xi.info("s3://bucket/cat.jpg", options: ["size": true])
/// print(info.width ?? 0, info.format ?? "")
/// let head = try await xi.video(
///     "s3://bucket/talk.mov", options: ["vd": 5], extension: "webm", media: .init(method: .head))
/// print(head.header("content-length") ?? "")
/// ```
public struct XtandardClient: Sendable {
  /// The server origin plus the imgproxy mount prefix, if any (no trailing slash).
  public let baseURL: String
  /// The URL builder of this client (base URL, keys and URL settings).
  public let urls: ImgproxyURLBuilder
  /// The session requests go through.
  public let session: URLSession
  /// Headers sent with every request.
  public let headers: [String: String]
  /// The timeout of each request, or `nil` for the session's.
  public let timeout: TimeInterval?
  let playgroundMetaURL: String

  /// A client of the server at `baseURL`.
  ///
  /// - Parameters:
  ///   - baseURL: The origin plus the imgproxy mount prefix, if any.
  ///   - signing: Signs the URLs the client builds.
  ///   - encryption: The key of encrypted sources and PDF passwords.
  ///   - unsignedSignature: The signature segment of unsigned URLs (default `insecure`).
  ///   - sourceEncoding: The default source encoding.
  ///   - splitSource: Splits base64 and encrypted sources into chunks (0: no split).
  ///   - separator: The argument separator (default `:`).
  ///   - optionNames: How option names are written.
  ///   - headers: Headers sent with every request.
  ///   - timeout: The timeout of each request (default 60 s; `nil`: the session's).
  ///   - session: The `URLSession` (default `.shared`).
  ///   - playgroundBaseURL: The origin of the admin listener for ``playgroundMeta()``; default `baseURL`.
  ///   - playgroundPath: `XI_PLAYGROUND_PATH`. Default `/_playground/`.
  public init(
    baseURL: String,
    signing: SigningKey? = nil,
    encryption: EncryptionKey? = nil,
    unsignedSignature: String = SpecConstants.defaultUnsigned,
    sourceEncoding: SourceEncoding = .base64,
    splitSource: Int = 0,
    separator: String = SpecConstants.defaultSeparator,
    optionNames: OptionNames = .short,
    headers: [String: String] = [:],
    timeout: TimeInterval? = 60,
    session: URLSession = .shared,
    playgroundBaseURL: String? = nil,
    playgroundPath: String = "/_playground/"
  ) {
    var base = baseURL
    while base.hasSuffix("/") { base.removeLast() }
    self.baseURL = base
    urls = ImgproxyURLBuilder(
      baseURL: base, signing: signing, unsignedSignature: unsignedSignature,
      encryption: encryption, sourceEncoding: sourceEncoding, splitSource: splitSource,
      separator: separator, optionNames: optionNames)
    self.session = session
    self.headers = headers
    self.timeout = timeout
    var origin = playgroundBaseURL ?? base
    while origin.hasSuffix("/") { origin.removeLast() }
    let path = playgroundPath.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
    playgroundMetaURL = "\(origin)/\(path)/api/meta"
  }

  // MARK: Endpoints

  /// `GET /healthz`: succeeds while the process can serve HTTP.
  ///
  /// - Throws: ``ClientError/http(_:)`` or ``ClientError/transport(_:)``.
  public func healthz() async throws(ClientError) {
    let r = try await send(.get, endpoint("/healthz"), accept: "text/plain")
    try checked(r)
  }

  /// `GET /readyz`: the readiness state; 503 (starting, draining) is not an error.
  ///
  /// - Throws: ``ClientError/http(_:)`` for another status, ``ClientError/decode(url:message:)``
  ///   for an unknown state.
  public func readyz() async throws(ClientError) -> Readiness {
    let url = endpoint("/readyz")
    let r = try await send(.get, url, accept: "text/plain")
    guard r.status == 200 || r.status == 503 else {
      try checked(r)
      throw .decode(url: url, message: "unexpected status \(r.status)")
    }
    let text = String(decoding: r.body, as: UTF8.self).trimmingCharacters(
      in: .whitespacesAndNewlines)
    guard let state = Readiness.State(rawValue: text) else {
      throw .decode(url: url, message: "unexpected readiness state \"\(text)\"")
    }
    return Readiness(ready: r.status == 200 && state == .ready, state: state)
  }

  /// The `/info` URL of a source (built and signed with ``urls``).
  ///
  /// - Throws: ``URLBuildError``.
  public func infoURL(
    _ source: String, options: Options = [:], sourceEncoding: SourceEncoding? = nil,
    pdfPassword: String? = nil
  ) throws(URLBuildError) -> String {
    try urls.infoURL(
      source, options: options, sourceEncoding: sourceEncoding, pdfPassword: pdfPassword)
  }

  /// `GET /info` for a source, with the URL built (and signed) from the arguments.
  ///
  /// - Throws: ``ClientError``.
  public func info(
    _ source: String, options: Options = [:], sourceEncoding: SourceEncoding? = nil,
    pdfPassword: String? = nil
  ) async throws(ClientError) -> InfoResponse {
    let url = try built { () throws(URLBuildError) -> String in
      try urls.infoURL(
        source, options: options, sourceEncoding: sourceEncoding, pdfPassword: pdfPassword)
    }
    return try await json(url)
  }

  /// `GET` a prebuilt `/info/…` path or URL.
  ///
  /// - Throws: ``ClientError``.
  public func info(at url: String) async throws(ClientError) -> InfoResponse {
    try await json(absolute(url))
  }

  /// `GET` (or `HEAD`) any URL or server path: an image or video URL, a query URL, a stream part.
  ///
  /// - Throws: ``ClientError/http(_:)`` or ``ClientError/transport(_:)``.
  public func media(_ url: String, _ media: MediaOptions = MediaOptions()) async throws(ClientError)
    -> MediaResponse
  {
    let r = try await send(
      media.method, absolute(url), accept: media.accept ?? "*/*", range: media.range,
      extra: media.headers)
    try checked(r)
    return r
  }

  /// `GET` (or `HEAD`) a processed image; the `Accept` header drives format negotiation.
  ///
  /// - Throws: ``ClientError``.
  public func image(
    _ source: String, options: Options = [:], extension ext: String? = nil,
    sourceEncoding: SourceEncoding? = nil, pdfPassword: String? = nil,
    media: MediaOptions = MediaOptions()
  ) async throws(ClientError) -> MediaResponse {
    let url = try built { () throws(URLBuildError) -> String in
      try urls.url(
        source, options: options, extension: ext, sourceEncoding: sourceEncoding,
        pdfPassword: pdfPassword)
    }
    return try await self.media(url, media)
  }

  /// `GET` (or `HEAD`) a `/video` result: video, audio or a waveform; a range asks for part of it (206).
  ///
  /// - Throws: ``ClientError``.
  public func video(
    _ source: String, options: Options = [:], extension ext: String? = nil,
    sourceEncoding: SourceEncoding? = nil, media: MediaOptions = MediaOptions()
  ) async throws(ClientError) -> MediaResponse {
    let url = try built { () throws(URLBuildError) -> String in
      try urls.videoURL(source, options: options, extension: ext, sourceEncoding: sourceEncoding)
    }
    return try await self.media(url, media)
  }

  /// `GET` an HLS or DASH manifest (`/video/…@m3u8` or `.mpd`).
  ///
  /// - Throws: ``ClientError``.
  public func stream(
    _ source: String, protocol proto: StreamProtocol, renditions: [Int] = [],
    segmentDuration: Int? = nil, segmentFormat: SegmentFormat? = nil, options: Options = [:],
    sourceEncoding: SourceEncoding? = nil
  ) async throws(ClientError) -> StreamManifest {
    let url = try built { () throws(URLBuildError) -> String in
      try urls.streamURL(
        source, protocol: proto, renditions: renditions, segmentDuration: segmentDuration,
        segmentFormat: segmentFormat, options: options, sourceEncoding: sourceEncoding)
    }
    let r = try await send(.get, url, accept: "\(proto.contentType), */*;q=0.5")
    try checked(r)
    return StreamManifest(
      url: url, contentType: r.contentType ?? proto.contentType,
      body: String(decoding: r.body, as: UTF8.self))
  }

  /// `GET` the playground's `api/meta` (needs `XI_PLAYGROUND_ENABLED`; admin listener by default).
  ///
  /// - Throws: ``ClientError``.
  public func playgroundMeta() async throws(ClientError) -> JSONValue {
    try await json(playgroundMetaURL)
  }

  #if !canImport(FoundationNetworking)
    /// `GET` a URL or server path and streams the body as `URLSession.AsyncBytes`
    /// (Apple platforms; on Linux use ``media(_:_:)``).
    ///
    /// The response has no ``MediaResponse/body``; iterate the bytes instead.
    ///
    /// - Throws: ``ClientError/http(_:)`` (the error body is read) or ``ClientError/transport(_:)``.
    public func bytes(_ url: String, _ media: MediaOptions = MediaOptions())
      async throws(ClientError)
      -> (URLSession.AsyncBytes, MediaResponse)
    {
      let target = absolute(url)
      let request = try makeRequest(
        media.method, target, accept: media.accept ?? "*/*", range: media.range,
        extra: media.headers)
      let bytes: URLSession.AsyncBytes
      let response: URLResponse
      do {
        (bytes, response) = try await session.bytes(for: request)
      } catch {
        throw .transport(TransportError(method: media.method.rawValue, url: target, error: error))
      }
      let head = MediaResponse(url: target, response: response, body: Data())
      if !(200..<300).contains(head.status) {
        var body = Data()
        do {
          for try await b in bytes { body.append(b) }
        } catch {
          throw .transport(TransportError(method: media.method.rawValue, url: target, error: error))
        }
        try checked(MediaResponse(url: target, response: response, body: body))
      }
      return (bytes, head)
    }
  #endif

  // MARK: Plumbing

  private func endpoint(_ path: String) -> String { baseURL + path }

  /// An absolute URL for a server path; absolute URLs are kept.
  private func absolute(_ url: String) -> String {
    if let colon = url.firstIndex(of: ":") {
      let scheme = url[..<colon]
      if let first = scheme.unicodeScalars.first, first.isASCII, first.properties.isAlphabetic,
        scheme.unicodeScalars.allSatisfy({
          $0.isASCII
            && ($0.properties.isAlphabetic || ("0"..."9").contains($0)
              || "+.-".unicodeScalars.contains($0))
        })
      {
        return url
      }
    }
    return baseURL + (url.hasPrefix("/") ? "" : "/") + url
  }

  private func built(_ make: () throws(URLBuildError) -> String) throws(ClientError) -> String {
    do { return try make() } catch { throw .build(error) }
  }

  private func makeRequest(
    _ method: HTTPMethod, _ url: String, accept: String, range: ByteRange?, extra: [String: String]
  ) throws(ClientError) -> URLRequest {
    guard let u = URL(string: url) else {
      throw .transport(TransportError(method: method.rawValue, url: url, message: "invalid URL"))
    }
    var req = URLRequest(url: u)
    req.httpMethod = method.rawValue
    if let timeout { req.timeoutInterval = timeout }
    for (k, v) in headers { req.setValue(v, forHTTPHeaderField: k) }
    for (k, v) in extra { req.setValue(v, forHTTPHeaderField: k) }
    req.setValue(accept, forHTTPHeaderField: "Accept")
    if let range {
      req.setValue(
        try built { () throws(URLBuildError) -> String in try rangeHeader(range) },
        forHTTPHeaderField: "Range")
    }
    return req
  }

  private func send(
    _ method: HTTPMethod, _ url: String, accept: String, range: ByteRange? = nil,
    extra: [String: String] = [:]
  ) async throws(ClientError) -> MediaResponse {
    let request = try makeRequest(method, url, accept: accept, range: range, extra: extra)
    do {
      let (data, response) = try await session.data(for: request)
      return MediaResponse(url: url, response: response, body: data)
    } catch {
      throw .transport(TransportError(method: method.rawValue, url: url, error: error))
    }
  }

  private func checked(_ r: MediaResponse) throws(ClientError) {
    guard (200..<300).contains(r.status) else {
      throw .http(HTTPError(status: r.status, url: r.url, body: r.body))
    }
  }

  private func json<T: Decodable>(_ url: String) async throws(ClientError) -> T {
    let r = try await send(.get, url, accept: "application/json")
    try checked(r)
    do {
      return try JSONDecoder().decode(T.self, from: r.body)
    } catch {
      throw .decode(url: url, message: String(describing: error))
    }
  }
}

/// The method of a media request.
public enum HTTPMethod: String, Sendable, Hashable {
  /// `GET` (default).
  case get = "GET"
  /// `HEAD`: the headers only.
  case head = "HEAD"
}

/// How a media request (image, video, stream part) is sent.
public struct MediaOptions: Sendable, Hashable {
  /// `GET` or `HEAD`.
  public var method: HTTPMethod
  /// Part of the body (206): `.bytes(0, 1023)`, `.from(100)`, `.suffix(500)`.
  public var range: ByteRange?
  /// The `Accept` header, which drives format negotiation (`image/avif,image/webp`); default `*/*`.
  public var accept: String?
  /// Extra request headers.
  public var headers: [String: String]

  /// `GET`, the whole body, `Accept: */*`.
  public init(
    method: HTTPMethod = .get, range: ByteRange? = nil, accept: String? = nil,
    headers: [String: String] = [:]
  ) {
    self.method = method
    self.range = range
    self.accept = accept
    self.headers = headers
  }
}

/// A response of a media request.
public struct MediaResponse: Sendable {
  /// The request URL.
  public let url: String
  /// The HTTP status (200, or 206 for a range).
  public let status: Int
  /// The response headers, by lower-case name.
  public let headers: [String: String]
  /// The body (empty for `HEAD`).
  public let body: Data

  init(url: String, response: URLResponse, body: Data) {
    self.url = url
    self.body = body
    let http = response as? HTTPURLResponse
    status = http?.statusCode ?? 0
    var h: [String: String] = [:]
    for (k, v) in http?.allHeaderFields ?? [:] {
      if let k = k as? String { h[k.lowercased()] = v as? String ?? "\(v)" }
    }
    headers = h
  }

  /// A header by name (any case).
  public func header(_ name: String) -> String? { headers[name.lowercased()] }

  /// The `Content-Type` header.
  public var contentType: String? { header("content-type") }
}

/// A fetched HLS playlist or DASH MPD.
public struct StreamManifest: Sendable, Hashable {
  /// The (signed) manifest URL; hand it to a player.
  public let url: String
  /// `application/vnd.apple.mpegurl` or `application/dash+xml`.
  public let contentType: String
  /// The manifest; its child URLs (`/_stream/<ticket>/…`) carry a ticket and need no signing.
  public let body: String
}

/// The `/readyz` answer: ``ready`` is true only for 200 `ready`.
public struct Readiness: Sendable, Hashable {
  /// The readiness states of `/readyz`.
  public enum State: String, Sendable, Hashable {
    /// Serving.
    case ready
    /// Starting up (503).
    case starting
    /// Shutting down (503).
    case draining
  }

  /// Whether the server takes traffic.
  public let ready: Bool
  /// The state the server reported.
  public let state: State
}

/// A request that did not succeed.
public enum ClientError: Error, Sendable, CustomStringConvertible {
  /// The URL could not be built.
  case build(URLBuildError)
  /// The server answered with a non-2xx status.
  case http(HTTPError)
  /// The request got no response (connection, TLS, timeout).
  case transport(TransportError)
  /// A 2xx body that is not what the endpoint returns (JSON that does not parse,
  /// an unknown readiness state).
  case decode(url: String, message: String)

  /// The HTTP error, when the server answered with a non-2xx status.
  public var httpError: HTTPError? {
    if case .http(let e) = self { return e }
    return nil
  }

  public var description: String {
    switch self {
    case .build(let e): "build: \(e)"
    case .http(let e): "\(e)"
    case .transport(let e): "\(e)"
    case .decode(let url, let message): "\(url): \(message)"
    }
  }
}

/// A request that got no response.
public struct TransportError: Error, Sendable, CustomStringConvertible {
  /// The request method.
  public let method: String
  /// The request URL.
  public let url: String
  /// What went wrong.
  public let message: String
  /// The `URLError` code, when the session reported one.
  public let code: URLError.Code?

  init(method: String, url: String, message: String) {
    self.method = method
    self.url = url
    self.message = message
    code = nil
  }

  init(method: String, url: String, error: any Error) {
    self.method = method
    self.url = url
    message = String(describing: error)
    code = (error as? URLError)?.code
  }

  public var description: String { "\(method) \(url): \(message)" }
}

/// A non-2xx response.
///
/// ``kind``, ``message`` and ``requestID`` come from the server's JSON error
/// envelope; a body that is not the envelope (a proxy's error page, say)
/// yields kind `"unknown"` and the body text.
public struct HTTPError: Error, Sendable, CustomStringConvertible {
  /// What a status means, as the Python client's exception classes have it.
  public enum Category: Sendable, Hashable {
    /// 400: the URL is invalid (kind `invalid_url`).
    case badRequest
    /// 403: a bad or missing signature (`unauthorized`) or a refused source or option (`forbidden`).
    case forbidden
    /// 404: the source does not exist (`not_found`) or the URL expired (`expired`).
    case notFound
    /// 406: no acceptable output format.
    case notAcceptable
    /// 422: the source is too large, unsupported, corrupt or over a processing limit.
    case unprocessable
    /// 429: the server is overloaded; retry later.
    case tooManyRequests
    /// 502 and 504: an upstream error or timeout.
    case gateway
    /// 503: the server is unavailable or draining.
    case serviceUnavailable
    /// Another 5xx.
    case server
    /// Another status.
    case other
  }

  /// The HTTP status.
  public let status: Int
  /// The error kind (`invalid_url`, `unauthorized`, `not_found`, …) or `"unknown"`.
  public let kind: String
  /// The client-safe message.
  public let message: String
  /// The request ID from the envelope, also in the server logs.
  public let requestID: String?
  /// The request URL.
  public let url: String
  /// The parsed envelope, when the body was one.
  public let body: ErrorResponse?

  init(status: Int, url: String, body: Data) {
    self.status = status
    self.url = url
    if let env = try? JSONDecoder().decode(ErrorResponse.self, from: body) {
      kind = env.error.kind
      message = env.error.message
      requestID = env.error.requestID
      self.body = env
      return
    }
    let text = String(decoding: body, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
    kind = "unknown"
    message =
      text.isEmpty ? HTTPURLResponse.localizedString(forStatusCode: status) : text
    requestID = nil
    self.body = nil
  }

  /// The kind as an ``ErrorKind``, when this client knows it.
  public var errorKind: ErrorKind? { ErrorKind(rawValue: kind) }

  /// The category of the status.
  public var category: Category {
    switch status {
    case 400: .badRequest
    case 403: .forbidden
    case 404: .notFound
    case 406: .notAcceptable
    case 422: .unprocessable
    case 429: .tooManyRequests
    case 502, 504: .gateway
    case 503: .serviceUnavailable
    case 500...: .server
    default: .other
    }
  }

  /// Whether the status is 5xx.
  public var isServerError: Bool { status >= 500 }

  public var description: String { "\(status) \(kind): \(message)" }
}
