defmodule XtandardImage.JSON do
  @moduledoc false
  # JSON decoding on OTP 27's :json module, so the package needs no JSON
  # library: objects become maps with string keys and null becomes nil.

  @spec decode(binary()) :: {:ok, term()} | :error
  def decode(binary) when is_binary(binary) do
    case :json.decode(binary, :ok, %{null: nil}) do
      {value, :ok, rest} -> if blank?(rest), do: {:ok, value}, else: :error
    end
  rescue
    _ in [ErlangError, ArgumentError] -> :error
  end

  defp blank?(rest), do: String.trim(rest) == ""
end
