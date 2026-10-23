# frozen_string_literal: true

require "json"
require "net/http"
require "openssl"
require "uri"

require_relative "xtandard_image/version"
require_relative "xtandard_image/generated/constants"
require_relative "xtandard_image/generated/catalogues"
require_relative "xtandard_image/generated/methods"
require_relative "xtandard_image/errors"
require_relative "xtandard_image/text"
require_relative "xtandard_image/options"
require_relative "xtandard_image/signing"
require_relative "xtandard_image/encryption"
require_relative "xtandard_image/sources"
require_relative "xtandard_image/chain"
require_relative "xtandard_image/builder"
require_relative "xtandard_image/query"
require_relative "xtandard_image/client"

# The official Ruby client of xtandard-image.
#
# URL builders for the imgproxy dialect (processing, /info, /video, HLS/DASH
# streams) and the query dialect (/q/, /q/video, imaginary endpoints),
# imgproxy and xi_sig signing, enc/ source and PDF password encryption, and
# an HTTP client on Net::HTTP. Build and sign URLs on a server: the keys
# sign and decrypt everything.
#
#   require "xtandard_image"
#
#   key = XtandardImage::SigningKey.new(key: ENV.fetch("XI_KEY"), salt: ENV.fetch("XI_SALT"))
#   img = XtandardImage.imgproxy(base_url: "https://img.example.com", signing: key)
#   img.url("s3://bucket/cat.jpg", { rs: ["fill", 300, 400], q: 80 }, extension: "webp")
module XtandardImage
  class << self
    # An ImgproxyURLBuilder (named like the TypeScript client's factory).
    def imgproxy(**) = ImgproxyURLBuilder.new(**)

    # A QueryURLBuilder (named like the TypeScript client's factory).
    def query_urls(**) = QueryURLBuilder.new(**)

    # See Signing.sign.
    def sign(message, key) = Signing.sign(message, key)

    # See Signing.sign_imgproxy_path.
    def sign_imgproxy_path(path, key) = Signing.sign_imgproxy_path(path, key)

    # See Signing.xi_message.
    def xi_message(path, raw_query) = Signing.xi_message(path, raw_query)

    # See Signing.sign_xi_query.
    def sign_xi_query(path, raw_query, key) = Signing.sign_xi_query(path, raw_query, key)

    # See Signing.sign_xi_url.
    def sign_xi_url(url, key, mount: "") = Signing.sign_xi_url(url, key, mount: mount)

    # See Encryption.encrypt_source.
    def encrypt_source(source, key) = Encryption.encrypt_source(source, key)

    # See Encryption.encrypt_pdf_password.
    def encrypt_pdf_password(password, key) = Encryption.encrypt_pdf_password(password, key)

    # See Sources.xi_source.
    def xi_source(name, key) = Sources.xi_source(name, key)

    # See Sources.source_url.
    def source_url(scheme, authority, key) = Sources.source_url(scheme, authority, key)

    # See Text.escape_plain_source.
    def escape_plain_source(source) = Text.escape_plain_source(source)

    # See Text.escape_query_value.
    def escape_query_value(value) = Text.escape_query_value(value)

    # See QueryMethodRequest.from_url.
    def query_method_request(url) = QueryMethodRequest.from_url(url)
  end
end
