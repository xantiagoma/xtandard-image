defmodule XtandardImage.Range do
  @moduledoc """
  Byte ranges for `Range` headers (partial `/video` results, stream parts).

      iex> XtandardImage.Range.header(0..1023)
      {:ok, "bytes=0-1023"}
      iex> XtandardImage.Range.header!({:from, 500})
      "bytes=500-"
      iex> XtandardImage.Range.header!({:suffix, 100})
      "bytes=-100"
      iex> XtandardImage.Range.header(%{start: 5, end: 4})
      {:error, %XtandardImage.Error{code: :invalid_range, message: "range end 4 is before start 5"}}
  """

  alias XtandardImage.Error

  @typedoc """
  A byte range: an inclusive `first..last` range, `{:from, start}` (to the end),
  `{:suffix, length}` (the last bytes), a map with `:start` (and `:end`) or
  `:suffix`, or a header value as is.
  """
  @type t ::
          Range.t()
          | {:from, non_neg_integer()}
          | {:suffix, non_neg_integer()}
          | %{required(:start) => non_neg_integer(), optional(:end) => non_neg_integer() | nil}
          | %{required(:suffix) => non_neg_integer()}
          | String.t()

  @doc "Formats a byte range as a `Range` header value."
  @spec header(t()) :: {:ok, String.t()} | {:error, Error.t()}
  def header(range), do: Error.wrap(fn -> header!(range) end)

  @doc "Like `header/1`, raising `XtandardImage.Error`."
  @spec header!(t()) :: String.t()
  def header!(range) when is_binary(range), do: range
  def header!({:suffix, n}), do: "bytes=-#{non_negative!(n, "suffix")}"
  def header!(%{suffix: n}), do: header!({:suffix, n})
  def header!({:from, start}), do: "bytes=#{non_negative!(start, "start")}-"
  def header!(%{start: start} = r), do: closed!(start, Map.get(r, :end))
  def header!(first..last//_), do: closed!(first, last)
  def header!(other), do: Error.raise!(:invalid_range, "invalid range #{inspect(other)}")

  defp closed!(start, nil), do: header!({:from, start})

  defp closed!(start, last) do
    s = non_negative!(start, "start")
    e = non_negative!(last, "end")
    if e < s, do: Error.raise!(:invalid_range, "range end #{e} is before start #{s}")
    "bytes=#{s}-#{e}"
  end

  defp non_negative!(n, _what) when is_integer(n) and n >= 0, do: n

  defp non_negative!(n, what),
    do: Error.raise!(:invalid_range, "range #{what} #{inspect(n)} is not a non-negative integer")
end
