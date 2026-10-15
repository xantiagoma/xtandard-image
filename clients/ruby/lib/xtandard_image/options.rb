# frozen_string_literal: true

module XtandardImage
  # Option catalogues at run time and their serialisation into option segments.
  # @api private
  module Options
    # One option catalogue: every name and alias to its canonical name, and canonical to short names.
    Catalogue = Data.define(:kind, :names, :short)

    PROCESSING = Catalogue.new(kind: "imgproxy", names: PROCESSING_NAMES, short: PROCESSING_SHORT)
    INFO = Catalogue.new(kind: "info", names: INFO_NAMES, short: INFO_SHORT)
    VIDEO = Catalogue.new(kind: "video", names: VIDEO_NAMES, short: VIDEO_SHORT)

    # How option names are written: the shortest alias (default), the canonical name, or as given.
    NAME_STYLES = %i[short canonical given].freeze

    # Characters an option argument cannot contain besides the separator.
    BAD_ARG = %r{[/?#\x00-\x1f\x7f\p{Zs}\p{Zl}\p{Zp}\u{85}\u{FEFF}]}
    private_constant :BAD_ARG

    module_function

    # Normalises an option_names setting (Symbol or String).
    def name_style(style)
      s = style.to_s.to_sym
      unless NAME_STYLES.include?(s)
        raise ArgumentError,
              "option_names must be one of #{NAME_STYLES.join(", ")}, not #{style.inspect}"
      end

      s
    end

    # Converts an options Hash into [name, args] entries, in key order; nil values are skipped.
    def entries_of(options)
      raise URLBuildError.new(:invalid_argument, "options must be a Hash, not #{options.class}") unless options.is_a?(Hash)

      options.filter_map do |name, value|
        next if value.nil?

        [name.to_s, value.is_a?(Array) ? value.dup : [value]]
      end
    end

    # One entry list per pipeline: a Hash is one pipeline, an Array of Hashes chains them.
    def pipelines_of(options)
      case options
      when nil then [[]]
      when Hash then [entries_of(options)]
      when Array
        pipelines = options.map { |o| entries_of(o) }
        pipelines.empty? ? [[]] : pipelines
      else
        raise URLBuildError.new(:invalid_argument, "options must be a Hash or an Array of Hashes, not #{options.class}")
      end
    end

    # Formats one argument. Strings are checked, never escaped: the server
    # does not unescape option arguments.
    def format_arg(arg, sep, where)
      case arg
      when nil then ""
      when true then "1"
      when false then "0"
      when Integer, Float then Text.format_number(arg, where)
      when String, Symbol
        s = arg.to_s
        if s.include?(sep) || BAD_ARG.match?(Text.utf8(s))
          raise URLBuildError.new(:invalid_argument,
                                  "#{where}: argument #{s.inspect} contains a character that cannot appear in an option",)
        end

        s
      else
        raise URLBuildError.new(:invalid_argument, "#{where}: unsupported argument type #{arg.class}")
      end
    end

    # The name an option is written with, or URLBuildError for an unknown name.
    def written_name(cat, name, style)
      canonical = cat.names[name]
      raise URLBuildError.new(:unknown_option, "unknown #{cat.kind} option #{name.inspect}") if canonical.nil?

      case style
      when :given then name
      when :canonical then canonical
      else cat.short.fetch(canonical, canonical)
      end
    end

    # Formats one option segment (rs:fill:300:400).
    def format_entry(cat, entry, sep, style)
      name, args = entry
      written = written_name(cat, name, style)
      raise URLBuildError.new(:invalid_argument, "option #{name}: at least one argument is required") if args.empty?

      parts = args.each_with_index.map { |a, i| format_arg(a, sep, "option #{name} argument #{i + 1}") }
      [written, *parts].join(sep)
    end
  end
end
