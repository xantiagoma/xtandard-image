defmodule XtandardImage.BuilderTest do
  # Unit tests beyond the shared fixtures: the Elixir forms (keyword lists,
  # maps, tuples, atoms, bare values, nil), number formatting and doctests.
  use ExUnit.Case, async: true

  alias XtandardImage.{Chain, Error, Imgproxy, Query, Text}
  alias XtandardImage.Processing, as: P
  alias XtandardImage.Video, as: V

  doctest XtandardImage
  doctest XtandardImage.Imgproxy
  doctest XtandardImage.Query
  doctest XtandardImage.QueryMethodRequest
  doctest XtandardImage.Chain
  doctest XtandardImage.Processing
  doctest XtandardImage.Info
  doctest XtandardImage.Video
  doctest XtandardImage.Signing
  doctest XtandardImage.Encryption
  doctest XtandardImage.Sources
  doctest XtandardImage.SigningKey
  doctest XtandardImage.EncryptionKey
  doctest XtandardImage.Range
  doctest XtandardImage.HTTPError
  doctest XtandardImage.Spec

  @src "https://example.com/a.jpg"

  describe "format_number/1 matches ECMAScript Number::toString" do
    for {value, want} <- [
          {0, "0"},
          {-0.0, "0"},
          {300, "300"},
          {300.0, "300"},
          {1.5, "1.5"},
          {-1.5, "-1.5"},
          {0.1, "0.1"},
          {0.000001, "0.000001"},
          {1.0e-7, "1e-7"},
          {1.5e-9, "1.5e-9"},
          {123_456_789.125, "123456789.125"},
          {1.0e20, "100000000000000000000"},
          {1.0e21, "1e+21"},
          {1.5e22, "1.5e+22"},
          {1_000_000_000_000_000_000_000, "1e+21"},
          {9_007_199_254_740_993, "9007199254740993"},
          {0.30000000000000004, "0.30000000000000004"},
          {-2.5e-8, "-2.5e-8"}
        ] do
      @value value
      @want want
      test "#{inspect(value)}" do
        assert Text.format_number(@value) == @want
      end
    end
  end

  test "keyword lists, tuples, atoms, bare values and nil" do
    opts = [rs: {:fill, 300, 400}, q: 80, g: :sm, bl: nil]

    assert Imgproxy.url(XtandardImage.imgproxy(), @src, opts) ==
             {:ok, "/insecure/rs:fill:300:400/q:80/g:sm/aHR0cHM6Ly9leGFtcGxlLmNvbS9hLmpwZw"}
  end

  test "lists, string keys and maps are accepted" do
    img = XtandardImage.imgproxy()
    want = Imgproxy.url!(img, @src, rs: {:fit, 10, 10})
    assert Imgproxy.url!(img, @src, rs: ["fit", 10, 10]) == want
    assert Imgproxy.url!(img, @src, [{"resize", ["fit", 10, 10]}]) == want
    assert Imgproxy.url!(img, @src, %{"rs" => {:fit, 10, 10}}) == want
    assert Imgproxy.url!(img, @src, [{:"min-width", 10}]) =~ "/mw:10/"
  end

  test "an empty argument and a null argument" do
    assert Imgproxy.url!(XtandardImage.imgproxy(), @src, rs: {:fill, 300, nil, true}) =~
             "/rs:fill:300::1/"
  end

  test "chained pipelines as a list of keyword lists" do
    url =
      Imgproxy.url!(XtandardImage.imgproxy(source_encoding: :plain), @src, [
        [rs: {:fit, 100, 100}],
        [bl: 2]
      ])

    assert url == "/insecure/rs:fit:100:100/-/bl:2/plain/https://example.com/a.jpg"
  end

  test "fluent functions use the name called, and nil skips an option" do
    url =
      XtandardImage.imgproxy(option_names: :given, source_encoding: :plain)
      |> Imgproxy.image(@src)
      |> P.resize([:fit, 1, 2])
      |> P.q(90)
      |> P.quality(nil)
      |> P.min_width(10)
      |> P.fn_("report")
      |> P.build!()

    assert url ==
             "/insecure/resize:fit:1:2/q:90/min-width:10/fn:report/plain/https://example.com/a.jpg"
  end

  test "to_string builds the chain" do
    chain = XtandardImage.imgproxy() |> Imgproxy.image(@src) |> P.w(100)
    assert to_string(chain) == Imgproxy.url!(XtandardImage.imgproxy(), @src, w: 100)
    assert "#{chain}" == to_string(chain)
  end

  test "a chain does not raise until build" do
    chain = XtandardImage.imgproxy() |> Imgproxy.image(@src) |> Chain.option(:nope, 1)
    assert {:error, %Error{code: :unknown_option}} = Chain.build(chain)
    assert_raise Error, ~r/unknown imgproxy option/, fn -> to_string(chain) end
  end

  test "Chain.options/2 appends a keyword list or a map" do
    url =
      XtandardImage.imgproxy() |> Imgproxy.image(@src) |> Chain.options(w: 1, h: 2) |> P.build!()

    assert url =~ "/w:1/h:2/"

    assert {:error, %Error{code: :invalid_argument}} =
             XtandardImage.imgproxy()
             |> Imgproxy.image(@src)
             |> Chain.options([:bad])
             |> Chain.build()
  end

  test "video, audio and streams" do
    b = XtandardImage.imgproxy(source_encoding: :plain)

    assert Imgproxy.video_url!(b, "s3://m/a.mov", [rs: {:fill, 640, 360}, vs: 1.5, vd: 5],
             extension: :webm
           ) ==
             "/video/insecure/rs:fill:640:360/vs:1.5/vd:5/plain/s3://m/a.mov@webm"

    assert b
           |> Imgproxy.video("s3://m/a.mov")
           |> V.start(2)
           |> V.mute(true)
           |> V.extension(:mp4)
           |> V.build!() ==
             "/video/insecure/vs:2/mu:1/plain/s3://m/a.mov@mp4"

    assert Imgproxy.stream_url!(b, "s3://m/a.mov", protocol: :dash, renditions: [360]) ==
             "/video/insecure/st:dash:360/plain/s3://m/a.mov@mpd"

    assert {:error, %Error{code: :invalid_request}} =
             Imgproxy.stream_url(b, "s3://m/a.mov", protocol: :hls, segment_format: :nope)
  end

  test "query dialect forms" do
    q = XtandardImage.query_urls()

    assert Query.url!(q, @src, params: [w: 300, enlarge: true, fit: :cover]) ==
             "/q/?url=https://example.com/a.jpg&w=300&enlarge=true&fit=cover"

    assert q |> Query.image(@src) |> P.param("w", 300) |> P.bl(2) |> P.build!() ==
             "/q/?url=https://example.com/a.jpg&w=300&o=bl:2"

    assert Query.imaginary_url!(q, :resize, @src, %{width: 10}) ==
             "/q/resize?url=https://example.com/a.jpg&width=10"

    assert q |> Query.video("s3://m/a.mov") |> V.crop([100, 100]) |> V.extension(:mp4) |> V.build!() ==
             "/q/video?url=s3://m/a.mov&c=100,100&format=mp4"
  end

  test "misused chains are invalid requests" do
    img = XtandardImage.imgproxy()

    assert {:error, %Error{code: :invalid_request}} =
             img |> Imgproxy.image(@src) |> P.param(:w, 1) |> P.build()

    assert {:error, %Error{code: :invalid_request}} =
             XtandardImage.query_urls() |> Query.image(@src) |> P.encoding(:plain) |> P.build()

    assert {:error, %Error{code: :invalid_request}} =
             XtandardImage.query_urls()
             |> Query.video(@src)
             |> V.vs(1)
             |> Chain.pipeline()
             |> V.vd(2)
             |> V.build()
  end

  test "unsupported arguments" do
    for bad <- [%{}, self(), [[1]], {:nested, [1]}] do
      assert {:error, %Error{code: :invalid_argument}} =
               Imgproxy.url(XtandardImage.imgproxy(), @src, w: bad)
    end

    assert {:error, %Error{code: :invalid_argument}} =
             Imgproxy.url(XtandardImage.imgproxy(), @src, :not_options)

    assert {:error, %Error{code: :invalid_argument}} =
             Query.url(XtandardImage.query_urls(), @src, params: [w: %{}])
  end

  test "argument characters: unicode whitespace and BOM are refused" do
    for bad <- ["a\u00A0b", "a\u2028b", "a\uFEFFb", <<0xFF>>] do
      assert {:error, %Error{code: :invalid_argument}} =
               Imgproxy.url(XtandardImage.imgproxy(), @src, fn: bad)
    end
  end

  test "unknown config and URL options raise" do
    assert_raise KeyError, fn -> XtandardImage.imgproxy(nope: 1) end
    assert_raise ArgumentError, fn -> Imgproxy.url(XtandardImage.imgproxy(), @src, [], nope: 1) end
  end

  test "generated constants" do
    assert XtandardImage.Spec.spec_version() == 1
    assert "webp" in XtandardImage.Spec.image_formats()
    assert XtandardImage.Spec.error_status()["not_found"] == 404
  end

  test "responses decode into structs" do
    info =
      XtandardImage.InfoResponse.from_map(%{
        "width" => 640,
        "average" => %{"R" => 1, "G" => 2, "B" => 3, "A" => 255},
        "palette" => [%{"R" => 9, "G" => 9, "B" => 9, "A" => 1}],
        "exif" => %{"Make" => "X"},
        "unknown" => true
      })

    assert info.width == 640
    assert info.average == %XtandardImage.InfoRGBA{r: 1, g: 2, b: 3, a: 255}
    assert [%XtandardImage.InfoRGBA{r: 9}] = info.palette
    assert info.exif == %{"Make" => "X"}
    assert info.objects == nil
  end
end
