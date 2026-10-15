# frozen_string_literal: true

# Unit tests of the builders beyond the shared fixtures: Ruby-specific forms and number formatting.

require_relative "test_helper"

class BuilderTest < Minitest::Test
  X = XtandardImage
  SRC = "https://example.com/a.jpg"

  NUMBERS = [
    [0, "0"], [-0.0, "0"], [300, "300"], [300.0, "300"], [1.5, "1.5"], [-1.5, "-1.5"], [0.1, "0.1"],
    [0.000001, "0.000001"], [1e-7, "1e-7"], [1.5e-9, "1.5e-9"], [123_456_789.125, "123456789.125"],
    [1e20, "100000000000000000000"], [1e21, "1e+21"], [1.5e22, "1.5e+22"], [10**21, "1e+21"],
    [(2**53) + 1, "9007199254740993"], [0.30000000000000004, "0.30000000000000004"], [1e16, "10000000000000000"],
    [0.00001, "0.00001"], [1.23e-18, "1.23e-18"], [-5e-324, "-5e-324"], [1.7976931348623157e308, "1.7976931348623157e+308"],
  ].freeze

  def test_format_number_matches_ecmascript
    NUMBERS.each { |v, want| assert_equal want, X::Text.format_number(v, "x"), v.inspect }
  end

  def test_format_number_rejects_non_finite_and_other_types
    [Float::INFINITY, -Float::INFINITY, Float::NAN, 1r, "1"].each do |v|
      assert_equal :invalid_argument, assert_raises(X::URLBuildError) { X::Text.format_number(v, "x") }.code
    end
  end

  def test_bare_values_symbols_and_nil
    url = X.imgproxy.url(SRC, { rs: [:fill, 300, 400], q: 80, "g" => "sm", bl: nil })
    assert_equal "/insecure/rs:fill:300:400/q:80/g:sm/aHR0cHM6Ly9leGFtcGxlLmNvbS9hLmpwZw", url
    assert_equal "/insecure/rs:fill:300::1/aHR0cHM6Ly9leGFtcGxlLmNvbS9hLmpwZw",
                 X.imgproxy.url(SRC, { rs: ["fill", 300, nil, true] })
  end

  def test_chained_hashes
    url = X.imgproxy(source_encoding: :plain).url(SRC, [{ rs: ["fit", 100, 100] }, { bl: 2 }])
    assert_equal "/insecure/rs:fit:100:100/-/bl:2/plain/https://example.com/a.jpg", url
  end

  def test_fluent_methods_use_the_name_called
    b = X.imgproxy(option_names: :given, source_encoding: "plain")
    assert_equal "/insecure/resize:fit:1:2/q:90/min-width:10/plain/https://example.com/a.jpg",
                 b.image(SRC).resize("fit", 1, 2).q(90).min_width(10).build
  end

  def test_keyword_and_object_method_names
    b = X.imgproxy(option_names: :given, source_encoding: :plain)
    assert_equal "/insecure/format:png/plain/https://example.com/a.jpg", b.image(SRC).format("png").to_s
    assert_equal "/insecure/ex:1/plain/https://example.com/a.jpg", b.image(SRC).ex(true).to_s
    assert_equal "/insecure/extend:1/plain/https://example.com/a.jpg", b.image(SRC).extend(true).to_s
  end

  def test_fluent_to_s_and_options
    assert_equal X.imgproxy.url(SRC, { w: 100, h: 50 }), X.imgproxy.image(SRC).w(100).options({ h: 50 }).to_s
    assert_equal "/insecure/w:1/-/h:2/aHR0cHM6Ly9leGFtcGxlLmNvbS9hLmpwZw",
                 X.imgproxy.image(SRC).w(1).pipeline.h(2).to_s
  end

  def test_video_and_stream
    b = X::ImgproxyURLBuilder.new(source_encoding: :plain)
    assert_equal "/video/insecure/rs:fill:640:360/vs:1.5/vd:5/plain/s3://m/a.mov@webm",
                 b.video_url("s3://m/a.mov", { rs: ["fill", 640, 360], vs: 1.5, vd: 5 }, extension: "webm")
    assert_equal "/video/insecure/vs:2/mu:1/plain/s3://m/a.mov@mp4",
                 b.video("s3://m/a.mov").start(2).mute(true).extension(:mp4).build
    assert_equal "/video/insecure/st:dash:360/plain/s3://m/a.mov@mpd",
                 b.stream_url("s3://m/a.mov", protocol: :dash, renditions: [360])
  end

  def test_query_fluent_and_hash_params
    q = X::QueryURLBuilder.new
    assert_equal "/q/?url=https://example.com/a.jpg&w=300&enlarge=true", q.url(SRC, params: { w: 300, enlarge: true })
    assert_equal "/q/?url=https://example.com/a.jpg&w=300&o=bl:2", q.image(SRC).param(:w, 300).bl(2).build
    assert_equal "/q/resize?url=https://example.com/a.jpg&width=10", X.query_urls.imaginary_url(:resize, SRC, { width: 10 })
  end

  def test_unsupported_argument_and_options_types
    assert_equal :invalid_argument, assert_raises(X::URLBuildError) { X.imgproxy.url(SRC, { w: Object.new }) }.code
    assert_equal :invalid_argument, assert_raises(X::URLBuildError) { X.imgproxy.url(SRC, "w:1") }.code
    assert_equal :unknown_option, assert_raises(X::URLBuildError) { X.imgproxy.image(SRC).option("nope", 1).build }.code
  end

  def test_invalid_option_names_setting
    assert_raises(ArgumentError) { X.imgproxy(option_names: :long) }
  end

  def test_split_source
    url = X.imgproxy(split_source: 8).url(SRC)
    assert_equal "/insecure/aHR0cHM6/Ly9leGFt/cGxlLmNv/bS9hLmpw/Zw", url
  end

  def test_strings_are_frozen_literals_safe
    url = X.imgproxy.url(SRC.dup.freeze, { w: 1 })
    refute_predicate url, :frozen?
  end
end
