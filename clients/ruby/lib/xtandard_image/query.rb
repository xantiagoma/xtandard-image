# frozen_string_literal: true

module XtandardImage
  # Settings of query dialect URLs (see QueryURLBuilder#initialize).
  QueryConfig = Data.define(:base_url, :mount, :signing, :encryption, :separator, :option_names)

  # Hides the keys from inspect.
  class QueryConfig
    def inspect = "#<data #{self.class.name} base_url=#{base_url.inspect} mount=#{mount.inspect} signed=#{!signing.nil?}>"
    alias to_s inspect
  end

  # A fluent /q/?url=… URL: the option methods write o=, +param+ a native key.
  class QueryURLChain < Chain
    include WithExtension
    include WithPipeline
    include WithPassword
    include ProcessingMethods

    # Appends a native key (w, fit, g, …), written before o=.
    def param(key, value)
      chain_state.params << [key, value]
      self
    end
  end

  # A fluent /q/video URL: one method per video option name and alias.
  class QueryVideoURLChain < Chain
    include WithExtension
    include VideoMethods
  end

  # Builds query dialect URLs (docs/URL-DIALECTS.md §4), mounted at /q/; the
  # server needs XI_DIALECTS_QUERY_ENABLED=true.
  #
  #   {base_url}{mount}/?url=<source>&<native key>=…&o=<imgproxy option path>[&xi_sig=…]
  #   {base_url}{mount}/video?url=<source>&<video option>=<arg,arg>…[&xi_sig=…]
  #   {base_url}{mount}/<imaginary endpoint>?url=<source>&<param>=…[&xi_sig=…]
  #
  # With +signing+, every URL ends in an xi_sig over the path after the mount
  # and the query:
  #
  #   q = XtandardImage::QueryURLBuilder.new(base_url: "https://img.example.com", signing: key)
  #   q.url("s3://bucket/cat.jpg", params: { w: 300, f: "webp" }, options: { bl: 2 })
  #   # https://img.example.com/q/?url=s3://bucket/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=…
  class QueryURLBuilder
    # The settings (a QueryConfig).
    attr_reader :config

    # base_url:: Server origin (and any path before the mount). Default "".
    # mount:: The query dialect's mount prefix (XI_DIALECTS_QUERY_PREFIX). Default "/q".
    # signing:: Signs with xi_sig (the imgproxy key and salt); without it URLs are unsigned.
    # encryption:: Key for PDF passwords (XI_ENCRYPTION_KEYS).
    # separator:: Argument separator inside o=.
    # option_names:: :short (default), :canonical or :given.
    def initialize(base_url: "", mount: QUERY_MOUNT, signing: nil, encryption: nil, separator: DEFAULT_SEPARATOR,
                   option_names: :short)
      @config = QueryConfig.new(
        base_url: base_url, mount: mount, signing: signing, encryption: encryption, separator: separator,
        option_names: Options.name_style(option_names),
      )
    end

    # Builds a native /q/?url=… URL: native keys (a Hash, or [key, value]
    # pairs whose keys may repeat), then imgproxy options as o=.
    def url(source, params: nil, options: nil, extension: nil, pdf_password: nil)
      QueryURLs.image(config, Options.pipelines_of(options), source, params, extension, pdf_password)
    end

    # Builds a /q/video URL; crop and flip are written c and fl. An Array of
    # option Hashes is written in order, so a key may repeat (later wins
    # argument by argument).
    def video_url(source, options = nil, extension: nil)
      QueryURLs.video(config, Options.pipelines_of(options).flatten(1), source, extension)
    end

    # Builds a /q/video HLS or DASH manifest URL (format=m3u8|mpd).
    def stream_url(source, protocol:, renditions: nil, segment_duration: nil, segment_format: nil, options: nil)
      entries, ext = URLs.stream_parts(Options.pipelines_of(options), protocol, renditions, segment_duration,
                                       segment_format,)
      QueryURLs.video(config, entries, source, ext)
    end

    # Builds an imaginary endpoint URL (/q/resize?url=…&width=300).
    def imaginary_url(endpoint, source, params = nil)
      QueryURLs.imaginary(config, endpoint, source, params)
    end

    # Starts a fluent /q/?url=… URL for a source.
    def image(source)
      cfg = config
      QueryURLChain.new(source, lambda { |s|
        QueryURLs.image(cfg, s.pipelines, s.source, s.params, s.ext, s.pdf_password)
      },)
    end

    # Starts a fluent /q/video URL for a source.
    def video(source)
      cfg = config
      QueryVideoURLChain.new(source, ->(s) { QueryURLs.video(cfg, QueryURLs.single_pipeline(s.pipelines), s.source, s.ext) })
    end

    def inspect = "#<#{self.class.name} #{config.inspect}>"
  end

  # The URL assembly behind QueryURLBuilder.
  # @api private
  module QueryURLs
    ENDPOINT = /\A[a-z]+\z/
    private_constant :ENDPOINT

    module_function

    def native_value(key, value)
      case value
      when true then "true"
      when false then "false"
      when Integer, Float then Text.format_number(value, "query key #{key}")
      when String, Symbol then value.to_s
      else raise URLBuildError.new(:invalid_argument, "query key #{key}: unsupported value type #{value.class}")
      end
    end

    def param_pairs(params)
      return [] if params.nil?

      params.filter_map { |k, v| [k.to_s, native_value(k, v)] unless v.nil? }
    end

    def finish(cfg, path, pairs)
      base = URLs.trim_slashes(cfg.base_url)
      mount = URLs.trim_slashes(cfg.mount)
      query = pairs.map { |k, v| "#{Text.escape_query_value(k)}=#{Text.escape_query_value(v)}" }.join("&")
      key = cfg.signing
      query = Signing.sign_xi_query(path, query, key) if key
      "#{base}#{mount}#{path}?#{query}"
    end

    # A native /q/ image URL; o= carries the option pipelines.
    def image(cfg, pipelines, source, params, ext, pdf_password)
      sep = URLs.check_separator(cfg.separator, FORBIDDEN_QUERY_SEPARATORS)
      style = cfg.option_names
      # @type var pairs: Array[[String, String]]
      pairs = [["url", Text.check_text(source, :invalid_source, "source")], *param_pairs(params)]
      non_empty = pipelines.reject(&:empty?)
      unless non_empty.empty?
        o = non_empty.map { |p| p.map { |e| Options.format_entry(Options::PROCESSING, e, sep, style) }.join("/") }
        pairs << ["o", o.join("/#{PIPELINE_SEPARATOR}/")]
      end
      pairs << ["format", Text.check_extension(ext)] unless ext.nil? || ext.to_s.empty?
      unless pdf_password.nil?
        token = URLs.password_token(cfg.encryption, cfg.signing, pdf_password)
        pairs << [style == :canonical ? PDF_PASSWORD_NAME : PDF_PASSWORD_SHORT, token]
      end
      finish(cfg, "/", pairs)
    end

    # The entries of a fluent /q/video chain, which has a single pipeline.
    def single_pipeline(pipelines)
      raise URLBuildError.new(:invalid_request, "/q/video URLs have no chained pipelines") if pipelines.count do |p|
        !p.empty?
      end > 1

      pipelines.flatten(1)
    end

    # A /q/video URL: every video option is a key, its arguments joined by ",".
    def video(cfg, entries, source, ext)
      # @type var pairs: Array[[String, String]]
      pairs = [["url", Text.check_text(source, :invalid_source, "source")]]
      entries.each do |name, args|
        key = Options.written_name(Options::VIDEO, name, cfg.option_names)
        raise URLBuildError.new(:invalid_argument, "option #{name}: at least one argument is required") if args.empty?

        values = args.each_with_index.map do |a, i|
          s = Options.format_arg(a, ",", "option #{name} argument #{i + 1}")
          if s.include?(":")
            raise URLBuildError.new(:invalid_argument,
                                    "option #{name} argument #{i + 1}: #{s.inspect} contains ':'",)
          end

          s
        end
        pairs << [NATIVE_VIDEO_KEYS.fetch(key, key), values.join(",")]
      end
      pairs << ["format", Text.check_extension(ext)] unless ext.nil? || ext.to_s.empty?
      finish(cfg, "/video", pairs)
    end

    # An imaginary endpoint URL.
    def imaginary(cfg, endpoint, source, params)
      ep = endpoint.to_s
      if !ENDPOINT.match?(ep) || ep == "video"
        raise URLBuildError.new(:invalid_endpoint,
                                "invalid imaginary endpoint #{endpoint.inspect}",)
      end

      finish(cfg, "/#{ep}", [["url", Text.check_text(source, :invalid_source, "source")], *param_pairs(params)])
    end
  end

  # The content type of an HTTP QUERY request's content.
  QUERY_CONTENT_TYPE = "application/x-www-form-urlencoded"

  # A query dialect request sent with the HTTP QUERY method (RFC 10008,
  # URL-DIALECTS §4.4): +http_method+ "QUERY", +url+ the URL without its query,
  # +content_type+ application/x-www-form-urlencoded, and +body+ the URL's
  # query exactly as built, xi_sig included (the server signs, limits and
  # caches it as the GET URL, since xi_sig does not cover the method).
  QueryMethodRequest = Data.define(:http_method, :url, :content_type, :body)

  # Splitting a built query dialect URL.
  class QueryMethodRequest
    # Splits a built query dialect URL (/q/…?url=…) at its first "?": the
    # parameters move from the URL to the content, so long option chains,
    # watermark text or encrypted sources stay out of URLs. The server must
    # have XI_SERVER_QUERY_METHOD on (the default); many CDNs and proxies
    # neither cache nor forward QUERY.
    #
    # Raises URLBuildError (:invalid_request) for a URL without a query, with
    # an empty one, or with a fragment.
    def self.from_url(url)
      path, query = url.split("?", 2)
      if path.nil? || query.nil? || query.empty? || url.include?("#")
        raise URLBuildError.new(:invalid_request, "a QUERY request needs a query dialect URL with a query and no fragment")
      end

      new(http_method: "QUERY", url: path, content_type: QUERY_CONTENT_TYPE, body: query)
    end
  end
end
