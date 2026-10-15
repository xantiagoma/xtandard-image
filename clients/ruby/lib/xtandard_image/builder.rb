# frozen_string_literal: true

module XtandardImage
  # Settings shared by every URL an ImgproxyURLBuilder makes (see ImgproxyURLBuilder#initialize).
  ImgproxyConfig = Data.define(
    :base_url, :signing, :unsigned_signature, :encryption, :source_encoding, :split_source, :separator, :option_names,
  )

  # Hides the keys from inspect.
  class ImgproxyConfig
    def inspect = "#<data #{self.class.name} base_url=#{base_url.inspect} signed=#{!signing.nil?}>"
    alias to_s inspect
  end

  # A fluent processing URL: one method per option name and alias, +pipeline+ for /-/.
  class ImgproxyURLChain < Chain
    include WithEncoding
    include WithExtension
    include WithPipeline
    include WithPassword
    include ProcessingMethods
  end

  # A fluent /info URL.
  class ImgproxyInfoURLChain < Chain
    include WithEncoding
    include WithPassword
    include InfoMethods
  end

  # A fluent /video URL: video transformations, audio and waveforms.
  class ImgproxyVideoURLChain < Chain
    include WithEncoding
    include WithExtension
    include VideoMethods
  end

  # Builds imgproxy-dialect URLs (docs/URL-DIALECTS.md §3.1) for an
  # xtandard-image (or imgproxy) server:
  #
  #   {base_url}[/info]/{signature}/{option}/…[/-/{option}/…]/{source}
  #   {base_url}/video/{signature}/{video option}/…/{source}   (signs "/video" + path)
  #
  # Sources are base64url (default), plain/ (percent-escaped, @ext) or enc/
  # (AES-CBC); base64 and enc sources take .ext.
  #
  #   img = XtandardImage::ImgproxyURLBuilder.new(base_url: "https://img.example.com", signing: key)
  #   img.url("s3://bucket/cat.jpg", { rs: ["fill", 300, 400], q: 80 }, extension: "webp")
  #   img.image("s3://bucket/cat.jpg").resize("fill", 300, 400).quality(80).extension("webp").build
  #   img.video_url("s3://bucket/talk.mov", { rs: ["fill", 640, 360], vs: 12, vd: 8 }, extension: "webm")
  #   img.stream_url("s3://bucket/talk.mov", protocol: :hls, renditions: [360, 720])
  class ImgproxyURLBuilder
    # The settings (an ImgproxyConfig).
    attr_reader :config

    # base_url:: Prepended to every path: the server origin and the dialect's
    #            mount prefix, if any. Default "" (root-relative paths).
    # signing:: A SigningKey; without it the signature segment is +unsigned_signature+.
    # unsigned_signature:: Signature segment of unsigned URLs: insecure, unsafe or _.
    # encryption:: An EncryptionKey for encrypted sources and PDF passwords.
    # source_encoding:: Default source encoding: :base64, :plain or :encrypted.
    # split_source:: Splits base64 and encrypted sources into /-separated chunks of this many characters.
    # separator:: Argument separator (XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR).
    # option_names:: :short (default), :canonical or :given (as written).
    def initialize(base_url: "", signing: nil, unsigned_signature: DEFAULT_UNSIGNED, encryption: nil,
                   source_encoding: :base64, split_source: nil, separator: DEFAULT_SEPARATOR, option_names: :short)
      @config = ImgproxyConfig.new(
        base_url: base_url, signing: signing, unsigned_signature: unsigned_signature, encryption: encryption,
        source_encoding: source_encoding, split_source: split_source, separator: separator,
        option_names: Options.name_style(option_names),
      )
    end

    # Builds a processing URL; an Array of option Hashes chains pipelines.
    #
    # source:: The source URL (https://, s3://, xi://, local:///, …).
    # options:: Processing options keyed by name or alias, in URL order.
    # extension:: Output format extension (@webp / .webp).
    # source_encoding:: Overrides the configured source encoding.
    # pdf_password:: Plain-text password of a protected PDF (needs +encryption+ and +signing+).
    def url(source, options = nil, extension: nil, source_encoding: nil, pdf_password: nil)
      URLs.imgproxy(config, :process, Options.pipelines_of(options), source, source_encoding, extension, pdf_password)
    end

    # Builds an /info URL.
    def info_url(source, options = nil, source_encoding: nil, pdf_password: nil)
      URLs.imgproxy(config, :info, Options.pipelines_of(options), source, source_encoding, nil, pdf_password)
    end

    # Builds a /video URL (video transformation, audio or waveform; +extension+ is the container).
    def video_url(source, options = nil, extension: nil, source_encoding: nil)
      URLs.imgproxy(config, :video, Options.pipelines_of(options), source, source_encoding, extension, nil)
    end

    # Builds an HLS or DASH manifest URL (/video/…@m3u8 or @mpd).
    #
    # protocol:: :hls or :dash.
    # renditions:: Rungs by the short side of the frame (at most 8); default: the server's ladder.
    # segment_duration:: Segment length in whole seconds (1 to 30).
    # segment_format:: :fmp4 (CMAF, default) or :ts (MPEG-TS, HLS only).
    # options:: Other /video options (trim, crop, quality, audio, …) applied to every rendition.
    def stream_url(source, protocol:, renditions: nil, segment_duration: nil, segment_format: nil, options: nil,
                   source_encoding: nil)
      entries, ext = URLs.stream_parts(Options.pipelines_of(options), protocol, renditions, segment_duration,
                                       segment_format,)
      URLs.imgproxy(config, :video, [entries], source, source_encoding, ext, nil)
    end

    # Starts a fluent processing URL for a source.
    def image(source) = ImgproxyURLChain.new(source, runner(:process))

    # Starts a fluent /info URL for a source.
    def info(source) = ImgproxyInfoURLChain.new(source, runner(:info))

    # Starts a fluent /video URL for a source.
    def video(source) = ImgproxyVideoURLChain.new(source, runner(:video))

    def inspect = "#<#{self.class.name} #{config.inspect}>"

    private

    def runner(endpoint)
      cfg = config
      lambda do |s|
        URLs.imgproxy(cfg, endpoint, s.pipelines, s.source, s.encoding, s.ext, s.pdf_password)
      end
    end
  end

  # The URL assembly behind the builders.
  # @api private
  module URLs
    STREAM_OPTIONS = %w[stream segment_duration segment_format format].freeze
    private_constant :STREAM_OPTIONS

    module_function

    # Rejects a separator that is not one character or that the grammar uses.
    def check_separator(sep, forbidden)
      unless sep.is_a?(String) && sep.length == 1 && !forbidden.include?(sep)
        raise URLBuildError.new(:invalid_separator, "invalid argument separator #{sep.inspect}")
      end

      sep
    end

    # The pdf_password token, checking that both keys are present.
    def password_token(encryption, signing, password)
      raise URLBuildError.new(:missing_encryption, "pdf_password needs an encryption key") if encryption.nil?
      if signing.nil?
        raise URLBuildError.new(:missing_signing, "pdf_password needs signing: the server refuses it on unsigned URLs")
      end

      Encryption.encrypt_pdf_password(password, encryption)
    end

    def trim_slashes(str) = str.sub(%r{/+\z}, "")

    # Builds an imgproxy-dialect URL from option pipelines and a source.
    def imgproxy(cfg, endpoint, pipelines, source, encoding, ext, pdf_password)
      sep = check_separator(cfg.separator, FORBIDDEN_SEPARATORS)
      style = cfg.option_names
      cat = { info: Options::INFO, video: Options::VIDEO }.fetch(endpoint, Options::PROCESSING)
      non_empty = pipelines.reject(&:empty?)
      if endpoint != :process && non_empty.length > 1
        raise URLBuildError.new(:invalid_request, "/#{endpoint} URLs have no chained pipelines")
      end
      raise URLBuildError.new(:invalid_request, "/video URLs take no pdf_password") if endpoint == :video && !pdf_password.nil?

      # @type var segments: Array[String]
      segments = []
      unless pdf_password.nil?
        token = password_token(cfg.encryption, cfg.signing, pdf_password)
        segments << "#{style == :canonical ? PDF_PASSWORD_NAME : PDF_PASSWORD_SHORT}#{sep}#{token}"
      end
      non_empty.each_with_index do |p, i|
        segments << PIPELINE_SEPARATOR if i.positive?
        p.each { |e| segments << Options.format_entry(cat, e, sep, style) }
      end
      segments << source_segment(cfg, source, encoding || cfg.source_encoding, ext)
      finish(cfg, endpoint, "/#{segments.join("/")}")
    end

    def source_segment(cfg, source, encoding, ext)
      Text.check_text(source, :invalid_source, "source")
      e = ext.nil? || ext.to_s.empty? ? nil : Text.check_extension(ext)
      case encoding.to_s
      when "plain" then "plain/#{Text.escape_plain_source(source)}#{"@#{e}" if e}"
      when "base64" then "#{split(Text.b64url(Text.utf8(source).b), cfg.split_source)}#{".#{e}" if e}"
      when "encrypted"
        enc = cfg.encryption
        raise URLBuildError.new(:missing_encryption, "encrypted sources need an encryption key") if enc.nil?

        "#{ENCRYPTED_SOURCE_PREFIX}#{split(Encryption.encrypt_source(source, enc), cfg.split_source)}#{".#{e}" if e}"
      else
        raise URLBuildError.new(:invalid_request, "unknown source encoding #{encoding.inspect}")
      end
    end

    def split(token, size)
      return token if size.nil? || size <= 0 || token.length <= size

      token.chars.each_slice(size).map(&:join).join("/")
    end

    def finish(cfg, endpoint, path)
      base = trim_slashes(cfg.base_url)
      key = cfg.signing
      if endpoint == :video
        sig = key ? Signing.sign("/video#{path}", key) : cfg.unsigned_signature
        return "#{base}/video/#{sig}#{path}"
      end
      signed = key ? Signing.sign_imgproxy_path(path, key) : "/#{cfg.unsigned_signature}#{path}"
      "#{base}#{"/info" if endpoint == :info}#{signed}"
    end

    # The video options and manifest container of a stream request.
    def stream_parts(pipelines, protocol, renditions, segment_duration, segment_format)
      entries = pipelines.flatten(1)
      entries.map(&:first).each do |name|
        next unless STREAM_OPTIONS.include?(VIDEO_NAMES[name])

        raise URLBuildError.new(:invalid_request,
                                "stream requests set #{name} through protocol, renditions and the segment settings",)
      end
      proto = protocol.to_s
      manifest = STREAM_MANIFESTS[proto]
      raise URLBuildError.new(:invalid_request, "unknown stream protocol #{protocol.inspect}") if manifest.nil?

      entries << ["stream", [proto, *renditions]] if renditions && !renditions.empty?
      entries << ["segment_duration", [segment_duration]] unless segment_duration.nil?
      append_segment_format(entries, proto, segment_format) unless segment_format.nil?
      [entries, manifest.fetch(0)]
    end

    def append_segment_format(entries, proto, segment_format)
      fmt = segment_format.to_s
      unless SEGMENT_FORMAT_PROTOCOLS.fetch(fmt, []).include?(proto)
        raise URLBuildError.new(:invalid_request, "segment format #{segment_format.inspect} is not available for #{proto}")
      end

      entries << ["segment_format", [fmt]] unless fmt == DEFAULT_SEGMENT_FORMAT
    end
  end
end
