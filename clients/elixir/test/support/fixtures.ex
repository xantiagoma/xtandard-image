defmodule XtandardImage.Fixtures do
  @moduledoc false
  # The shared fixtures, read from the repository by relative path
  # (clients/elixir -> repository root).

  @root Path.expand("../../../..", __DIR__)

  @spec load(String.t()) :: map()
  def load(rel) do
    path = Path.join(@root, rel)
    {:ok, value} = path |> File.read!() |> XtandardImage.JSON.decode()
    value
  end

  @spec path(String.t()) :: String.t()
  def path(rel), do: Path.join(@root, rel)
end
