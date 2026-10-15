# frozen_string_literal: true

# The official Ruby client of xtandard-image (see lib/xtandard_image.rb).
module XtandardImage
  # Base class of every exception this gem raises.
  class Error < StandardError; end

  # A URL, token or header cannot be built from the given input.
  #
  # +code+ is the language-neutral error code of clients/spec/conformance.json
  # (see clients/README.md), as a Symbol: +:unknown_option+,
  # +:invalid_argument+, +:invalid_source+, +:invalid_extension+,
  # +:invalid_separator+, +:missing_signing+, +:missing_encryption+,
  # +:invalid_key+, +:invalid_password+, +:invalid_request+, +:invalid_name+,
  # +:invalid_endpoint+ or +:invalid_range+.
  class URLBuildError < Error
    # Every code, in the order of clients/README.md.
    CODES = %i[
      unknown_option invalid_argument invalid_source invalid_extension invalid_separator
      missing_signing missing_encryption invalid_key invalid_password invalid_request
      invalid_name invalid_endpoint invalid_range
    ].freeze

    # The error code (a member of CODES).
    attr_reader :code

    def initialize(code, message)
      super(message)
      @code = code
    end
  end

  # The request did not get a response (connection, TLS, timeout); +cause+ is
  # the underlying exception.
  class TransportError < Error; end

  # A non-2xx response.
  #
  # +kind+, +message+ and +request_id+ come from the server's JSON error
  # envelope; a body that is not the envelope (a proxy's error page, say)
  # yields kind "unknown" and the body text.
  class HTTPError < Error
    # The HTTP status.
    attr_reader :status
    # The request URL.
    attr_reader :url
    # The parsed envelope ({"error" => {"kind", "message", "request_id"}}), when the body was one.
    attr_reader :body
    # The error kind (invalid_url, unauthorized, not_found, …) or "unknown".
    attr_reader :kind
    # The request ID from the envelope, also in the server logs.
    attr_reader :request_id

    def initialize(status, url, message, body = nil)
      super(message)
      @status = status
      @url = url
      @body = body
      error = body&.fetch("error")
      @kind = error ? error.fetch("kind") : "unknown"
      @request_id = error&.[]("request_id")
    end
  end

  # 400: the URL is invalid (kind invalid_url).
  class BadRequestError < HTTPError; end

  # 403: a bad or missing signature (unauthorized) or a refused source or option (forbidden).
  class ForbiddenError < HTTPError; end

  # 404: the source does not exist (not_found) or the URL expired (expired).
  class NotFoundError < HTTPError; end

  # 406: no acceptable output format.
  class NotAcceptableError < HTTPError; end

  # 422: the source is too large, unsupported, corrupt or over a processing limit.
  class UnprocessableError < HTTPError; end

  # 429: the server is overloaded; retry later.
  class TooManyRequestsError < HTTPError; end

  # 5xx: the server, an upstream source or a timeout failed.
  class ServerError < HTTPError; end

  # 503: the server is unavailable or draining.
  class ServiceUnavailableError < ServerError; end

  # 502 and 504: an upstream error or timeout.
  class GatewayError < ServerError; end

  # HTTP status to its exception class (others: ServerError for 5xx, HTTPError otherwise).
  HTTP_ERROR_CLASSES = {
    400 => BadRequestError,
    403 => ForbiddenError,
    404 => NotFoundError,
    406 => NotAcceptableError,
    422 => UnprocessableError,
    429 => TooManyRequestsError,
    502 => GatewayError,
    503 => ServiceUnavailableError,
    504 => GatewayError,
  }.freeze

  # The exception class of an HTTP status.
  def self.http_error_class(status)
    HTTP_ERROR_CLASSES.fetch(status) { status >= 500 ? ServerError : HTTPError }
  end
end
