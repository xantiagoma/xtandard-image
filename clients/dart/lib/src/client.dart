/// The HTTP client on `package:http`.
library;

import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

import 'builder.dart';
import 'encryption.dart';
import 'errors.dart';
import 'generated/constants.g.dart';
import 'generated/responses.g.dart';
import 'options.dart';
import 'signing.dart';

/// A byte range of a media request (206 Partial Content): bytes `start` to
/// `end` inclusive, the last `length` bytes, or a raw header value.
sealed class ByteRange {
  const ByteRange._();

  /// Bytes [start] to [end] (inclusive), or to the end when [end] is null.
  const factory ByteRange(int start, [int? end]) = ByteSpan;

  /// The last [length] bytes.
  const factory ByteRange.suffix(int length) = ByteSuffix;

  /// A Range header value as given (`bytes=0-99,200-299`).
  const factory ByteRange.raw(String header) = RawByteRange;
}

/// Bytes [start] to [end] (inclusive), or to the end when [end] is null.
final class ByteSpan extends ByteRange {
  /// Creates the range.
  const ByteSpan(this.start, [this.end]) : super._();

  /// The first byte.
  final int start;

  /// The last byte (inclusive), or null for the rest.
  final int? end;
}

/// The last [length] bytes.
final class ByteSuffix extends ByteRange {
  /// Creates the range.
  const ByteSuffix(this.length) : super._();

  /// How many bytes from the end.
  final int length;
}

/// A Range header value, sent as given.
final class RawByteRange extends ByteRange {
  /// Creates the range.
  const RawByteRange(this.header) : super._();

  /// The header value.
  final String header;
}

int _nonNegative(int n, String what) {
  if (n < 0) {
    throw UrlBuildException(
      ErrorCode.invalidRange,
      'range $what $n is not a non-negative integer',
    );
  }
  return n;
}

/// Formats a byte range as a Range header value (`bytes=0-1023`,
/// `bytes=-500`); throws [UrlBuildException] (`invalid_range`) for a
/// negative value or an end before the start.
String rangeHeader(ByteRange range) => switch (range) {
      RawByteRange(:final header) => header,
      ByteSuffix(:final length) => 'bytes=-${_nonNegative(length, 'suffix')}',
      ByteSpan(:final start, end: null) =>
        'bytes=${_nonNegative(start, 'start')}-',
      ByteSpan(:final start, end: final int end) => () {
          _nonNegative(start, 'start');
          _nonNegative(end, 'end');
          if (end < start) {
            throw UrlBuildException(
              ErrorCode.invalidRange,
              'range end $end is before start $start',
            );
          }
          return 'bytes=$start-$end';
        }(),
    };

/// The HTTP method of a media request.
enum HttpMethod {
  /// The body.
  get('GET'),

  /// The headers only (type, length, ETag).
  head('HEAD');

  const HttpMethod(this.value);

  /// The method as sent.
  final String value;
}

/// A `/readyz` state.
enum ReadyState {
  /// Serving.
  ready,

  /// Starting up (503).
  starting,

  /// Shutting down (503).
  draining,
}

/// The `/readyz` answer: `ready` is true only for 200 `ready`.
typedef Readiness = ({bool ready, ReadyState state});

/// A fetched HLS playlist or DASH MPD.
final class StreamManifest {
  /// Creates the manifest.
  const StreamManifest({
    required this.url,
    required this.contentType,
    required this.body,
  });

  /// The (signed) manifest URL; hand it to a player.
  final String url;

  /// `application/vnd.apple.mpegurl` or `application/dash+xml`.
  final String contentType;

  /// The manifest; its child URLs (`/_stream/<ticket>/…`) carry a ticket
  /// and need no signing.
  final String body;
}

final RegExp _scheme = RegExp(r'^[a-zA-Z][a-zA-Z0-9+.-]*:');

ErrorResponse? _envelope(String text) {
  try {
    final Object? v = jsonDecode(text);
    if (v is Map<String, Object?> &&
        v['error'] is Map<String, Object?> &&
        (v['error']! as Map<String, Object?>)['kind'] is String &&
        (v['error']! as Map<String, Object?>)['message'] is String) {
      return ErrorResponse.fromJson(v);
    }
  } on FormatException {
    // not JSON, or not the envelope
  }
  return null;
}

/// Converts a non-2xx response into an [XtandardHttpException] subclass
/// chosen by status.
XtandardHttpException toXtandardException(http.Response res, String url) {
  final text = res.body;
  final body = _envelope(text);
  final message = body?.error.message ??
      (text.trim().isNotEmpty
          ? text.trim()
          : res.reasonPhrase ?? 'HTTP ${res.statusCode}');
  return XtandardHttpException.forStatus(
    res.statusCode,
    url,
    message,
    body: body,
  );
}

/// A client of an xtandard-image server on any `http.Client`.
///
/// It covers the JSON and health endpoints (`/info`, `/healthz`,
/// `/readyz`, the playground's `api/meta`) and media fetches (images,
/// `/video` results with Range and HEAD, HLS and DASH manifests). URLs are
/// built (and signed) with [urls]. Non-2xx responses throw an
/// [XtandardHttpException] subclass chosen by status; transport failures
/// (and [timeout]) throw [TransportException].
///
/// ```dart
/// final xi = XtandardClient('http://localhost:8080', signing: key);
/// final info = await xi.info('s3://bucket/cat.jpg', options: {'size': true});
/// final head = await xi.video('s3://bucket/talk.mov', options: {'vd': 5},
///     extension: 'webm', method: HttpMethod.head);
/// xi.close();
/// ```
///
/// Pass `httpClient` to use your own: `IOClient`, `BrowserClient`,
/// `cronet_http`'s `CronetClient`, `cupertino_http`'s `CupertinoClient`, a
/// `RetryClient`, or a `MockClient` in tests. A client you pass is not
/// closed by [close].
final class XtandardClient {
  /// Creates a client for [baseUrl] (the server origin, plus the imgproxy
  /// mount prefix if any).
  ///
  /// The URL settings are those of [ImgproxyUrlBuilder]. [headers] are sent
  /// with every request. [playgroundBaseUrl] (default [baseUrl]) and
  /// [playgroundPath] (`XI_PLAYGROUND_PATH`) locate [playgroundMeta]; the
  /// playground lives on the admin listener by default.
  XtandardClient(
    String baseUrl, {
    http.Client? httpClient,
    SigningKey? signing,
    EncryptionKey? encryption,
    String unsignedSignature = defaultUnsigned,
    SourceEncoding sourceEncoding = SourceEncoding.base64,
    int? splitSource,
    String separator = defaultSeparator,
    OptionNames optionNames = OptionNames.short,
    Map<String, String> headers = const {},
    this.timeout,
    String? playgroundBaseUrl,
    String playgroundPath = '/_playground/',
  })  : baseUrl = _trim(baseUrl),
        _http = httpClient ?? http.Client(),
        _owned = httpClient == null,
        _headers = Map.unmodifiable(headers),
        _playgroundMeta = '${_trim(playgroundBaseUrl ?? baseUrl)}/'
            '${_strip(playgroundPath)}/api/meta',
        urls = ImgproxyUrlBuilder(
          baseUrl: _trim(baseUrl),
          signing: signing,
          unsignedSignature: unsignedSignature,
          encryption: encryption,
          sourceEncoding: sourceEncoding,
          splitSource: splitSource,
          separator: separator,
          optionNames: optionNames,
        );

  /// The server origin, without a trailing `/`.
  final String baseUrl;

  /// The URL builder of the client's requests.
  final ImgproxyUrlBuilder urls;

  /// A deadline for each request until its headers arrive, or null.
  final Duration? timeout;

  final http.Client _http;
  final bool _owned;
  final Map<String, String> _headers;
  final String _playgroundMeta;

  static String _trim(String s) =>
      s.endsWith('/') ? _trim(s.substring(0, s.length - 1)) : s;

  static String _strip(String s) {
    var t = _trim(s);
    while (t.startsWith('/')) {
      t = t.substring(1);
    }
    return t;
  }

  /// Closes the underlying `http.Client` if this client created it.
  void close() {
    if (_owned) _http.close();
  }

  String _abs(String url) => _scheme.hasMatch(url)
      ? url
      : '$baseUrl${url.startsWith('/') ? '' : '/'}$url';

  Future<http.StreamedResponse> _send(
    HttpMethod method,
    String url,
    String accept, {
    ByteRange? range,
    Map<String, String>? headers,
    Future<void>? abortTrigger,
  }) async {
    final req = http.AbortableRequest(
      method.value,
      Uri.parse(url),
      abortTrigger: abortTrigger,
    );
    req.headers
      ..addAll(_headers)
      ..addAll(headers ?? const {})
      ..['Accept'] = accept;
    if (range != null) req.headers['Range'] = rangeHeader(range);
    try {
      final sent = _http.send(req);
      final t = timeout;
      return await (t == null ? sent : sent.timeout(t));
    } on http.ClientException catch (e) {
      throw TransportException('${method.value} $url: ${e.message}', cause: e);
    } on TimeoutException catch (e) {
      throw TransportException('${method.value} $url: timed out', cause: e);
    }
  }

  Future<http.StreamedResponse> _checked(
    http.StreamedResponse res,
    String url,
  ) async {
    if (res.statusCode >= 200 && res.statusCode < 300) return res;
    throw toXtandardException(await _read(res, url), url);
  }

  Future<http.Response> _read(http.StreamedResponse res, String url) async {
    try {
      return await http.Response.fromStream(res);
    } on http.ClientException catch (e) {
      throw TransportException('reading $url: ${e.message}', cause: e);
    }
  }

  Future<Object?> _json(
    String url, {
    Map<String, String>? headers,
    Future<void>? abortTrigger,
  }) async {
    final res = await _read(
      await _checked(
        await _send(
          HttpMethod.get,
          url,
          'application/json',
          headers: headers,
          abortTrigger: abortTrigger,
        ),
        url,
      ),
      url,
    );
    return jsonDecode(res.body);
  }

  /// `GET /healthz`: completes while the process can serve HTTP.
  Future<void> healthz({
    Map<String, String>? headers,
    Future<void>? abortTrigger,
  }) async {
    final url = '$baseUrl/healthz';
    await _read(
      await _checked(
        await _send(
          HttpMethod.get,
          url,
          'text/plain, application/json',
          headers: headers,
          abortTrigger: abortTrigger,
        ),
        url,
      ),
      url,
    );
  }

  /// `GET /readyz`: the readiness state; 503 (starting, draining) is not an
  /// error.
  Future<Readiness> readyz({
    Map<String, String>? headers,
    Future<void>? abortTrigger,
  }) async {
    final url = '$baseUrl/readyz';
    final res = await _read(
      await _send(
        HttpMethod.get,
        url,
        'text/plain, application/json',
        headers: headers,
        abortTrigger: abortTrigger,
      ),
      url,
    );
    if (res.statusCode != 200 && res.statusCode != 503) {
      throw toXtandardException(res, url);
    }
    final text = res.body.trim();
    final state = ReadyState.values.where((s) => s.name == text).firstOrNull;
    if (state == null) {
      throw XtandardHttpException(
        res.statusCode,
        url,
        'unexpected readiness state "$text"',
      );
    }
    return (
      ready: res.statusCode == 200 && state == ReadyState.ready,
      state: state
    );
  }

  /// `GET /info` for [source], with the URL built (and signed) from the
  /// arguments of [ImgproxyUrlBuilder.infoUrl].
  Future<InfoResponse> info(
    String source, {
    Map<String, Object?>? options,
    SourceEncoding? sourceEncoding,
    String? pdfPassword,
    Map<String, String>? headers,
    Future<void>? abortTrigger,
  }) =>
      infoAt(
        urls.infoUrl(
          source,
          options: options,
          sourceEncoding: sourceEncoding,
          pdfPassword: pdfPassword,
        ),
        headers: headers,
        abortTrigger: abortTrigger,
      );

  /// `GET` a prebuilt `/info/…` path or URL.
  Future<InfoResponse> infoAt(
    String url, {
    Map<String, String>? headers,
    Future<void>? abortTrigger,
  }) async {
    final v =
        await _json(_abs(url), headers: headers, abortTrigger: abortTrigger);
    if (v is! Map<String, Object?>) {
      throw FormatException('/info answered ${v.runtimeType}, not an object');
    }
    return InfoResponse.fromJson(v);
  }

  /// `GET` (or `HEAD`) any URL or server path, buffered: an image or video
  /// URL, a query URL, a stream part. [accept] drives image format
  /// negotiation; [range] asks for part of the body (206).
  Future<http.Response> media(
    String url, {
    HttpMethod method = HttpMethod.get,
    ByteRange? range,
    String accept = '*/*',
    Map<String, String>? headers,
    Future<void>? abortTrigger,
  }) async {
    final abs = _abs(url);
    return _read(
      await mediaStreamed(
        abs,
        method: method,
        range: range,
        accept: accept,
        headers: headers,
        abortTrigger: abortTrigger,
      ),
      abs,
    );
  }

  /// Like [media], but the body is not read: listen to
  /// `http.StreamedResponse.stream` (or cancel it) to free the connection.
  Future<http.StreamedResponse> mediaStreamed(
    String url, {
    HttpMethod method = HttpMethod.get,
    ByteRange? range,
    String accept = '*/*',
    Map<String, String>? headers,
    Future<void>? abortTrigger,
  }) async {
    final abs = _abs(url);
    return _checked(
      await _send(
        method,
        abs,
        accept,
        range: range,
        headers: headers,
        abortTrigger: abortTrigger,
      ),
      abs,
    );
  }

  /// `GET` (or `HEAD`) a processed image; the URL arguments are those of
  /// [ImgproxyUrlBuilder.url].
  Future<http.Response> image(
    String source, {
    Map<String, Object?>? options,
    List<Map<String, Object?>>? pipelines,
    String? extension,
    SourceEncoding? sourceEncoding,
    String? pdfPassword,
    HttpMethod method = HttpMethod.get,
    String accept = '*/*',
    Map<String, String>? headers,
    Future<void>? abortTrigger,
  }) =>
      media(
        urls.url(
          source,
          options: options,
          pipelines: pipelines,
          extension: extension,
          sourceEncoding: sourceEncoding,
          pdfPassword: pdfPassword,
        ),
        method: method,
        accept: accept,
        headers: headers,
        abortTrigger: abortTrigger,
      );

  /// `GET` (or `HEAD`) a `/video` result, buffered: video, audio or a
  /// waveform; [range] asks for part of it (206). The URL arguments are
  /// those of [ImgproxyUrlBuilder.videoUrl].
  Future<http.Response> video(
    String source, {
    Map<String, Object?>? options,
    String? extension,
    SourceEncoding? sourceEncoding,
    HttpMethod method = HttpMethod.get,
    ByteRange? range,
    String accept = '*/*',
    Map<String, String>? headers,
    Future<void>? abortTrigger,
  }) =>
      media(
        urls.videoUrl(
          source,
          options: options,
          extension: extension,
          sourceEncoding: sourceEncoding,
        ),
        method: method,
        range: range,
        accept: accept,
        headers: headers,
        abortTrigger: abortTrigger,
      );

  /// Like [video], but the body is streamed (see [mediaStreamed]).
  Future<http.StreamedResponse> videoStreamed(
    String source, {
    Map<String, Object?>? options,
    String? extension,
    SourceEncoding? sourceEncoding,
    HttpMethod method = HttpMethod.get,
    ByteRange? range,
    String accept = '*/*',
    Map<String, String>? headers,
    Future<void>? abortTrigger,
  }) =>
      mediaStreamed(
        urls.videoUrl(
          source,
          options: options,
          extension: extension,
          sourceEncoding: sourceEncoding,
        ),
        method: method,
        range: range,
        accept: accept,
        headers: headers,
        abortTrigger: abortTrigger,
      );

  /// `GET` an HLS or DASH manifest (`/video/…@m3u8` or `@mpd`); the URL
  /// arguments are those of [ImgproxyUrlBuilder.streamUrl].
  Future<StreamManifest> stream(
    String source, {
    required StreamProtocol protocol,
    List<int>? renditions,
    int? segmentDuration,
    SegmentFormat? segmentFormat,
    Map<String, Object?>? options,
    SourceEncoding? sourceEncoding,
    Map<String, String>? headers,
    Future<void>? abortTrigger,
  }) async {
    final url = urls.streamUrl(
      source,
      protocol: protocol,
      renditions: renditions,
      segmentDuration: segmentDuration,
      segmentFormat: segmentFormat,
      options: options,
      sourceEncoding: sourceEncoding,
    );
    final res = await media(
      url,
      accept: '${protocol.contentType}, */*;q=0.5',
      headers: headers,
      abortTrigger: abortTrigger,
    );
    return StreamManifest(
      url: url,
      contentType: res.headers['content-type'] ?? protocol.contentType,
      body: res.body,
    );
  }

  /// `GET` the playground's `api/meta` (needs `XI_PLAYGROUND_ENABLED`; on
  /// the admin listener by default).
  Future<Map<String, Object?>> playgroundMeta({
    Map<String, String>? headers,
    Future<void>? abortTrigger,
  }) async {
    final v = await _json(
      _playgroundMeta,
      headers: headers,
      abortTrigger: abortTrigger,
    );
    if (v is! Map<String, Object?>) {
      throw FormatException(
          'api/meta answered ${v.runtimeType}, not an object');
    }
    return v;
  }
}
