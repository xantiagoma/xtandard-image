defmodule XtandardImage.ClientTest do
  # The HTTP client (Req, through Finch) against a stand-in server.
  use ExUnit.Case, async: true

  alias XtandardImage.{Client, Error, HTTPError, Imgproxy, InfoResponse, Readiness}
  alias XtandardImage.{QueryMethodRequest, SigningKey, StandIn, StreamManifest, TransportError}

  @body for i <- 0..999, into: <<>>, do: <<rem(i, 256)>>

  setup do
    key = SigningKey.new!("736563726574", "68656C6C6F")
    test_pid = self()
    # The signature covers the path only, so the path is the same at any base URL.
    video_path =
      Imgproxy.new(signing: key)
      |> Imgproxy.video_url!("s3://m/clip.mov", [vd: 2], extension: :webm)

    {base, listen} = StandIn.start(&handle(&1, test_pid, video_path))
    on_exit(fn -> StandIn.stop(listen) end)
    %{base: base, client: Client.new(base, signing: key, playground_path: "/_admin/play/")}
  end

  @json [{"content-type", "application/json"}]
  @fixed %{
    "/healthz" => {200, [], "ok\n"},
    "/readyz" => {503, [], "draining\n"},
    "/_admin/play/api/meta" => {200, @json, ~s({"version":"test"})},
    "/proxy-error" => {502, [], "<html>bad gateway</html>"},
    "/busy" =>
      {429, [], ~s({"error":{"kind":"overloaded","message":"too many requests","request_id":"r1"}})},
    "/drop" => :close
  }

  defp handle(req, test_pid, video_path) do
    send(test_pid, {:request, req})
    Map.get_lazy(@fixed, req.path, fn -> route(req, video_path) end)
  end

  defp route(%{path: "/info/" <> _}, _video_path),
    do: {200, @json, ~s({"width":640,"height":480,"format":"jpeg"})}

  defp route(%{path: "/video/" <> _ = path} = req, video_path) do
    cond do
      String.ends_with?(path, ".m3u8") ->
        {200, [{"content-type", "application/vnd.apple.mpegurl"}], "#EXTM3U\n/_stream/T/v0.m3u8\n"}

      path != video_path ->
        {403, @json, ~s({"error":{"kind":"unauthorized","message":"invalid signature"}})}

      true ->
        media(req)
    end
  end

  defp route(%{path: "/_stream/" <> _} = req, _video_path), do: media(req)
  defp route(%{path: "/q/" <> _}, _video_path), do: {200, [], "img"}
  defp route(%{path: "/img/q/" <> _}, _video_path), do: {200, [], "img"}

  defp route(_req, _video_path),
    do: {404, @json, ~s({"error":{"kind":"not_found","message":"source image not found"}})}

  defp media(req) do
    case Regex.run(~r/^bytes=(\d+)-(\d*)$/, req.headers["range"] || "") do
      [_, s, e] ->
        start = String.to_integer(s)
        last = if e == "", do: byte_size(@body) - 1, else: String.to_integer(e)

        {206, [{"content-type", "video/webm"}, {"content-range", "bytes #{start}-#{last}/1000"}],
         binary_part(@body, start, last - start + 1)}

      nil ->
        {200, [{"content-type", "video/webm"}], @body}
    end
  end

  defp last_request do
    receive do
      {:request, req} -> drain(req)
    after
      1_000 -> flunk("no request reached the stand-in server")
    end
  end

  defp drain(req) do
    receive do
      {:request, newer} -> drain(newer)
    after
      0 -> req
    end
  end

  test "health and readiness", %{client: c} do
    assert Client.healthz(c) == :ok
    assert Client.readyz(c) == {:ok, %Readiness{ready: false, state: :draining}}
    assert Client.readyz!(c).state == :draining
  end

  test "info", %{client: c} do
    assert {:ok, %InfoResponse{width: 640, format: "jpeg"}} =
             Client.info(c, "s3://b/cat.jpg", size: true, format: true)

    req = last_request()
    assert req.headers["accept"] == "application/json"
    assert String.starts_with?(req.path, "/info/")
    assert Client.info_at!(c, req.path).height == 480
  end

  test "video: whole, range, head", %{client: c} do
    assert {:ok, resp} = Client.video(c, "s3://m/clip.mov", [vd: 2], extension: :webm)
    assert resp.body == @body

    assert {:ok, resp} =
             Client.video(c, "s3://m/clip.mov", [vd: 2], extension: :webm, range: 10..19)

    assert resp.status == 206
    assert Req.Response.get_header(resp, "content-range") == ["bytes 10-19/1000"]
    assert resp.body == binary_part(@body, 10, 10)
    assert last_request().headers["range"] == "bytes=10-19"

    assert {:ok, resp} =
             Client.video(c, "s3://m/clip.mov", [vd: 2], extension: :webm, method: :head)

    assert last_request().method == "HEAD"
    assert Req.Response.get_header(resp, "content-type") == ["video/webm"]
    assert resp.body == ""
  end

  test "a streamed body", %{client: c} do
    parent = self()

    into = fn {:data, data}, acc ->
      send(parent, {:chunk, data})
      {:cont, acc}
    end

    assert {:ok, _} = Client.video(c, "s3://m/clip.mov", [vd: 2], extension: :webm, into: into)
    assert collect_chunks("") == @body
  end

  defp collect_chunks(acc) do
    receive do
      {:chunk, data} -> collect_chunks(acc <> data)
    after
      0 -> acc
    end
  end

  test "a bad signature is a 403", %{client: c} do
    assert {:error,
            %HTTPError{
              status: 403,
              reason: :forbidden,
              kind: "unauthorized",
              message: "invalid signature"
            }} =
             Client.video(c, "s3://m/clip.mov", [vd: 3], extension: :webm)

    assert_raise HTTPError, fn -> Client.video!(c, "s3://m/clip.mov", [vd: 3], extension: :webm) end
  end

  test "stream manifests and ticket children", %{client: c} do
    assert {:ok, %StreamManifest{} = m} =
             Client.stream(c, "s3://m/clip.mov", protocol: :hls, renditions: [360, 720])

    assert m.url =~ "/st:hls:360:720/"
    assert m.content_type == "application/vnd.apple.mpegurl"
    child = m.body |> String.split("\n") |> Enum.at(1)
    assert {:ok, resp} = Client.media(c, child, range: {:suffix, 4})
    req = last_request()
    assert req.path == "/_stream/T/v0.m3u8"
    assert req.headers["range"] == "bytes=-4"
    # The stand-in only answers start-end ranges.
    assert resp.status == 200
  end

  test "errors by status", %{client: c} do
    assert {:error, %HTTPError{reason: :not_found, kind: "not_found"}} =
             Client.image(c, "s3://b/missing.jpg")

    assert {:error,
            %HTTPError{
              reason: :gateway,
              kind: "unknown",
              message: "<html>bad gateway</html>",
              body: nil
            }} =
             Client.media(c, "/proxy-error")

    assert {:error, %HTTPError{reason: :too_many_requests, request_id: "r1"}} =
             Client.media(c, "/busy")

    assert {:error, %TransportError{}} = Client.media(c, "/drop")
  end

  test "URL errors come back before any request", %{client: c} do
    assert {:error, %Error{code: :unknown_option}} = Client.image(c, "s3://b/cat.jpg", nope: 1)

    assert {:error, %Error{code: :invalid_range}} =
             Client.media(c, "/x", range: %{start: 5, end: 1})

    refute_received {:request, _}
  end

  test "playground meta", %{client: c} do
    assert Client.playground_meta(c) == {:ok, %{"version" => "test"}}
  end

  # Every request the stand-in saw, oldest first: {method, target, content type, body}.
  defp seen(acc \\ []) do
    receive do
      {:request, r} ->
        target = if r.query == "", do: r.path, else: "#{r.path}?#{r.query}"
        seen([{r.method, target, r.headers["content-type"], r.body} | acc])
    after
      0 -> Enum.reverse(acc)
    end
  end

  describe "HTTP QUERY (opt-in)" do
    @q "/q/?url=https://e.com/a.jpg&w=300&xi_sig=abc"
    @form "application/x-www-form-urlencoded"

    test "off by default: query dialect URLs go out as GET", %{client: c} do
      assert {:ok, %{status: 200}} = Client.media(c, @q)
      assert seen() == [{"GET", @q, nil, ""}]
    end

    test "query_method sends URLs under the mount as QUERY, others as GET", %{base: base} do
      c = Client.new(base, query_method: true)
      assert {:ok, %{body: "img"}} = Client.media(c, @q)
      assert {:ok, _} = Client.media(c, base <> "/q/video?url=s3://m/a.mov&vd=5")
      assert {:ok, _} = Client.media(c, "/_stream/T/v0.m3u8")
      assert {:error, %HTTPError{status: 404}} = Client.media(c, "/qq?url=x")
      assert {:ok, %{body: ""}} = Client.media(c, "/q/?url=x", method: :head)
      assert {:ok, _} = Client.media(c, "/q/?url=x", method: :get)

      assert seen() == [
               {"QUERY", "/q/", @form, "url=https://e.com/a.jpg&w=300&xi_sig=abc"},
               {"QUERY", "/q/video", @form, "url=s3://m/a.mov&vd=5"},
               {"GET", "/_stream/T/v0.m3u8", nil, ""},
               {"GET", "/qq?url=x", nil, ""},
               {"HEAD", "/q/?url=x", nil, ""},
               {"GET", "/q/?url=x", nil, ""}
             ]

      # A URL under the mount without a query cannot be a QUERY request.
      assert {:error, %Error{code: :invalid_request}} = Client.media(c, "/q/")
      assert_raise Error, fn -> Client.media!(c, "/q/") end
      assert seen() == []
    end

    test "method: :query on one call, with a custom mount", %{base: base} do
      c = Client.new(base, query_mount: "/img/q/")
      assert {:ok, %{body: "img"}} = Client.media(c, "/img/q/resize?url=x&width=1", method: :query)
      assert {:ok, _} = Client.media(c, "/img/q/resize?url=x")

      assert seen() == [
               {"QUERY", "/img/q/resize", @form, "url=x&width=1"},
               {"GET", "/img/q/resize?url=x", nil, ""}
             ]
    end

    test "query_method_request splits a built URL" do
      assert {:ok, q} =
               XtandardImage.query_method_request(
                 "https://xi.test/q/watermark?url=a&text=50%25%20off"
               )

      assert {q.method, q.url, q.content_type, q.body} ==
               {"QUERY", "https://xi.test/q/watermark", @form, "url=a&text=50%25%20off"}

      assert QueryMethodRequest.from_url!("/q/?a=1?b").body == "a=1?b"
      assert QueryMethodRequest.content_type() == @form

      for bad <- ["/q/", "/q/?", "/q/?url=x#frag"] do
        assert {:error, %Error{code: :invalid_request}} = QueryMethodRequest.from_url(bad)
      end

      assert_raise Error, fn -> XtandardImage.query_method_request!("/q/") end
    end
  end

  test "a refused connection is a transport error" do
    {:ok, listen} = :gen_tcp.listen(0, ip: {127, 0, 0, 1})
    {:ok, port} = :inet.port(listen)
    :gen_tcp.close(listen)
    c = Client.new("http://127.0.0.1:#{port}")
    assert {:error, %TransportError{reason: :econnrefused}} = Client.healthz(c)
    assert_raise TransportError, fn -> Client.healthz!(c) end
  end
end
