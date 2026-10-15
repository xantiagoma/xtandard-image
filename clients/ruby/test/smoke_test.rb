# frozen_string_literal: true

# Smoke test against a running server, off unless XI_SMOKE_URL is set.
#
# The playground demo (compose.playground.yml, unsigned URLs allowed) works:
#
#   XI_SMOKE_URL=http://127.0.0.1:8080 ruby -Ilib -Itest test/smoke_test.rb
#
# It needs the demo's local fixtures (xi://local/…) and, for the /video
# cases, a full or gpl image with XI_VIDEO_TRANSFORM_ENABLED.

require_relative "test_helper"

class SmokeTest < Minitest::Test
  X = XtandardImage
  BASE = ENV.fetch("XI_SMOKE_URL", "")
  IMAGE = X.xi_source("local", "jpeg/baseline.jpg")
  VIDEO = X.xi_source("local", "video/bbb-h264-aac.mp4")

  def setup
    skip "set XI_SMOKE_URL to run against a server" if BASE.empty?
  end

  def test_health_and_image
    xi = X::Client.new(BASE, source_encoding: :plain)
    assert_nil xi.healthz
    assert_predicate xi.readyz, :ready?
    res = xi.image(IMAGE, { rs: ["fit", 64, 64] }, extension: "webp")
    assert_equal "image/webp", res.content_type
    assert_equal "RIFF", res.body.byteslice(0, 4)
    info = xi.info(IMAGE, { size: true, format: true, dimensions: true })
    assert_equal "jpeg", info["format"]
    assert_operator info["size"], :>, 0
    assert_raises(X::NotFoundError) { xi.image(X.xi_source("local", "jpeg/missing.jpg")) }
  end

  def test_query_dialect
    url = X::QueryURLBuilder.new(base_url: BASE).url(IMAGE, params: { w: 32, f: "png" })
    assert_equal "image/png", X::Client.new(BASE).media(url).content_type
  end

  def test_video_range_head_and_stream
    xi = X::Client.new(BASE)
    res = xi.video(VIDEO, { vd: 1, w: 160 }, extension: "webm", range: 0..99)
    assert_includes [200, 206], res.status
    head = xi.video(VIDEO, { vd: 1, w: 160 }, extension: "webm", method: :head)
    assert head.content_type.start_with?("video/webm")
    bytes = 0
    xi.video(VIDEO, { vd: 1, w: 160 }, extension: "webm") { |chunk| bytes += chunk.bytesize }
    assert_operator bytes, :>, 0
    m = xi.stream(VIDEO, protocol: :hls, renditions: [240])
    assert m.body.start_with?("#EXTM3U")
    child = m.body.lines(chomp: true).find { |l| !l.empty? && !l.start_with?("#") }
    assert_equal 200, xi.media(child).status
  end
end
