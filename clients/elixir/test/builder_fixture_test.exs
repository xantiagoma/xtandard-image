defmodule XtandardImage.BuilderFixtureTest do
  # testdata/urls/imgproxy-builder.json: 400+ cases derived from the server's
  # golden tables. The URLs were written by the TypeScript client and parsed
  # back by the Go dialect tests (TestBuilderFixture); every client must write
  # the same ones: `url` with option names as given, `url_short` with the
  # default short names, and `query_url` (query dialect) where there is one.
  use ExUnit.Case, async: true

  alias XtandardImage.{Chain, EncryptionKey, Fixtures, Imgproxy, Query, SigningKey}

  @fixture Fixtures.load("testdata/urls/imgproxy-builder.json")
  @external_resource Fixtures.path("testdata/urls/imgproxy-builder.json")
  @cases @fixture["cases"]

  test "the fixture covers the tables" do
    assert length(@cases) >= 400
  end

  for c <- @cases do
    @case c
    test c["name"] do
      c = @case
      assert fluent(c, :given) == {:ok, c["url"]}
      assert fluent(c, :short) == {:ok, c["url_short"]}
      if c["query"], do: assert(query(c) == {:ok, c["query_url"]})
    end
  end

  defp signing(c) do
    case c["signature_size"] do
      size when size in [nil, 0] ->
        nil

      size ->
        SigningKey.new!(@fixture["signing"]["key"], @fixture["signing"]["salt"], size: size)
    end
  end

  defp fluent(c, names) do
    b =
      Imgproxy.new(
        unsigned_signature: "unsafe",
        encryption:
          EncryptionKey.new!(@fixture["encryption"]["key"], iv: @fixture["encryption"]["iv"]),
        signing: signing(c),
        separator: c["separator"] || ":",
        option_names: names
      )

    chain =
      case c["endpoint"] do
        "info" -> Imgproxy.info(b, c["source"])
        "video" -> Imgproxy.video(b, c["source"])
        _ -> Imgproxy.image(b, c["source"])
      end

    chain |> Chain.encoding(c["encoding"]) |> fill(c) |> Chain.build()
  end

  defp query(c) do
    q = Query.new(signing: signing(c))

    chain =
      if c["endpoint"] == "video",
        do: Query.video(q, c["source"]),
        else: Query.image(q, c["source"])

    chain |> fill(c) |> Chain.build()
  end

  defp fill(chain, c) do
    chain =
      c["pipelines"]
      |> Enum.with_index()
      |> Enum.reduce(chain, fn {pl, i}, chain ->
        chain = if i > 0, do: Chain.pipeline(chain), else: chain

        for entry <- pl, {name, args} <- entry, reduce: chain do
          chain -> Chain.option(chain, name, args)
        end
      end)

    if c["extension"] in [nil, ""], do: chain, else: Chain.extension(chain, c["extension"])
  end
end
