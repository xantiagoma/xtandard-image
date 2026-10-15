# frozen_string_literal: true

module XtandardImage
  # Byte and text helpers shared by signing, encryption and the URL builders.
  # @api private
  module Text
    HEX = /\A(?:[0-9a-fA-F]{2})*\z/
    CONTROL = /[\x00-\x1f\x7f]/
    ALNUM = /\A[A-Za-z0-9]\z/
    MAX_EXACT_INTEGER = 1_000_000_000_000_000_000_000 # 1e21
    private_constant :HEX, :CONTROL, :ALNUM, :MAX_EXACT_INTEGER

    module_function

    # Decodes key material: a hex String (either case, surrounding whitespace
    # ignored), or a binary String (Encoding::BINARY) of raw bytes.
    def key_bytes(material, what)
      raise URLBuildError.new(:invalid_key, "#{what} must be a String") unless material.is_a?(String)
      return material.dup if material.encoding == Encoding::BINARY

      hex = material.strip
      raise URLBuildError.new(:invalid_key, "#{what} is not a hex string") unless HEX.match?(hex)

      [hex].pack("H*")
    end

    # Unpadded base64url (RFC 4648 §5).
    def b64url(bytes)
      [bytes].pack("m0").tr("+/", "-_").delete("=")
    end

    # The UTF-8 form of s; invalid bytes become U+FFFD, as with TextEncoder.
    def utf8(str)
      if str.encoding == Encoding::UTF_8
        str.scrub("\u{FFFD}")
      elsif [Encoding::BINARY, Encoding::US_ASCII].include?(str.encoding)
        str.dup.force_encoding(Encoding::UTF_8).scrub("\u{FFFD}")
      else
        str.encode(Encoding::UTF_8, invalid: :replace, undef: :replace, replace: "\u{FFFD}")
      end
    end

    # Whether s contains an ASCII control character (C0 or DEL).
    def control_char?(str)
      CONTROL.match?(utf8(str))
    end

    # Rejects a value that is not a String, is empty or has control characters.
    def check_text(str, code, what)
      unless str.is_a?(String) && !str.empty? && !control_char?(str)
        raise URLBuildError.new(code, "#{what} must be a non-empty string without control characters")
      end

      str
    end

    # Rejects an extension that is not alphanumeric.
    def check_extension(ext)
      s = ext.to_s
      raise URLBuildError.new(:invalid_extension, "invalid extension #{ext.inspect}") unless EXTENSION_PATTERN.match?(s)

      s
    end

    # Percent-escapes a source for a plain/ URL; the server path-unescapes it.
    #
    # RFC 3986 unreserved characters, the sub-delims, ":" and "/" are kept;
    # everything else ("%", "@", "?", "#", spaces, non-ASCII) is
    # percent-encoded as UTF-8.
    def escape_plain_source(source)
      escape(source, PLAIN_SAFE)
    end

    # Percent-escapes a query key or value, keeping ":", "/" and "," readable.
    def escape_query_value(value)
      escape(value, QUERY_SAFE)
    end

    def escape(str, safe)
      out = +""
      utf8(str).each_char do |ch|
        if ALNUM.match?(ch) || safe.include?(ch)
          out << ch
        else
          ch.each_byte { |b| out << format("%%%02X", b) }
        end
      end
      out
    end

    # Formats a number like ECMAScript Number::toString (300, 1.5, 0.1, 1e+21, 1e-7).
    #
    # Every client writes numbers this way, so URLs (and their signatures)
    # match across languages.
    def format_number(value, where)
      float = number_float(value, where)
      return float if float.is_a?(String)
      return "0" if float.zero?

      (float.negative? ? "-" : "") + shortest(float.abs)
    end

    # An Integer below 1e21 as its digits, anything else as a finite Float.
    def number_float(value, where)
      case value
      when Integer
        return value.to_s if value.abs < MAX_EXACT_INTEGER

        value.to_f
      when Float
        raise URLBuildError.new(:invalid_argument, "#{where}: #{value} is not a finite number") unless value.finite?

        value
      else
        raise URLBuildError.new(:invalid_argument, "#{where}: unsupported number type #{value.class}")
      end
    end

    # ECMAScript's layout of a positive finite Float from Ruby's shortest
    # round-trip digits.
    def shortest(float)
      mantissa, exp = float.to_s.split("e", 2)
      int_part, frac = (mantissa || "").split(".", 2)
      int_part ||= ""
      all = int_part + (frac || "")
      lead = all.length - all.sub(/\A0+/, "").length
      digits = all[lead..].to_s.sub(/0+\z/, "")
      # float = 0.<digits> x 10^n (ECMAScript's k digits and exponent n)
      n = int_part.length + exp.to_i - lead
      layout(digits, n)
    end

    def layout(digits, n)
      k = digits.length
      if n.between?(k, 21)
        digits + ("0" * (n - k))
      elsif n.positive? && n <= 21
        "#{digits[0, n]}.#{digits[n..]}"
      elsif n > -6 && n <= 0
        "0.#{"0" * -n}#{digits}"
      else
        e = n - 1
        es = "e#{e >= 0 ? "+" : "-"}#{e.abs}"
        k == 1 ? digits + es : "#{digits[0]}.#{digits[1..]}#{es}"
      end
    end
  end
end
