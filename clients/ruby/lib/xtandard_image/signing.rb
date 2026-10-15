# frozen_string_literal: true

module XtandardImage
  # A signing key: XI_SIGNING_KEYS / XI_SIGNING_SALTS (imgproxy IMGPROXY_KEY /
  # IMGPROXY_SALT).
  #
  # +key+ and +salt+ are hex Strings (as in the server config) or binary
  # Strings of raw bytes; +size+ is the signature size in bytes before
  # encoding, 1 to 32 (XI_SIGNING_SIZE). +inspect+ hides the secrets.
  #
  #   XtandardImage::SigningKey.new(key: ENV.fetch("XI_KEY"), salt: ENV.fetch("XI_SALT"))
  SigningKey = Data.define(:key, :salt, :size)

  # Checks the key material and the size; hides the secrets from inspect.
  class SigningKey
    def initialize(key:, salt:, size: DEFAULT_SIGNATURE_SIZE)
      unless size.is_a?(Integer) && size.between?(MIN_SIGNATURE_SIZE, MAX_SIGNATURE_SIZE)
        raise URLBuildError.new(:invalid_key,
                                "signature size #{size.inspect} out of range [#{MIN_SIGNATURE_SIZE}, #{MAX_SIGNATURE_SIZE}]",)
      end

      Text.key_bytes(key, "signing key")
      Text.key_bytes(salt, "signing salt")
      super
    end

    def inspect = "#<data #{self.class.name} size=#{size}>"
    alias to_s inspect
  end

  # Signing, byte-compatible with the server (internal/security/sign).
  #
  # * imgproxy: base64url(HMAC-SHA256(key, salt ‖ path)[0, size]), the
  #   signature segment of /{signature}/{options}/{source}; /video URLs sign
  #   "/video" + path.
  # * xi_sig: the same construction over path ‖ "?" ‖ query-without-xi_sig,
  #   appended as the last query parameter (docs/URL-DIALECTS.md §2).
  module Signing
    ABSOLUTE = %r{\A[a-zA-Z][a-zA-Z0-9+.-]*://}
    private_constant :ABSOLUTE

    module_function

    # Signs a message with the salted HMAC-SHA256 construction of the
    # imgproxy and xi_sig schemes. Like the server, a message without a
    # leading "/" is signed as if it had one.
    def sign(message, key)
      m = message.start_with?("/") ? message : "/#{message}"
      data = Text.key_bytes(key.salt, "signing salt") + Text.utf8(m).b
      mac = OpenSSL::HMAC.digest("SHA256", Text.key_bytes(key.key, "signing key"), data)
      Text.b64url(mac.byteslice(0, key.size).to_s)
    end

    # "/" + signature + path for an imgproxy path (everything after the signature segment).
    def sign_imgproxy_path(path, key)
      p = path.start_with?("/") ? path : "/#{path}"
      "/#{sign(p, key)}#{p}"
    end

    # Splits a raw query into its xi_sig value (or nil) and the message xi_sig
    # signs: the path, then "?" and the remaining non-empty parameters in
    # their original order and encoding.
    #
    # Raises URLBuildError (:invalid_request) when the query has more than
    # one xi_sig (the server answers 403).
    def xi_message(path, raw_query)
      # @type var sig: String?
      sig = nil
      # @type var kept: Array[String]
      kept = []
      raw_query.split("&").each do |part|
        next if part.empty?

        k, eq, v = part.partition("=")
        unless k == XI_SIG_PARAM
          kept << part
          next
        end
        raise URLBuildError.new(:invalid_request, "query has more than one #{XI_SIG_PARAM} parameter") unless sig.nil?

        sig = eq.empty? ? "" : v
      end
      [sig, kept.empty? ? path : "#{path}?#{kept.join("&")}"]
    end

    # raw_query without any xi_sig and with a fresh xi_sig for path appended
    # last. +path+ is the escaped path the dialect sees, after its mount prefix.
    def sign_xi_query(path, raw_query, key)
      _, msg = xi_message(path, raw_query.delete_prefix("?"))
      sig = sign(msg, key)
      q = msg[path.length..].to_s
      q.empty? ? "#{XI_SIG_PARAM}=#{sig}" : "#{q[1..]}&#{XI_SIG_PARAM}=#{sig}"
    end

    # Signs a URL (absolute, or a path with an optional query) with xi_sig.
    #
    # +mount+ is the dialect's mount prefix, which the signed path excludes
    # ("/q" for the query dialect). A fragment is dropped.
    #
    # Raises URLBuildError (:invalid_request) when the path is not under the
    # mount or the query has two xi_sig.
    def sign_xi_url(url, key, mount: "")
      head, _, query = url.split("#", 2).first.to_s.partition("?")
      origin, path = split_origin(head)
      m = mount.sub(%r{/+\z}, "")
      unless m.empty?
        unless path == m || path.start_with?("#{m}/")
          raise URLBuildError.new(:invalid_request,
                                  "path #{path} is not under the mount #{m}",)
        end

        path = path[m.length..].to_s
        path = "/" if path.empty?
      end
      "#{origin}#{m}#{path}?#{sign_xi_query(path, query, key)}"
    end

    # [origin, path] of an absolute URL or a path.
    def split_origin(head)
      return ["", head] unless ABSOLUTE.match?(head)

      path_start = head.index("/", head.index("//").to_i + 2)
      path_start.nil? ? [head, "/"] : [head[0, path_start].to_s, head[path_start..].to_s]
    end
  end
end
