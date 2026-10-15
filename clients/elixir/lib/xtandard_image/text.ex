defmodule XtandardImage.Text do
  @moduledoc false
  # Byte and text helpers shared by signing, encryption and the URL builders.

  alias XtandardImage.Error
  alias XtandardImage.Spec

  @plain_safe Spec.plain_safe() |> String.to_charlist()
  @query_safe Spec.query_safe() |> String.to_charlist()

  @doc "Decodes hex key material (either case, surrounding whitespace ignored) or returns raw bytes."
  @spec key_bytes!(XtandardImage.key_material(), String.t()) :: binary()
  def key_bytes!({:raw, bytes}, _what) when is_binary(bytes), do: bytes

  def key_bytes!(hex, what) when is_binary(hex) do
    case Base.decode16(String.trim(hex), case: :mixed) do
      {:ok, bytes} -> bytes
      :error -> Error.raise!(:invalid_key, "#{what} is not a hex string")
    end
  end

  def key_bytes!(_other, what), do: Error.raise!(:invalid_key, "#{what} is not a hex string")

  @doc "Unpadded base64url (RFC 4648 §5)."
  @spec b64url(binary()) :: String.t()
  def b64url(bytes), do: Base.url_encode64(bytes, padding: false)

  @doc "Whether a string contains an ASCII control character (C0 or DEL)."
  @spec control_char?(binary()) :: boolean()
  def control_char?(<<c, _::binary>>) when c < 0x20 or c == 0x7F, do: true
  def control_char?(<<_, rest::binary>>), do: control_char?(rest)
  def control_char?(<<>>), do: false

  @doc "Rejects a value that is not a non-empty string without control characters."
  @spec check_text!(term(), Error.code(), String.t()) :: String.t()
  def check_text!(s, code, what) do
    if is_binary(s) and s != "" and not control_char?(s) do
      s
    else
      Error.raise!(code, "#{what} must be a non-empty string without control characters")
    end
  end

  @doc "Rejects an extension that is not ASCII letters and digits."
  @spec check_extension!(term()) :: String.t()
  def check_extension!(ext) when is_atom(ext) and not is_nil(ext) and not is_boolean(ext),
    do: check_extension!(Atom.to_string(ext))

  def check_extension!(ext) do
    if is_binary(ext) and ext != "" and alnum?(ext) do
      ext
    else
      Error.raise!(:invalid_extension, "invalid extension #{inspect(ext)}")
    end
  end

  defp alnum?(<<c, rest::binary>>) when c in ?a..?z or c in ?A..?Z or c in ?0..?9,
    do: alnum?(rest)

  defp alnum?(<<>>), do: true
  defp alnum?(_), do: false

  @doc "Percent-escapes a source for a `plain/` URL."
  @spec escape_plain_source(String.t()) :: String.t()
  def escape_plain_source(s) when is_binary(s), do: escape(s, @plain_safe)

  @doc "Percent-escapes a query key or value."
  @spec escape_query_value(String.t()) :: String.t()
  def escape_query_value(s) when is_binary(s), do: escape(s, @query_safe)

  defp escape(s, safe) do
    for <<c <- s>>, into: "" do
      if c in ?a..?z or c in ?A..?Z or c in ?0..?9 or c in safe do
        <<c>>
      else
        "%" <> Base.encode16(<<c>>)
      end
    end
  end

  @doc """
  Formats a number like ECMAScript's Number::toString (`300`, `1.5`, `0.1`,
  `1e+21`, `1e-7`), so URLs and their signatures match across languages.
  """
  @spec format_number(number()) :: String.t()
  def format_number(n) when is_integer(n) and abs(n) < 1_000_000_000_000_000_000_000,
    do: Integer.to_string(n)

  def format_number(n) when is_integer(n), do: format_number(n * 1.0)
  def format_number(f) when is_float(f) and f == 0, do: "0"

  def format_number(f) when is_float(f) do
    sign = if f < 0, do: "-", else: ""
    # Shortest round-trip digits, e.g. "1.5", "1.0e-7", "1.0e21".
    {mantissa, exp} =
      case String.split(:erlang.float_to_binary(abs(f), [:short]), "e") do
        [m] -> {m, 0}
        [m, e] -> {m, String.to_integer(e)}
      end

    {int_part, frac} =
      case String.split(mantissa, ".") do
        [i] -> {i, ""}
        [i, fr] -> {i, fr}
      end

    all = int_part <> frac
    stripped = String.trim_leading(all, "0")
    lead = byte_size(all) - byte_size(stripped)
    digits = String.trim_trailing(stripped, "0")
    # abs(f) = 0.<digits> x 10^n (ECMAScript's k digits and exponent n)
    n = byte_size(int_part) + exp - lead
    sign <> layout(digits, byte_size(digits), n)
  end

  defp layout(digits, k, n) when k <= n and n <= 21,
    do: digits <> String.duplicate("0", n - k)

  defp layout(digits, _k, n) when 0 < n and n <= 21,
    do: binary_part(digits, 0, n) <> "." <> binary_part(digits, n, byte_size(digits) - n)

  defp layout(digits, _k, n) when -6 < n and n <= 0,
    do: "0." <> String.duplicate("0", -n) <> digits

  defp layout(digits, k, n) do
    e = n - 1
    es = "e" <> if(e >= 0, do: "+", else: "-") <> Integer.to_string(abs(e))

    if k == 1,
      do: digits <> es,
      else: binary_part(digits, 0, 1) <> "." <> binary_part(digits, 1, k - 1) <> es
  end
end
