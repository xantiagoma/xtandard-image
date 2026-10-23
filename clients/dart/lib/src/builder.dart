/// The URL builders: the imgproxy dialect (processing, `/info`, `/video`,
/// HLS and DASH) and, in `query.dart`, the query dialect.
///
/// ```text
/// {baseUrl}[/info]/{signature}/{option}/…[/-/{option}/…]/{source}
/// {baseUrl}/video/{signature}/{video option}/…/{source}   (signs "/video" + path)
/// ```
///
/// Sources are base64url (default), `plain/` (percent-escaped, `@ext`) or
/// `enc/` (AES-CBC); base64 and enc sources take `.ext`.
library;

import 'encryption.dart';
import 'errors.dart';
import 'generated/constants.g.dart';
import 'generated/methods.g.dart';
import 'generated/options.g.dart';
import 'options.dart';
import 'signing.dart';
import 'text.dart';

part 'query.dart';

enum _Endpoint { process, info, video }

/// Rejects a separator that is not one character or that the grammar uses.
String _checkSeparator(String sep, List<String> forbidden) {
  if (sep.length != 1 || forbidden.contains(sep)) {
    throw UrlBuildException(
      ErrorCode.invalidSeparator,
      'invalid argument separator "$sep"',
    );
  }
  return sep;
}

String _trimSlashes(String s) {
  var end = s.length;
  while (end > 0 && s.codeUnitAt(end - 1) == 0x2f) {
    end--;
  }
  return s.substring(0, end);
}

String _split(String token, int? n) {
  if (n == null || n <= 0 || token.length <= n) return token;
  return [
    for (var i = 0; i < token.length; i += n)
      token.substring(i, i + n > token.length ? token.length : i + n),
  ].join('/');
}

/// The pdf_password token, checking that both keys are present.
String _passwordToken(
  EncryptionKey? encryption,
  SigningKey? signing,
  String password,
) {
  if (encryption == null) {
    throw const UrlBuildException(
      ErrorCode.missingEncryption,
      'pdf_password needs an encryption key',
    );
  }
  if (signing == null) {
    throw const UrlBuildException(
      ErrorCode.missingSigning,
      'pdf_password needs signing: the server refuses it on unsigned URLs',
    );
  }
  return encryptPdfPassword(password, encryption);
}

/// The video options and manifest container of a stream request.
(List<OptionEntry>, String) _streamParts(
  List<List<OptionEntry>> pipelines,
  StreamProtocol protocol,
  List<int>? renditions,
  int? segmentDuration,
  SegmentFormat? segmentFormat,
) {
  final entries = [for (final p in pipelines) ...p];
  for (final e in entries) {
    final canonical = Catalogue.video.names[e.name];
    if (const ['stream', 'segment_duration', 'segment_format', 'format']
        .contains(canonical)) {
      throw UrlBuildException(
        ErrorCode.invalidRequest,
        'stream requests set ${e.name} through protocol, renditions and the '
        'segment settings',
      );
    }
  }
  if (renditions != null && renditions.isNotEmpty) {
    entries.add((name: 'stream', args: [protocol.value, ...renditions]));
  }
  if (segmentDuration != null) {
    entries.add((name: 'segment_duration', args: [segmentDuration]));
  }
  if (segmentFormat != null) {
    if (!segmentFormat.protocols.contains(protocol)) {
      throw UrlBuildException(
        ErrorCode.invalidRequest,
        'segment format ${segmentFormat.value} is not available for '
        '${protocol.value}',
      );
    }
    if (segmentFormat != SegmentFormat.defaultFormat) {
      entries.add((name: 'segment_format', args: [segmentFormat.value]));
    }
  }
  return (entries, protocol.container);
}

/// Builds imgproxy-dialect URLs for an xtandard-image (or imgproxy) server.
///
/// ```dart
/// final img = ImgproxyUrlBuilder(
///   baseUrl: 'https://img.example.com',
///   signing: SigningKey(key: key, salt: salt),
/// );
/// img.url('s3://bucket/cat.jpg', options: {'rs': ['fill', 300, 400], 'q': 80}, extension: 'webp');
/// img.image('s3://bucket/cat.jpg').resize('fill', 300, 400).quality(80).extension('webp').build();
/// img.videoUrl('s3://bucket/talk.mov', options: {'vs': 12, 'vd': 8}, extension: 'webm');
/// img.streamUrl('s3://bucket/talk.mov', protocol: StreamProtocol.hls, renditions: [360, 720]);
/// ```
///
/// Every method throws [UrlBuildException] with an [ErrorCode] when the
/// input cannot make a URL.
final class ImgproxyUrlBuilder {
  /// Creates a builder.
  ///
  /// - [baseUrl]: prepended to every path: the server origin and the
  ///   dialect's mount prefix, if any. Default `''` (root-relative paths).
  /// - [signing]: signs URLs with the imgproxy scheme; without it the
  ///   signature segment is [unsignedSignature] (`insecure`, `unsafe` or
  ///   `_`).
  /// - [encryption]: the key of [SourceEncoding.encrypted] sources and PDF
  ///   passwords.
  /// - [sourceEncoding]: the default source encoding.
  /// - [splitSource]: splits base64 and encrypted sources into
  ///   `/`-separated chunks of this many characters.
  /// - [separator]: the argument separator
  ///   (`XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR`).
  /// - [optionNames]: how option names are written.
  const ImgproxyUrlBuilder({
    this.baseUrl = '',
    this.signing,
    this.unsignedSignature = defaultUnsigned,
    this.encryption,
    this.sourceEncoding = SourceEncoding.base64,
    this.splitSource,
    this.separator = defaultSeparator,
    this.optionNames = OptionNames.short,
  });

  /// Prepended to every path.
  final String baseUrl;

  /// The signing key, or null for unsigned URLs.
  final SigningKey? signing;

  /// The signature segment of unsigned URLs.
  final String unsignedSignature;

  /// The encryption key of encrypted sources and PDF passwords.
  final EncryptionKey? encryption;

  /// The default source encoding.
  final SourceEncoding sourceEncoding;

  /// The chunk size of split base64 and encrypted sources, or null.
  final int? splitSource;

  /// The argument separator.
  final String separator;

  /// How option names are written.
  final OptionNames optionNames;

  /// Builds a processing URL.
  ///
  /// [options] maps option names or aliases to their arguments, in URL
  /// order: a list, or the first argument alone (`{'rs': ['fill', 300,
  /// 400], 'q': 80}`); a null value skips the option, a null argument is
  /// empty. [pipelines] chains more pipelines (`/-/`) after [options].
  /// [extension] is the output format (`@webp` / `.webp`), [pdfPassword]
  /// the plain-text password of a protected PDF (needs [encryption] and
  /// [signing]).
  String url(
    String source, {
    Map<String, Object?>? options,
    List<Map<String, Object?>>? pipelines,
    String? extension,
    SourceEncoding? sourceEncoding,
    String? pdfPassword,
  }) =>
      _build(
        _Endpoint.process,
        pipelinesOf(options, pipelines),
        source,
        sourceEncoding,
        extension,
        pdfPassword,
      );

  /// Builds an `/info` URL.
  String infoUrl(
    String source, {
    Map<String, Object?>? options,
    SourceEncoding? sourceEncoding,
    String? pdfPassword,
  }) =>
      _build(
        _Endpoint.info,
        pipelinesOf(options, null),
        source,
        sourceEncoding,
        null,
        pdfPassword,
      );

  /// Builds a `/video` URL: a video transformation, audio or a waveform;
  /// [extension] is the container (see [videoContainers]).
  String videoUrl(
    String source, {
    Map<String, Object?>? options,
    String? extension,
    SourceEncoding? sourceEncoding,
  }) =>
      _build(
        _Endpoint.video,
        pipelinesOf(options, null),
        source,
        sourceEncoding,
        extension,
        null,
      );

  /// Builds an HLS or DASH manifest URL (`/video/…@m3u8` or `@mpd`).
  ///
  /// [renditions] are rungs by the short side of the frame (at most
  /// [maxRenditions]; default: the server's ladder), [segmentDuration] the
  /// segment length in whole seconds, [segmentFormat] `fmp4` (CMAF, the
  /// default) or `ts` (MPEG-TS, HLS only). [options] are other `/video`
  /// options (trim, crop, quality, audio, …) applied to every rendition.
  String streamUrl(
    String source, {
    required StreamProtocol protocol,
    List<int>? renditions,
    int? segmentDuration,
    SegmentFormat? segmentFormat,
    Map<String, Object?>? options,
    SourceEncoding? sourceEncoding,
  }) {
    final (entries, ext) = _streamParts(
      pipelinesOf(options, null),
      protocol,
      renditions,
      segmentDuration,
      segmentFormat,
    );
    return _build(
      _Endpoint.video,
      [entries],
      source,
      sourceEncoding,
      ext,
      null,
    );
  }

  /// Starts a fluent processing URL for [source].
  ImgproxyUrlChain image(String source) =>
      ImgproxyUrlChain._(_runner(_Endpoint.process), source);

  /// Starts a fluent `/info` URL for [source].
  ImgproxyInfoUrlChain info(String source) =>
      ImgproxyInfoUrlChain._(_runner(_Endpoint.info), source);

  /// Starts a fluent `/video` URL for [source].
  ImgproxyVideoUrlChain video(String source) =>
      ImgproxyVideoUrlChain._(_runner(_Endpoint.video), source);

  String Function(_ChainState) _runner(_Endpoint endpoint) => (s) => _build(
        endpoint,
        s.pipelines,
        s.source,
        s.encoding,
        s.extension,
        s.pdfPassword,
      );

  String _build(
    _Endpoint endpoint,
    List<List<OptionEntry>> pipelines,
    String source,
    SourceEncoding? encoding,
    String? ext,
    String? pdfPassword,
  ) {
    final sep = _checkSeparator(separator, forbiddenSeparators);
    final cat = switch (endpoint) {
      _Endpoint.info => Catalogue.info,
      _Endpoint.video => Catalogue.video,
      _Endpoint.process => Catalogue.processing,
    };
    final nonEmpty = [
      for (final p in pipelines)
        if (p.isNotEmpty) p,
    ];
    if (endpoint != _Endpoint.process && nonEmpty.length > 1) {
      throw UrlBuildException(
        ErrorCode.invalidRequest,
        '/${endpoint.name} URLs have no chained pipelines',
      );
    }
    if (endpoint == _Endpoint.video && pdfPassword != null) {
      throw const UrlBuildException(
        ErrorCode.invalidRequest,
        '/video URLs take no pdf_password',
      );
    }
    final segments = <String>[];
    if (pdfPassword != null) {
      final token = _passwordToken(encryption, signing, pdfPassword);
      final name = optionNames == OptionNames.canonical
          ? pdfPasswordName
          : pdfPasswordShort;
      segments.add('$name$sep$token');
    }
    for (var i = 0; i < nonEmpty.length; i++) {
      if (i > 0) segments.add(pipelineSeparator);
      for (final e in nonEmpty[i]) {
        segments.add(formatEntry(cat, e, sep, optionNames));
      }
    }
    segments.add(_sourceSegment(source, encoding ?? sourceEncoding, ext));
    return _finish(endpoint, '/${segments.join('/')}');
  }

  String _sourceSegment(String source, SourceEncoding encoding, String? ext) {
    checkText(source, ErrorCode.invalidSource, 'source');
    final e = ext == null || ext.isEmpty ? null : checkExtension(ext);
    switch (encoding) {
      case SourceEncoding.plain:
        return 'plain/${escapePlainSource(source)}${e == null ? '' : '@$e'}';
      case SourceEncoding.base64:
        return '${_split(b64url(utf8Bytes(source)), splitSource)}'
            '${e == null ? '' : '.$e'}';
      case SourceEncoding.encrypted:
        final key = encryption;
        if (key == null) {
          throw const UrlBuildException(
            ErrorCode.missingEncryption,
            'encrypted sources need an encryption key',
          );
        }
        final token = encryptSource(source, key);
        return '$encryptedSourcePrefix${_split(token, splitSource)}'
            '${e == null ? '' : '.$e'}';
    }
  }

  String _finish(_Endpoint endpoint, String path) {
    final base = _trimSlashes(baseUrl);
    final key = signing;
    if (endpoint == _Endpoint.video) {
      final sig = key == null ? unsignedSignature : sign('/video$path', key);
      return '$base/video/$sig$path';
    }
    final signed =
        key == null ? '/$unsignedSignature$path' : signImgproxyPath(path, key);
    return '$base${endpoint == _Endpoint.info ? '/info' : ''}$signed';
  }
}

// ---- fluent chains ------------------------------------------------------------

final class _ChainState {
  _ChainState(this.source);

  final String source;
  final List<List<OptionEntry>> pipelines = [[]];
  final List<(String, Object?)> params = [];
  SourceEncoding? encoding;
  String? extension;
  String? pdfPassword;
}

/// The shared state and control methods of the fluent URL builders.
///
/// A chain is mutable: each method appends to it and returns it, so calls
/// chain (`.resize('fit', 300, 300).quality(80)`) and cascade
/// (`..quality(80)`). [build] (or [toString]) makes the URL and throws
/// [UrlBuildException] when it cannot.
abstract base class UrlChain<T extends UrlChain<T, O>, O extends OptionValue> {
  UrlChain._(this._run, String source) : _s = _ChainState(source);

  final String Function(_ChainState) _run;
  final _ChainState _s;

  T get _self => this as T;

  /// Appends one option occurrence by name or alias (as the per-option
  /// methods do).
  T option(String name, List<Object?> args) {
    _s.pipelines.last.add((name: name, args: List.unmodifiable(args)));
    return _self;
  }

  /// Appends every option of an options map, in key order.
  T options(Map<String, Object?> options) {
    _s.pipelines.last.addAll(entriesOf(options));
    return _self;
  }

  /// Appends a typed option (`ProcessingOption.resize('fit', 300, 300)`).
  T add(O option) => this.option(option.name, option.args);

  /// Appends typed options in order.
  T addAll(Iterable<O> options) {
    for (final o in options) {
      add(o);
    }
    return _self;
  }

  /// Builds the URL.
  String build() => _run(_s);

  /// The URL; throws like [build].
  @override
  String toString() => build();
}

/// `encoding()`: the source encoding of one URL.
base mixin _Encoding<T extends UrlChain<T, O>, O extends OptionValue>
    on UrlChain<T, O> {
  /// Overrides the builder's source encoding for this URL.
  T encoding(SourceEncoding encoding) {
    _s.encoding = encoding;
    return _self;
  }
}

/// `extension()`: the output format or container.
base mixin _Extension<T extends UrlChain<T, O>, O extends OptionValue>
    on UrlChain<T, O> {
  /// Sets the output extension (`@webp` / `.webp`; a container for
  /// `/video`).
  T extension(String extension) {
    _s.extension = extension;
    return _self;
  }
}

/// `pipeline()`: chained pipelines.
base mixin _Pipeline<T extends UrlChain<T, O>, O extends OptionValue>
    on UrlChain<T, O> {
  /// Starts the next chained pipeline (`/-/`).
  T pipeline() {
    _s.pipelines.add([]);
    return _self;
  }
}

/// `withPdfPassword()`: a password-protected PDF source.
base mixin _Password<T extends UrlChain<T, O>, O extends OptionValue>
    on UrlChain<T, O> {
  /// Opens a password-protected PDF: the plain-text password is encrypted
  /// into `pdp` (needs both keys).
  T withPdfPassword(String password) {
    _s.pdfPassword = password;
    return _self;
  }
}

/// A fluent processing URL: one method per option name and alias,
/// [pipeline] for `/-/`.
final class ImgproxyUrlChain
    extends UrlChain<ImgproxyUrlChain, ProcessingOption>
    with
        _Encoding<ImgproxyUrlChain, ProcessingOption>,
        _Extension<ImgproxyUrlChain, ProcessingOption>,
        _Pipeline<ImgproxyUrlChain, ProcessingOption>,
        _Password<ImgproxyUrlChain, ProcessingOption>,
        ProcessingMethods<ImgproxyUrlChain> {
  ImgproxyUrlChain._(super._run, super.source) : super._();
}

/// A fluent `/info` URL.
final class ImgproxyInfoUrlChain
    extends UrlChain<ImgproxyInfoUrlChain, InfoOption>
    with
        _Encoding<ImgproxyInfoUrlChain, InfoOption>,
        _Password<ImgproxyInfoUrlChain, InfoOption>,
        InfoMethods<ImgproxyInfoUrlChain> {
  ImgproxyInfoUrlChain._(super._run, super.source) : super._();
}

/// A fluent `/video` URL: video transformations, audio and waveforms.
final class ImgproxyVideoUrlChain
    extends UrlChain<ImgproxyVideoUrlChain, VideoOption>
    with
        _Encoding<ImgproxyVideoUrlChain, VideoOption>,
        _Extension<ImgproxyVideoUrlChain, VideoOption>,
        VideoMethods<ImgproxyVideoUrlChain> {
  ImgproxyVideoUrlChain._(super._run, super.source) : super._();
}
