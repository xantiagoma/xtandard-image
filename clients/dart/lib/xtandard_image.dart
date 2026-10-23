/// The official Dart client of xtandard-image: typed URL builders for the
/// imgproxy dialect (processing, `/info`, `/video`, HLS and DASH) and the
/// query dialect, imgproxy and `xi_sig` signing, `enc/` source and PDF
/// password encryption, and an HTTP client on `package:http`.
///
/// Pure Dart: it runs in Flutter apps (iOS, Android, web, desktop) and on
/// servers (shelf, dart_frog, serverpod).
///
/// ```dart
/// import 'package:xtandard_image/xtandard_image.dart';
///
/// final img = ImgproxyUrlBuilder(baseUrl: 'https://img.example.com');
/// final url = img.image('s3://photos/cat.jpg')
///     .resize('fill', 600, 400)
///     .quality(80)
///     .extension('webp')
///     .build();
/// ```
///
/// Signing and encryption keys sign and decrypt everything. Keep them on a
/// server; see the README for apps.
library;

export 'src/builder.dart'
    show
        ImgproxyInfoUrlChain,
        ImgproxyUrlBuilder,
        ImgproxyUrlChain,
        ImgproxyVideoUrlChain,
        QueryUrlBuilder,
        QueryUrlChain,
        QueryVideoUrlChain,
        UrlChain;
export 'src/client.dart';
export 'src/encryption.dart';
export 'src/errors.dart';
export 'src/generated/catalogue.g.dart';
export 'src/generated/constants.g.dart';
export 'src/generated/methods.g.dart';
export 'src/generated/options.g.dart';
export 'src/generated/responses.g.dart';
export 'src/options.dart' show Catalogue, OptionEntry, OptionNames;
export 'src/signing.dart';
export 'src/sources.dart';
export 'src/text.dart' show escapePlainSource, escapeQueryValue, formatNumber;
