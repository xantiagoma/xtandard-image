// Query dialect URLs (docs/URL-DIALECTS.md §4), mounted at /q/:
//
//   {baseUrl}{mount}/?url=<source>&<native key>=…&o=<imgproxy option path>[&xi_sig=…]
//   {baseUrl}{mount}/video?url=<source>&<video option>=<arg,arg>…[&xi_sig=…]
//   {baseUrl}{mount}/<imaginary endpoint>?url=<source>&<param>=…[&xi_sig=…]
//
// xi_sig covers the escaped path after the mount plus the query, so it is
// appended last.
part of 'builder.dart';

String _nativeValue(String key, Object? v) => switch (v) {
      final bool b => b ? 'true' : 'false',
      final num n => formatNumber(n, 'query key $key'),
      final String s => s,
      _ => throw UrlBuildException(
          ErrorCode.invalidArgument,
          'query key $key: unsupported value type ${v.runtimeType}',
        ),
    };

final RegExp _endpoint = RegExp(r'^[a-z]+$');

/// Builds query dialect URLs (the server needs
/// `XI_DIALECTS_QUERY_ENABLED=true`).
///
/// With [signing], every URL ends in an `xi_sig` over the path after the
/// mount and the query:
///
/// ```dart
/// final q = QueryUrlBuilder(baseUrl: 'https://img.example.com', signing: key);
/// q.url('s3://bucket/cat.jpg', params: {'w': 300, 'f': 'webp'}, options: {'bl': 2});
/// // https://img.example.com/q/?url=s3://bucket/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=…
/// ```
final class QueryUrlBuilder {
  /// Creates a builder.
  ///
  /// - [baseUrl]: the server origin (and any path before the mount).
  /// - [mount]: the query dialect's mount prefix
  ///   (`XI_DIALECTS_QUERY_PREFIX`).
  /// - [signing]: signs with xi_sig (the imgproxy key and salt); without
  ///   it URLs are unsigned.
  /// - [encryption]: the key of PDF passwords (`XI_ENCRYPTION_KEYS`).
  /// - [separator]: the argument separator inside `o=`.
  /// - [optionNames]: how option names are written.
  const QueryUrlBuilder({
    this.baseUrl = '',
    this.mount = queryMount,
    this.signing,
    this.encryption,
    this.separator = defaultSeparator,
    this.optionNames = OptionNames.short,
  });

  /// The server origin.
  final String baseUrl;

  /// The query dialect's mount prefix.
  final String mount;

  /// The signing key, or null for unsigned URLs.
  final SigningKey? signing;

  /// The encryption key of PDF passwords.
  final EncryptionKey? encryption;

  /// The argument separator inside `o=`.
  final String separator;

  /// How option names are written.
  final OptionNames optionNames;

  /// Builds a native `/q/?url=…` URL: [params] are native keys (`w`, `fit`,
  /// `g`, …; see [queryImageKeys]) in map order, null values skipped, then
  /// [options] and [pipelines] as `o=`, [extension] as `format=` and
  /// [pdfPassword] as `pdp=`.
  String url(
    String source, {
    Map<String, Object?>? params,
    Map<String, Object?>? options,
    List<Map<String, Object?>>? pipelines,
    String? extension,
    String? pdfPassword,
  }) =>
      _image(
        pipelinesOf(options, pipelines),
        source,
        [
          for (final MapEntry(:key, :value) in (params ?? const {}).entries)
            (key, value),
        ],
        extension,
        pdfPassword,
      );

  /// Builds a `/q/video` URL; `crop` and `flip` are written `c` and `fl`.
  ///
  /// [pipelines] are more option maps written after [options], in order,
  /// so a key may repeat (later wins argument by argument).
  String videoUrl(
    String source, {
    Map<String, Object?>? options,
    List<Map<String, Object?>>? pipelines,
    String? extension,
  }) =>
      _video(
        [for (final p in pipelinesOf(options, pipelines)) ...p],
        source,
        extension,
      );

  /// Builds a `/q/video` HLS or DASH manifest URL (`format=m3u8|mpd`); the
  /// arguments are those of [ImgproxyUrlBuilder.streamUrl].
  String streamUrl(
    String source, {
    required StreamProtocol protocol,
    List<int>? renditions,
    int? segmentDuration,
    SegmentFormat? segmentFormat,
    Map<String, Object?>? options,
  }) {
    final (entries, ext) = _streamParts(
      pipelinesOf(options, null),
      protocol,
      renditions,
      segmentDuration,
      segmentFormat,
    );
    return _video(entries, source, ext);
  }

  /// Builds an imaginary endpoint URL (`/q/resize?url=…&width=300`); see
  /// [imaginaryEndpoints] and [imaginaryParams].
  String imaginaryUrl(
    String endpoint,
    String source, {
    Map<String, Object?>? params,
  }) {
    if (!_endpoint.hasMatch(endpoint) || endpoint == 'video') {
      throw UrlBuildException(
        ErrorCode.invalidEndpoint,
        'invalid imaginary endpoint "$endpoint"',
      );
    }
    return _finish('/$endpoint', [
      ('url', checkText(source, ErrorCode.invalidSource, 'source')),
      ..._pairs([
        for (final MapEntry(:key, :value) in (params ?? const {}).entries)
          (key, value),
      ]),
    ]);
  }

  /// Starts a fluent `/q/?url=…` URL for [source].
  QueryUrlChain image(String source) => QueryUrlChain._(
        (s) => _image(
          s.pipelines,
          s.source,
          s.params,
          s.extension,
          s.pdfPassword,
        ),
        source,
      );

  /// Starts a fluent `/q/video` URL for [source].
  QueryVideoUrlChain video(String source) => QueryVideoUrlChain._(
        (s) {
          if (s.pipelines.where((p) => p.isNotEmpty).length > 1) {
            throw const UrlBuildException(
              ErrorCode.invalidRequest,
              '/q/video URLs have no chained pipelines',
            );
          }
          return _video(
            [for (final p in s.pipelines) ...p],
            s.source,
            s.extension,
          );
        },
        source,
      );

  List<(String, String)> _pairs(List<(String, Object?)> params) => [
        for (final (k, v) in params)
          if (v != null) (k, _nativeValue(k, v)),
      ];

  String _finish(String path, List<(String, String)> pairs) {
    final base = _trimSlashes(baseUrl);
    final m = _trimSlashes(mount);
    var query = pairs
        .map((p) => '${escapeQueryValue(p.$1)}=${escapeQueryValue(p.$2)}')
        .join('&');
    final key = signing;
    if (key != null) query = signXiQuery(path, query, key);
    return '$base$m$path?$query';
  }

  String _image(
    List<List<OptionEntry>> pipelines,
    String source,
    List<(String, Object?)> params,
    String? ext,
    String? pdfPassword,
  ) {
    final sep = _checkSeparator(separator, forbiddenQuerySeparators);
    final pairs = [
      ('url', checkText(source, ErrorCode.invalidSource, 'source')),
      ..._pairs(params),
    ];
    final nonEmpty = [
      for (final p in pipelines)
        if (p.isNotEmpty) p,
    ];
    if (nonEmpty.isNotEmpty) {
      final o = nonEmpty
          .map(
            (p) => p
                .map(
                  (e) => formatEntry(Catalogue.processing, e, sep, optionNames),
                )
                .join('/'),
          )
          .join('/$pipelineSeparator/');
      pairs.add(('o', o));
    }
    if (ext != null && ext.isNotEmpty) {
      pairs.add(('format', checkExtension(ext)));
    }
    if (pdfPassword != null) {
      final token = _passwordToken(encryption, signing, pdfPassword);
      pairs.add((
        optionNames == OptionNames.canonical
            ? pdfPasswordName
            : pdfPasswordShort,
        token,
      ));
    }
    return _finish('/', pairs);
  }

  String _video(List<OptionEntry> entries, String source, String? ext) {
    final pairs = [
      ('url', checkText(source, ErrorCode.invalidSource, 'source')),
    ];
    for (final (:name, :args) in entries) {
      var key = writtenName(Catalogue.video, name, optionNames);
      if (args.isEmpty) {
        throw UrlBuildException(
          ErrorCode.invalidArgument,
          'option $name: at least one argument is required',
        );
      }
      key = nativeVideoKeys[key] ?? key;
      final values = <String>[];
      for (var i = 0; i < args.length; i++) {
        final s = formatArg(args[i], ',', 'option $name argument ${i + 1}');
        if (s.contains(':')) {
          throw UrlBuildException(
            ErrorCode.invalidArgument,
            'option $name argument ${i + 1}: "$s" contains ":"',
          );
        }
        values.add(s);
      }
      pairs.add((key, values.join(',')));
    }
    if (ext != null && ext.isNotEmpty) {
      pairs.add(('format', checkExtension(ext)));
    }
    return _finish('/video', pairs);
  }
}

/// A fluent `/q/?url=…` URL: the option methods write `o=`, [param] a
/// native key.
final class QueryUrlChain extends UrlChain<QueryUrlChain, ProcessingOption>
    with
        _Extension<QueryUrlChain, ProcessingOption>,
        _Pipeline<QueryUrlChain, ProcessingOption>,
        _Password<QueryUrlChain, ProcessingOption>,
        ProcessingMethods<QueryUrlChain> {
  QueryUrlChain._(super._run, super.source) : super._();

  /// Appends a native key (`w`, `fit`, `g`, …), written before `o=`; a
  /// null value is skipped.
  QueryUrlChain param(String key, Object? value) {
    _s.params.add((key, value));
    return this;
  }
}

/// A fluent `/q/video` URL: one method per video option name and alias.
final class QueryVideoUrlChain extends UrlChain<QueryVideoUrlChain, VideoOption>
    with
        _Extension<QueryVideoUrlChain, VideoOption>,
        VideoMethods<QueryVideoUrlChain> {
  QueryVideoUrlChain._(super._run, super.source) : super._();
}
