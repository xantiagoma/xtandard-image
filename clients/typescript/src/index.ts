// @xtandard/image: a typed imgproxy-dialect URL builder for
// xtandard-image, with imgproxy and xi_sig signing and `enc/` source
// encryption on WebCrypto. No runtime dependencies. The fetch client is the
// "@xtandard/image/client" entry point.

export {
  buildImaginaryUrl,
  buildImgproxyInfoUrl,
  buildImgproxyStreamUrl,
  buildImgproxyUrl,
  buildImgproxyVideoUrl,
  buildQueryStreamUrl,
  buildQueryUrl,
  buildQueryVideoUrl,
  escapePlainSource,
  type ImaginaryUrlRequest,
  imgproxy,
  type ImgproxyStreamUrlRequest,
  type ImgproxyInfoUrlChain,
  type ImgproxyInfoUrlRequest,
  type ImgproxyUrlBuilder,
  type ImgproxyUrlChain,
  type ImgproxyUrlConfig,
  type ImgproxyUrlRequest,
  type ImgproxyVideoUrlChain,
  type ImgproxyVideoUrlRequest,
  type QueryStreamUrlRequest,
  type QueryUrlBuilder,
  type QueryUrlChain,
  type QueryUrlRequest,
  type QueryVideoUrlChain,
  type QueryVideoUrlRequest,
  queryUrls,
  type SourceEncoding,
  type StreamProtocol,
  type VideoContainer,
} from "./builder.ts";
export {
  escapeQueryValue,
  type ImaginaryEndpoint,
  type ImaginaryParams,
  type ParamList,
  type QueryImageParams,
  type QueryUrlConfig,
  type QueryValue,
} from "./query.ts";
export { type SourceScheme, sourceUrl, xiSource } from "./source.ts";
export type { KeyMaterial } from "./bytes.ts";
export { type EncryptionKey, encryptPdfPassword, encryptSource, MAX_PDF_PASSWORD_BYTES } from "./encrypt.ts";
export {
  imgproxyInfoAliases,
  imgproxyInfoOptionNames,
  imgproxyProcessingAliases,
  imgproxyProcessingOptionNames,
  imgproxyVideoAliases,
  imgproxyVideoOptionNames,
} from "./generated/options.ts";
export type {
  Arg,
  ImgproxyInfoArgs,
  ImgproxyInfoArgsOf,
  ImgproxyInfoOptionName,
  ImgproxyInfoOptions,
  ImgproxyProcessingArgs,
  ImgproxyProcessingArgsOf,
  ImgproxyProcessingOptionName,
  ImgproxyProcessingOptions,
  ImgproxyVideoArgs,
  ImgproxyVideoArgsOf,
  ImgproxyVideoOptionName,
  ImgproxyVideoOptions,
  OptionNameStyle,
  OptionValue,
} from "./options.ts";
export { sign, type SigningKey, signImgproxyPath, signXiQuery, signXiUrl, XI_SIG_PARAM, xiMessage } from "./sign.ts";
