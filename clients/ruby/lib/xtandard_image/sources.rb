# frozen_string_literal: true

module XtandardImage
  # Source references (docs/URL-DIALECTS.md §3.1): the string a URL carries
  # as its source. These helpers only join strings; the URL builders escape
  # or encode the result.
  #
  #   https://host/path         an http source
  #   s3://bucket/key           gs://bucket/key   abs://container/key
  #   local:///path             ftp://host/path   sftp://host/path
  #   xi://<source>/<key>       a named source (XI_SOURCES)
  module Sources
    BAD_AUTHORITY = %r{[/?#[:space:]]}
    private_constant :BAD_AUTHORITY

    module_function

    # A named source reference, xi://<name>/<key>. The key may be relative
    # or, for an http source, an absolute URL:
    #
    #   xi_source("photos", "2026/cat.jpg")                # xi://photos/2026/cat.jpg
    #   xi_source("web", "https://cdn.example.com/a.jpg")  # xi://web/https://cdn.example.com/a.jpg
    #
    # Raises URLBuildError (:invalid_name) for a bad source name.
    def xi_source(name, key)
      unless name.is_a?(String) && XI_NAME_PATTERN.match?(name)
        raise URLBuildError.new(:invalid_name,
                                "invalid source name #{name.inspect}",)
      end

      "xi://#{name}/#{key.to_s.sub(%r{\A/+}, "")}"
    end

    # A source reference for a scheme: the bucket, container, host or source
    # name, then the key. local takes only a path (local:///<path>):
    #
    #   source_url(:s3, "photos", "cat.jpg")   # s3://photos/cat.jpg
    #   source_url(:local, "", "img/cat.jpg")  # local:///img/cat.jpg
    #
    # Raises URLBuildError (:invalid_name) when the authority is missing, has
    # "/", "?", "#" or whitespace, or is given for local.
    def source_url(scheme, authority, key)
      s = scheme.to_s
      k = key.to_s.sub(%r{\A/+}, "")
      case s
      when "xi" then xi_source(authority, key)
      when "local"
        raise URLBuildError.new(:invalid_name, "local sources have no authority: local:///<path>") unless authority.to_s.empty?

        "local:///#{k}"
      else
        a = authority.to_s
        raise URLBuildError.new(:invalid_name, "invalid #{s} authority #{a.inspect}") if a.empty? || BAD_AUTHORITY.match?(a)

        "#{s}://#{a}/#{k}"
      end
    end
  end
end
