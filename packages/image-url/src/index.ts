// @xtandard/image-url: a typed imgproxy-dialect URL builder for
// xtandard-image, with imgproxy and xi_sig signing and `enc/` source
// encryption on WebCrypto. No runtime dependencies. The fetch client is the
// "@xtandard/image-url/client" entry point.

export {
  buildImgproxyInfoUrl,
  buildImgproxyUrl,
  buildImgproxyVideoUrl,
  escapePlainSource,
  imgproxy,
  type ImgproxyInfoUrlChain,
  type ImgproxyInfoUrlRequest,
  type ImgproxyUrlBuilder,
  type ImgproxyUrlChain,
  type ImgproxyUrlConfig,
  type ImgproxyUrlRequest,
  type ImgproxyVideoUrlChain,
  type ImgproxyVideoUrlRequest,
  type SourceEncoding,
} from "./builder.ts";
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
