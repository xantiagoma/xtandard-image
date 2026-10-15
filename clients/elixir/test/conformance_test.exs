defmodule XtandardImage.ConformanceTest do
  # clients/spec/conformance.json: the cases every official client runs
  # (ADR 0013). Positive cases must produce expect.value byte for byte; error
  # cases must fail with expect.error as the code. URL cases are built twice,
  # through the options form (keyword-style pairs) and through the fluent
  # chain, and both must agree.
  use ExUnit.Case, async: true

  alias XtandardImage.{Chain, EncryptionKey, Error, Imgproxy, Query, Signing, SigningKey}
  alias XtandardImage.{Encryption, Fixtures, Range, Sources}

  @fixture Fixtures.load("clients/spec/conformance.json")
  @external_resource Fixtures.path("clients/spec/conformance.json")
  @keys @fixture["keys"]
  @cases @fixture["cases"]

  test "the fixture covers every op" do
    assert @cases |> Enum.map(& &1["op"]) |> Enum.uniq() |> length() >= 17
    assert length(@cases) >= 100
  end

  for c <- @cases do
    @case c
    test "#{c["op"]}:#{c["name"]}" do
      check(@case)
    end
  end

  defp check(%{"expect" => %{"error" => code}} = c) do
    want = String.to_existing_atom(code)

    if mapping?(c) do
      assert {:error, %Error{code: ^want}} = run_mapping(c)
    end

    case run_fluent(c) do
      :none -> assert mapping?(c), "no form expresses #{c["name"]}"
      result -> assert {:error, %Error{code: ^want}} = result
    end
  end

  defp check(%{"expect" => %{"value" => value}} = c) do
    assert run_mapping(c) == {:ok, value}

    case run_fluent(c) do
      :none -> :ok
      result -> assert result == {:ok, value}
    end
  end

  # video_url/4 takes no :pdf_password (an unknown option is an
  # ArgumentError); the fluent chain expresses that case instead.
  defp mapping?(%{"op" => "imgproxy.video_url", "input" => inp}),
    do: not Map.has_key?(inp, "pdf_password")

  defp mapping?(_c), do: true

  # ---- config ----------------------------------------------------------------

  defp signing!(cfg) do
    case cfg["signing"] do
      nil -> nil
      true -> signing_key!(@keys["signing"], cfg)
      k -> signing_key!(k, cfg)
    end
  end

  defp signing_key!(k, cfg) do
    opts = if Map.has_key?(cfg, "signature_size"), do: [size: cfg["signature_size"]], else: []
    SigningKey.new!(k["key"], k["salt"], opts)
  end

  defp encryption!(cfg) do
    case cfg["encryption"] do
      nil -> nil
      true -> EncryptionKey.new!(@keys["encryption"]["key"], iv: @keys["encryption"]["iv"])
      k -> EncryptionKey.new!(k["key"], iv: k["iv"])
    end
  end

  defp take(map, keys) do
    for k <- keys, Map.has_key?(map, k), do: {String.to_existing_atom(k), map[k]}
  end

  defp imgproxy!(cfg) do
    opts =
      take(cfg, ~w(base_url unsigned_signature source_encoding split_source separator option_names))

    Imgproxy.new([signing: signing!(cfg), encryption: encryption!(cfg)] ++ opts)
  end

  defp query!(cfg) do
    opts = take(cfg, ~w(base_url mount separator option_names))
    Query.new([signing: signing!(cfg), encryption: encryption!(cfg)] ++ opts)
  end

  # ---- options form ------------------------------------------------------------

  defp pipelines(inp) do
    case inp["options"] do
      nil -> nil
      pls -> Enum.map(pls, &pairs/1)
    end
  end

  defp pairs(pipeline), do: Enum.map(pipeline, fn [name, args] -> {name, args} end)

  # One pipeline as a plain list of pairs, several as a list of them.
  defp one(inp) do
    case pipelines(inp) do
      [single] -> single
      other -> other
    end
  end

  defp stream_opts(inp), do: take(inp, ~w(protocol renditions segment_duration segment_format))

  defp params(inp), do: Enum.map(inp["params"] || [], fn [k, v] -> {k, v} end)

  defp range(r) when is_binary(r), do: r
  defp range(%{"suffix" => n}), do: %{suffix: n}
  defp range(%{"start" => s} = r), do: %{start: s, end: r["end"]}

  defp run_mapping(c) do
    Error.wrap(fn -> mapping!(c["op"], c["config"] || %{}, c["input"]) end)
  end

  defp mapping!("imgproxy.url", cfg, inp) do
    Imgproxy.url!(
      imgproxy!(cfg),
      inp["source"],
      pipelines(inp),
      take(inp, ~w(extension pdf_password source_encoding))
    )
  end

  defp mapping!("imgproxy.info_url", cfg, inp),
    do:
      Imgproxy.info_url!(
        imgproxy!(cfg),
        inp["source"],
        one(inp),
        take(inp, ~w(pdf_password source_encoding))
      )

  defp mapping!("imgproxy.video_url", cfg, inp),
    do:
      Imgproxy.video_url!(
        imgproxy!(cfg),
        inp["source"],
        one(inp),
        take(inp, ~w(extension source_encoding))
      )

  defp mapping!("imgproxy.stream_url", cfg, inp) do
    opts = stream_opts(inp) ++ take(inp, ~w(source_encoding)) ++ [options: one(inp)]
    Imgproxy.stream_url!(imgproxy!(cfg), inp["source"], opts)
  end

  defp mapping!("query.url", cfg, inp) do
    opts = [params: params(inp), options: pipelines(inp)] ++ take(inp, ~w(extension pdf_password))
    Query.url!(query!(cfg), inp["source"], opts)
  end

  defp mapping!("query.video_url", cfg, inp),
    do: Query.video_url!(query!(cfg), inp["source"], pipelines(inp), take(inp, ~w(extension)))

  defp mapping!("query.stream_url", cfg, inp),
    do: Query.stream_url!(query!(cfg), inp["source"], stream_opts(inp) ++ [options: one(inp)])

  defp mapping!("query.imaginary_url", cfg, inp),
    do: Query.imaginary_url!(query!(cfg), inp["endpoint"], inp["source"], params(inp))

  defp mapping!("sign", cfg, inp), do: Signing.sign(inp["message"], signing!(cfg))

  defp mapping!("sign_xi_url", cfg, inp),
    do: Signing.sign_xi_url!(inp["url"], signing!(cfg), mount: inp["mount"] || "")

  defp mapping!("encrypt_source", cfg, inp),
    do: Encryption.encrypt_source!(inp["source"], encryption!(cfg))

  defp mapping!("encrypt_pdf_password", cfg, inp),
    do: Encryption.encrypt_pdf_password!(inp["password"], encryption!(cfg))

  defp mapping!("xi_source", _cfg, inp), do: Sources.xi_source!(inp["name"], inp["key"])

  defp mapping!("source_url", _cfg, inp),
    do: Sources.source_url!(inp["scheme"], inp["authority"], inp["key"])

  defp mapping!("range_header", _cfg, inp), do: Range.header!(range(inp["range"]))
  defp mapping!("escape_plain_source", _cfg, inp), do: Sources.escape_plain_source(inp["value"])
  defp mapping!("escape_query_value", _cfg, inp), do: Sources.escape_query_value(inp["value"])

  # ---- fluent form -------------------------------------------------------------

  defp run_fluent(c) do
    cfg = c["config"] || %{}
    inp = c["input"]

    # Key errors happen when the builder is made, before any chain.
    case Error.wrap(fn -> start(c["op"], cfg, inp) end) do
      {:ok, :none} -> :none
      {:ok, chain} -> chain |> fill(c["op"], inp) |> Chain.build()
      error -> error
    end
  end

  defp start("imgproxy.url", cfg, inp), do: Imgproxy.image(imgproxy!(cfg), inp["source"])
  defp start("imgproxy.info_url", cfg, inp), do: Imgproxy.info(imgproxy!(cfg), inp["source"])
  defp start("imgproxy.video_url", cfg, inp), do: Imgproxy.video(imgproxy!(cfg), inp["source"])

  defp start("query.url", cfg, inp) do
    Enum.reduce(params(inp), Query.image(query!(cfg), inp["source"]), fn {k, v}, chain ->
      Chain.param(chain, k, v)
    end)
  end

  defp start("query.video_url", cfg, inp), do: Query.video(query!(cfg), inp["source"])
  defp start(_op, _cfg, _inp), do: :none

  defp fill(chain, op, inp) do
    chain =
      (inp["options"] || [])
      |> Enum.with_index()
      |> Enum.reduce(chain, fn {pl, i}, chain ->
        # /q/video writes several option lists in order (keys may repeat).
        chain = if i > 0 and op != "query.video_url", do: Chain.pipeline(chain), else: chain
        Enum.reduce(pl, chain, fn [name, args], chain -> Chain.option(chain, name, args) end)
      end)

    chain =
      if inp["source_encoding"], do: Chain.encoding(chain, inp["source_encoding"]), else: chain

    chain = if inp["extension"], do: Chain.extension(chain, inp["extension"]), else: chain

    if Map.has_key?(inp, "pdf_password"),
      do: Chain.with_pdf_password(chain, inp["pdf_password"]),
      else: chain
  end
end
