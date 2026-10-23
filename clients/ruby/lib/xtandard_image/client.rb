# frozen_string_literal: true

# The official Ruby client of xtandard-image (see lib/xtandard_image.rb).
module XtandardImage
  # The last +length+ bytes of a resource (Range: bytes=-length).
  ByteSuffix = Data.define(:length)

  # The /readyz answer: +ready+ is true only for 200 "ready"; +state+ is
  # "ready", "starting" or "draining".
  Readiness = Data.define(:ready, :state)

  # ready? reads like a predicate.
  class Readiness
    alias ready? ready
  end

  # A fetched HLS playlist or DASH MPD. +url+ is the (signed) manifest URL to
  # hand to a player; the child URLs in +body+ (/_stream/<ticket>/…) carry a
  # ticket and need no signing.
  StreamManifest = Data.define(:url, :content_type, :body)

  # A media response: +status+ (200, or 206 for a range), +headers+ (lower-case
  # names), +body+ (a binary String; nil for HEAD and when a block streamed it)
  # and the request +url+.
  Response = Data.define(:status, :headers, :body, :url)

  # Header access by name.
  class Response
    # A header value by case-insensitive name.
    def [](name) = headers[name.to_s.downcase]

    # The Content-Type header.
    def content_type = headers["content-type"]

    def inspect = "#<data #{self.class.name} status=#{status} url=#{url.inspect} content_type=#{content_type.inspect}>"
    alias to_s inspect
  end

  # Formats a byte range as a Range header value: a String is used as is;
  # a Range (0..1023, 0...1024, 500..), a ByteSuffix (the last n bytes) or a
  # Hash ({start:, end:} or {suffix:}) is written bytes=0-1023, bytes=500-,
  # bytes=-500.
  #
  # Raises URLBuildError (:invalid_range) for a negative value or an end
  # before the start.
  def self.range_header(range)
    RangeHeader.format(range)
  end

  # @api private
  module RangeHeader
    module_function

    def format(range)
      case range
      when String then range
      when ByteSuffix then "bytes=-#{non_negative(range.length, "suffix")}"
      when Range then from_range(range)
      when Hash then from_hash(range)
      else raise URLBuildError.new(:invalid_range, "unsupported range #{range.inspect}")
      end
    end

    def from_range(range)
      start = range.begin.nil? ? 0 : non_negative(range.begin, "start")
      last = range.end
      return "bytes=#{start}-" if last.nil?

      bytes(start, non_negative(last, "end") - (range.exclude_end? ? 1 : 0))
    end

    def from_hash(hash)
      h = hash.transform_keys(&:to_s)
      return "bytes=-#{non_negative(h["suffix"], "suffix")}" if h.key?("suffix")

      start = non_negative(h["start"], "start")
      h["end"].nil? ? "bytes=#{start}-" : bytes(start, non_negative(h["end"], "end"))
    end

    def bytes(start, last)
      raise URLBuildError.new(:invalid_range, "range end #{last} is before start #{start}") if last < start

      "bytes=#{start}-#{last}"
    end

    def non_negative(value, what)
      unless value.is_a?(Integer) && value >= 0
        raise URLBuildError.new(:invalid_range,
                                "range #{what} #{value.inspect} is not a non-negative integer",)
      end

      value
    end
  end

  # An HTTP client on Net::HTTP for the JSON and health endpoints (/info,
  # /healthz, /readyz, the playground's api/meta) and media fetches (images,
  # /video results with Range and HEAD, HLS and DASH manifests). URLs are
  # built (and signed) by an ImgproxyURLBuilder (#urls). Non-2xx responses
  # raise an HTTPError subclass chosen by status; transport failures raise
  # TransportError.
  #
  #   xi = XtandardImage::Client.new("http://localhost:8080", signing: key)
  #   info = xi.info("s3://bucket/cat.jpg", { size: true, format: true })
  #   head = xi.video("s3://bucket/talk.mov", { vd: 5 }, extension: "webm", method: :head)
  #   File.open("clip.webm", "wb") do |f|
  #     xi.video("s3://bucket/talk.mov", { vd: 5 }, extension: "webm") { |chunk| f.write(chunk) }
  #   end
  #
  # HTTP QUERY (RFC 10008) is opt-in: <tt>media(url, method: :query)</tt>
  # sends one query dialect URL as a QUERY request (QueryMethodRequest), and
  # <tt>query_method: true</tt> does so for every #media URL under
  # +query_mount+ when no method is given. Off by default: the server needs
  # XI_SERVER_QUERY_METHOD (on by default), and many CDNs and proxies
  # neither cache nor forward QUERY.
  #
  # Each request opens its own connection, so a Client is safe to share
  # between threads.
  class Client
    # Net::HTTP has no request class for QUERY (RFC 10008): safe, with content.
    class QueryRequest < Net::HTTPRequest
      METHOD = "QUERY"
      REQUEST_HAS_BODY = true
      RESPONSE_HAS_BODY = true
    end

    TRANSPORT_ERRORS = [
      IOError, SystemCallError, SocketError, Timeout::Error, OpenSSL::SSL::SSLError, Net::HTTPBadResponse,
      Net::ProtocolError, Zlib::Error,
    ].freeze
    SCHEME = /\A[a-zA-Z][a-zA-Z0-9+.-]*:/
    ORIGIN = %r{\A[a-zA-Z][a-zA-Z0-9+.-]*://[^/?#]*}
    READY_STATES = %w[ready starting draining].freeze
    private_constant :TRANSPORT_ERRORS, :SCHEME, :ORIGIN, :READY_STATES, :QueryRequest

    # The server origin (plus the imgproxy mount prefix, if any), without a trailing "/".
    attr_reader :base_url
    # The ImgproxyURLBuilder this client builds URLs with.
    attr_reader :urls

    # base_url:: Server origin, plus the imgproxy mount prefix if any.
    # signing, encryption, unsigned_signature, source_encoding, split_source,
    # separator, option_names:: As for ImgproxyURLBuilder.
    # headers:: Headers sent with every request.
    # open_timeout, read_timeout, write_timeout:: Net::HTTP timeouts in seconds.
    # playground_base_url:: Origin of the admin listener for #playground_meta; default +base_url+.
    # playground_path:: XI_PLAYGROUND_PATH. Default "/_playground/".
    # query_method:: Send #media URLs under +query_mount+ with HTTP QUERY when no method is given. Default false.
    # query_mount:: The query dialect's mount (XI_DIALECTS_QUERY_PREFIX), for +query_method+. Default "/q".
    def initialize(base_url, signing: nil, encryption: nil, unsigned_signature: DEFAULT_UNSIGNED,
                   source_encoding: :base64, split_source: nil, separator: DEFAULT_SEPARATOR, option_names: :short,
                   headers: {}, open_timeout: 10, read_timeout: 60, write_timeout: 60, playground_base_url: nil,
                   playground_path: "/_playground/", query_method: false, query_mount: QUERY_MOUNT)
      @base_url = URLs.trim_slashes(base_url)
      @urls = ImgproxyURLBuilder.new(
        base_url: @base_url, signing: signing, unsigned_signature: unsigned_signature, encryption: encryption,
        source_encoding: source_encoding, split_source: split_source, separator: separator, option_names: option_names,
      )
      @headers = headers.to_h { |k, v| [k.to_s, v.to_s] }.freeze
      @open_timeout = open_timeout
      @read_timeout = read_timeout
      @write_timeout = write_timeout
      origin = URLs.trim_slashes(playground_base_url || @base_url)
      @playground_meta = "#{origin}/#{playground_path.gsub(%r{\A/+|/+\z}, "")}/api/meta"
      @query_method = query_method
      @query_mount = URLs.trim_slashes(query_mount)
    end

    # GET /healthz: returns nil while the process can serve HTTP.
    def healthz
      fetch(:get, "#{base_url}/healthz", request_headers("text/plain", nil, nil))
      nil
    end

    # GET /readyz: the readiness state; 503 (starting, draining) is not an error.
    def readyz
      url = "#{base_url}/readyz"
      status, _, body = perform(:get, url, request_headers("text/plain", nil, nil))
      raise http_error(status, url, body) unless [200, 503].include?(status)

      state = body.to_s.strip
      raise HTTPError.new(status, url, "unexpected readiness state #{state.inspect}") unless READY_STATES.include?(state)

      Readiness.new(ready: status == 200 && state == "ready", state: state)
    end

    # GET /info for a source, with the URL built (and signed) from the
    # arguments; the parsed JSON (an info_response Hash with String keys).
    def info(source, options = nil, source_encoding: nil, pdf_password: nil)
      json(urls.info_url(source, options, source_encoding: source_encoding, pdf_password: pdf_password))
    end

    # GET a prebuilt /info/… path or URL.
    def info_at(url) = json(absolute(url))

    # GET (or HEAD) any URL or server path: an image or video URL, a query
    # URL, a stream part. +range+ asks for part of it (206; see
    # XtandardImage.range_header). With a block, the body is streamed to it
    # in chunks and the Response has no body. +method+ :query sends a query
    # dialect URL's query as the request content (RFC 10008); with no
    # +method+, the client's +query_method+ picks QUERY for URLs under its
    # +query_mount+ and GET for the rest.
    def media(url, method: nil, range: nil, accept: "*/*", headers: nil, &)
      abs = absolute(url)
      h = request_headers(accept, range, headers)
      return fetch(method || :get, abs, h, &) unless query?(method, abs)

      q = QueryMethodRequest.from_url(abs)
      h["Content-Type"] = q.content_type
      fetch(:query, q.url, h, q.body, &)
    end

    # GET (or HEAD) a processed image; +accept+ drives format negotiation.
    def image(source, options = nil, extension: nil, source_encoding: nil, pdf_password: nil, method: :get, range: nil,
              accept: "*/*", headers: nil, &)
      url = urls.url(source, options, extension: extension, source_encoding: source_encoding, pdf_password: pdf_password)
      media(url, method: method, range: range, accept: accept, headers: headers, &)
    end

    # GET (or HEAD) a /video result: video, audio or a waveform; +range+
    # asks for part of it (206).
    def video(source, options = nil, extension: nil, source_encoding: nil, method: :get, range: nil, accept: "*/*",
              headers: nil, &)
      url = urls.video_url(source, options, extension: extension, source_encoding: source_encoding)
      media(url, method: method, range: range, accept: accept, headers: headers, &)
    end

    # GET an HLS or DASH manifest (/video/…@m3u8 or @mpd).
    def stream(source, protocol:, renditions: nil, segment_duration: nil, segment_format: nil, options: nil,
               source_encoding: nil, headers: nil)
      url = urls.stream_url(source, protocol: protocol, renditions: renditions, segment_duration: segment_duration,
                                    segment_format: segment_format, options: options, source_encoding: source_encoding,)
      ctype = STREAM_MANIFESTS.fetch(protocol.to_s, ["", "application/octet-stream"]).fetch(1)
      res = fetch(:get, url, request_headers("#{ctype}, */*;q=0.5", nil, headers))
      StreamManifest.new(url: url, content_type: res["content-type"] || ctype,
                         body: res.body.to_s.dup.force_encoding(Encoding::UTF_8),)
    end

    # GET the playground's api/meta (needs XI_PLAYGROUND_ENABLED; admin listener by default).
    def playground_meta = json(@playground_meta)

    def inspect = "#<#{self.class.name} #{base_url}>"

    private

    def query?(method, url)
      return method.to_s.casecmp?("query") unless method.nil?
      return false unless @query_method

      path = url.split(/[?#]/, 2).first.to_s.sub(ORIGIN, "")
      path == @query_mount || path.start_with?("#{@query_mount}/")
    end

    def absolute(url)
      return url if SCHEME.match?(url)

      "#{base_url}#{"/" unless url.start_with?("/")}#{url}"
    end

    def request_headers(accept, range, headers)
      h = @headers.dup
      headers&.each { |k, v| h[k.to_s] = v.to_s }
      h["Accept"] = accept
      h["Range"] = XtandardImage.range_header(range) unless range.nil?
      h
    end

    def json(url)
      JSON.parse(fetch(:get, url, request_headers("application/json", nil, nil)).body.to_s)
    end

    # Sends a request and raises HTTPError unless it is 2xx.
    def fetch(method, url, headers, content = nil, &)
      status, res_headers, body = perform(method, url, headers, content, &)
      raise http_error(status, url, body) unless (200..299).cover?(status)

      Response.new(status: status, headers: res_headers, body: body, url: url)
    end

    # [status, headers, body]: the body is nil for HEAD and when a block
    # took a 2xx body; error bodies are always read.
    def perform(method, url, headers, content = nil, &block)
      uri = URI.parse(url)
      host = uri.host
      raise ArgumentError, "#{url.inspect} is not an absolute http(s) URL" if host.nil?

      req = request_class(method).new(uri, headers)
      req.body = content unless content.nil?
      # @type var user_error: Exception?
      user_error = nil
      begin
        Net::HTTP.start(host, uri.port, use_ssl: uri.scheme == "https", open_timeout: @open_timeout,
                                        read_timeout: @read_timeout, write_timeout: @write_timeout,) do |http|
          http.request(req) do |res|
            status = res.code.to_i
            if block && (200..299).cover?(status)
              res.read_body do |chunk|
                block.call(chunk)
              rescue Exception => e # rubocop:disable Lint/RescueException -- re-raised untouched below
                user_error = e
                raise
              end
              return [status, res_headers(res), nil]
            end
            return [status, res_headers(res), res.body]
          end
        end
      rescue *TRANSPORT_ERRORS => e
        raise user_error if user_error

        raise TransportError, "#{method.to_s.upcase} #{url}: #{e.message}"
      end
      raise TransportError, "#{method.to_s.upcase} #{url}: no response"
    end

    def request_class(method)
      case method.to_s.downcase
      when "get" then Net::HTTP::Get
      when "head" then Net::HTTP::Head
      when "query" then QueryRequest
      else raise ArgumentError, "method must be :get, :head or :query, not #{method.inspect}"
      end
    end

    def res_headers(res)
      res.each_header.to_h.freeze
    end

    def http_error(status, url, body)
      text = body.to_s.dup.force_encoding(Encoding::UTF_8).scrub
      envelope = parse_envelope(text)
      message = envelope ? envelope.fetch("error").fetch("message") : text.strip
      message = "HTTP #{status}" if message.empty?
      XtandardImage.http_error_class(status).new(status, url, message, envelope)
    end

    def parse_envelope(text)
      v = JSON.parse(text)
      err = v.is_a?(Hash) ? v["error"] : nil
      err.is_a?(Hash) && err["kind"].is_a?(String) && err["message"].is_a?(String) ? v : nil
    rescue JSON::ParserError
      nil
    end
  end
end
