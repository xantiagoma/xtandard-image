defmodule XtandardImage.SmokeTest do
  # Against a running server, off unless XI_SMOKE_URL is set. The playground
  # demo (compose.playground.yml, unsigned URLs allowed) works:
  #
  #   XI_SMOKE_URL=http://127.0.0.1:8080 mix test test/smoke_test.exs
  #
  # It needs the demo's local fixtures (xi://local/…) and, for the /video
  # cases, an image with XI_VIDEO_TRANSFORM_ENABLED.
  use ExUnit.Case, async: true

  alias XtandardImage.{Client, HTTPError, InfoResponse, Query}

  @moduletag :smoke

  setup do
    base = System.fetch_env!("XI_SMOKE_URL")

    %{
      base: base,
      image: XtandardImage.xi_source!("local", "jpeg/baseline.jpg"),
      video: XtandardImage.xi_source!("local", "video/bbb-h264-aac.mp4")
    }
  end

  test "health, image and info", %{base: base, image: image} do
    xi = Client.new(base, source_encoding: :plain)
    assert Client.healthz(xi) == :ok
    assert Client.readyz!(xi).ready

    resp = Client.image!(xi, image, [rs: {:fit, 64, 64}], extension: :webp)
    assert Req.Response.get_header(resp, "content-type") == ["image/webp"]

    assert {:ok, %InfoResponse{format: "jpeg"} = info} =
             Client.info(xi, image, size: true, format: true, dimensions: true)

    assert info.size > 0

    assert {:error, %HTTPError{reason: :not_found}} =
             Client.image(xi, XtandardImage.xi_source!("local", "jpeg/missing.jpg"))
  end

  test "query dialect", %{base: base, image: image} do
    url = XtandardImage.query_urls(base_url: base) |> Query.url!(image, params: [w: 32, f: :png])
    resp = base |> Client.new() |> Client.media!(url)
    assert Req.Response.get_header(resp, "content-type") == ["image/png"]
  end

  test "video, range, head and a stream", %{base: base, video: video} do
    xi = Client.new(base, req_options: [receive_timeout: 120_000])

    resp = Client.video!(xi, video, [vd: 1, w: 160], extension: :webm, range: 0..99)
    assert resp.status in [200, 206]

    head = Client.video!(xi, video, [vd: 1, w: 160], extension: :webm, method: :head)
    assert [type] = Req.Response.get_header(head, "content-type")
    assert String.starts_with?(type, "video/webm")

    manifest = Client.stream!(xi, video, protocol: :hls, renditions: [240])
    assert String.starts_with?(manifest.body, "#EXTM3U")

    child =
      manifest.body
      |> String.split("\n", trim: true)
      |> Enum.find(&(not String.starts_with?(&1, "#")))

    assert Client.media!(xi, child).status == 200
  end
end
