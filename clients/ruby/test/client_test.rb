# frozen_string_literal: true

# The HTTP client against a stand-in server (a local TCPServer).

require_relative "test_helper"
require_relative "support/stand_in_server"

class ClientTest < Minitest::Test
  X = XtandardImage
  KEY = X::SigningKey.new(key: "736563726574", salt: "68656C6C6F")
  BODY = (0...1000).map { |i| (i % 256).chr }.join.b
  VIDEO_PATH = X::ImgproxyURLBuilder.new(signing: KEY).video_url("s3://m/clip.mov", { vd: 2 }, extension: "webm")

  def self.handle(req)
    path = req["path"]
    target = req["target"]
    range = req["headers"]["range"].to_s
    envelope = ->(kind, message, extra = {}) { JSON.generate({ error: { kind: kind, message: message, **extra } }) }
    json = { "Content-Type" => "application/json" }
    case path
    when "/healthz" then [200, {}, "ok\n"]
    when "/readyz" then [503, {}, "draining\n"]
    when %r{\A/info/} then [200, json, JSON.generate({ width: 640, height: 480, format: "jpeg" })]
    when %r{\A/video/.*\.m3u8\z}
      [200, { "Content-Type" => "application/vnd.apple.mpegurl" }, "#EXTM3U\n/_stream/T/v0.m3u8\n"]
    when %r{\A/(video|_stream)/}
      if path.start_with?("/video/") && target != VIDEO_PATH
        return [403, json, envelope.call("unauthorized", "invalid signature")]
      end

      m = /\Abytes=(\d+)-(\d*)\z/.match(range)
      return [200, { "Content-Type" => "video/webm" }, BODY] if m.nil?

      start = m[1].to_i
      last = m[2].to_s.empty? ? BODY.bytesize - 1 : m[2].to_i
      [206, { "Content-Type" => "video/webm", "Content-Range" => "bytes #{start}-#{last}/#{BODY.bytesize}" },
       BODY.byteslice(start..last),]
    when "/_admin/play/api/meta" then [200, json, JSON.generate({ version: "test" })]
    when "/proxy-error" then [502, { "Content-Type" => "text/html" }, "<html>bad gateway</html>"]
    when "/busy" then [429, json, envelope.call("overloaded", "too many requests", request_id: "r1")]
    when %r{\A/(img/)?q(/|\z)} then [200, {}, "img"]
    else [404, json, envelope.call("not_found", "source image not found")]
    end
  end

  SERVER = StandInServer.new { |req| handle(req) }
  Minitest.after_run { SERVER.close }

  def setup
    SERVER.seen
    @xi = X::Client.new(SERVER.base_url, signing: KEY, playground_path: "/_admin/play/")
  end

  def last_request = SERVER.seen.last

  def test_health
    assert_nil @xi.healthz
    r = @xi.readyz
    refute_predicate r, :ready?
    assert_equal "draining", r.state
  end

  def test_info
    info = @xi.info("s3://b/cat.jpg", { size: true, format: true })
    assert_equal 640, info["width"]
    req = last_request
    assert_equal "application/json", req["headers"]["accept"]
    assert req["path"].start_with?("/info/")
    assert_equal "jpeg", @xi.info_at(req["target"])["format"]
  end

  def test_video_whole_range_head
    res = @xi.video("s3://m/clip.mov", { vd: 2 }, extension: "webm")
    assert_equal BODY, res.body
    assert_equal Encoding::BINARY, res.body.encoding

    res = @xi.video("s3://m/clip.mov", { vd: 2 }, extension: "webm", range: 10..19)
    assert_equal [206, "bytes 10-19/1000"], [res.status, res["Content-Range"]]
    assert_equal BODY.byteslice(10, 10), res.body
    assert_equal "bytes=10-19", last_request["headers"]["range"]

    res = @xi.video("s3://m/clip.mov", { vd: 2 }, extension: "webm", method: :head)
    assert_equal "HEAD", last_request["method"]
    assert_nil res.body
    assert_equal "video/webm", res.content_type
  end

  def test_video_streamed_to_a_block
    chunks = []
    res = @xi.video("s3://m/clip.mov", { vd: 2 }, extension: "webm") { |c| chunks << c }
    assert_equal BODY, chunks.join.b
    assert_nil res.body
    assert_equal 200, res.status
  end

  def test_block_errors_propagate_untouched
    err = assert_raises(IOError) do
      @xi.video("s3://m/clip.mov", { vd: 2 }, extension: "webm") { |_| raise IOError, "disk full" }
    end
    assert_equal "disk full", err.message
  end

  def test_bad_signature_is_forbidden
    e = assert_raises(X::ForbiddenError) { @xi.video("s3://m/clip.mov", { vd: 3 }, extension: "webm") }
    assert_equal [403, "unauthorized", "invalid signature"], [e.status, e.kind, e.message]
    assert e.url.start_with?(SERVER.base_url)
  end

  def test_error_body_is_not_streamed_to_the_block
    called = false
    assert_raises(X::NotFoundError) { @xi.image("s3://b/missing.jpg") { |_| called = true } }
    refute called
  end

  def test_stream_and_ticket_child
    m = @xi.stream("s3://m/clip.mov", protocol: :hls, renditions: [360, 720])
    assert_includes m.url, "/st:hls:360:720/"
    assert_equal "application/vnd.apple.mpegurl", m.content_type
    assert_equal Encoding::UTF_8, m.body.encoding
    child = m.body.lines(chomp: true)[1]
    res = @xi.media(child, range: X::ByteSuffix.new(length: 4))
    req = last_request
    assert_equal "/_stream/T/v0.m3u8", req["path"]
    assert_equal "bytes=-4", req["headers"]["range"]
    assert_equal 200, res.status # the stand-in only answers start-end ranges
  end

  def test_errors_by_status
    nf = assert_raises(X::NotFoundError) { @xi.image("s3://b/missing.jpg") }
    assert_equal "not_found", nf.kind
    gw = assert_raises(X::GatewayError) { @xi.media("/proxy-error") }
    assert_equal ["unknown", "<html>bad gateway</html>", nil], [gw.kind, gw.message, gw.body]
    busy = assert_raises(X::TooManyRequestsError) { @xi.media("/busy") }
    assert_equal "r1", busy.request_id
    assert_operator X::ServiceUnavailableError, :<, X::ServerError
    assert_operator X::ServerError, :<, X::HTTPError
    assert_operator X::HTTPError, :<, X::Error
    assert_operator X::URLBuildError, :<, X::Error
  end

  def test_transport_error
    port = TCPServer.open("127.0.0.1", 0) { |s| s.addr[1] } # a port nobody listens on
    err = assert_raises(X::TransportError) { X::Client.new("http://127.0.0.1:#{port}", open_timeout: 2).healthz }
    assert_kind_of SystemCallError, err.cause
  end

  def test_headers_and_accept
    @xi.image("s3://b/cat.jpg", { w: 10 }, accept: "image/avif,image/webp", headers: { "X-Trace" => "t1" })
    h = last_request["headers"]
    assert_equal ["image/avif,image/webp", "t1"], [h["accept"], h["x-trace"]]
  rescue X::NotFoundError
    h = last_request["headers"]
    assert_equal ["image/avif,image/webp", "t1"], [h["accept"], h["x-trace"]]
  end

  def test_playground_meta
    assert_equal({ "version" => "test" }, @xi.playground_meta)
  end

  def test_range_header
    assert_equal "bytes=0-", X.range_header(0..)
    assert_equal "bytes=5-9", X.range_header(5..9)
    assert_equal "bytes=5-9", X.range_header(5...10)
    assert_equal "bytes=-100", X.range_header(X::ByteSuffix.new(length: 100))
    assert_equal "bytes=1-2", X.range_header("bytes=1-2")
    assert_equal "bytes=3-", X.range_header({ start: 3 })
    [5..4, 5...5, -1.., X::ByteSuffix.new(length: -1), { start: 1.5 }, 42].each do |bad|
      assert_equal :invalid_range, assert_raises(X::URLBuildError) { X.range_header(bad) }.code
    end
  end

  def seen_query = SERVER.seen.map { |r| [r["method"], r["target"], r["headers"]["content-type"], r["body"]] }

  def test_query_method_off_by_default
    q = "/q/?url=https://e.com/a.jpg&w=300&xi_sig=abc"
    assert_equal 200, @xi.media(q).status
    assert_equal [["GET", q, nil, ""]], seen_query
  end

  def test_query_method_option
    xi = X::Client.new(SERVER.base_url, query_method: true)
    form = X::QUERY_CONTENT_TYPE
    xi.media("/q/?url=https://e.com/a.jpg&w=300&xi_sig=abc")
    xi.media("#{SERVER.base_url}/q/video?url=s3://m/a.mov&vd=5")
    xi.media("/_stream/T/v0.m3u8")
    assert_raises(X::NotFoundError) { xi.media("/qq?url=x") }
    res = xi.media("/q/?url=x", method: :head)
    xi.media("/q/?url=x", method: :get)
    assert_nil res.body
    assert_equal [
      ["QUERY", "/q/", form, "url=https://e.com/a.jpg&w=300&xi_sig=abc"],
      ["QUERY", "/q/video", form, "url=s3://m/a.mov&vd=5"],
      ["GET", "/_stream/T/v0.m3u8", nil, ""],
      ["GET", "/qq?url=x", nil, ""],
      ["HEAD", "/q/?url=x", nil, ""],
      ["GET", "/q/?url=x", nil, ""],
    ], seen_query
    # A URL under the mount without a query cannot be a QUERY request.
    assert_equal :invalid_request, assert_raises(X::URLBuildError) { xi.media("/q/") }.code
  end

  def test_query_method_per_call_with_custom_mount
    xi = X::Client.new(SERVER.base_url, query_mount: "/img/q/")
    res = xi.media("/img/q/resize?url=x&width=1", method: "QUERY")
    xi.media("/img/q/resize?url=x")
    assert_equal ["img", "#{SERVER.base_url}/img/q/resize"], [res.body, res.url]
    assert_equal [
      ["QUERY", "/img/q/resize", X::QUERY_CONTENT_TYPE, "url=x&width=1"],
      ["GET", "/img/q/resize?url=x", nil, ""],
    ], seen_query
  end

  def test_query_method_request
    q = X.query_method_request("https://xi.test/q/watermark?url=a&text=50%25%20off")
    assert_equal ["QUERY", "https://xi.test/q/watermark", "application/x-www-form-urlencoded", "url=a&text=50%25%20off"],
                 [q.http_method, q.url, q.content_type, q.body]
    assert_equal "a=1?b", X::QueryMethodRequest.from_url("/q/?a=1?b").body
    ["/q/", "/q/?", "/q/?url=x#frag"].each do |bad|
      assert_equal :invalid_request, assert_raises(X::URLBuildError) { X.query_method_request(bad) }.code, bad
    end
  end

  def test_client_builds_urls_with_its_settings
    xi = X::Client.new("http://xi.test/", source_encoding: :plain)
    assert_equal "http://xi.test", xi.base_url
    assert_equal "http://xi.test/insecure/w:10/plain/s3://b/a.jpg", xi.urls.url("s3://b/a.jpg", { w: 10 })
    assert_raises(ArgumentError) { xi.media("/x", method: :post) }
  end
end
