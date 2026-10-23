# frozen_string_literal: true

# clients/spec/conformance.json: the cases every official client runs (ADR 0013).
#
# Positive cases must produce expect.value byte for byte; error cases must
# raise URLBuildError with expect.error as its code. The fixture's options
# are pipelines of [name, args] pairs; they are built twice here, as option
# Hashes (url and friends) and through the fluent chains (option), and both
# must agree.

require_relative "test_helper"

class ConformanceTest < Minitest::Test
  FIXTURE = TestHelper.load_json("clients/spec/conformance.json")
  KEYS = FIXTURE.fetch("keys")
  CASES = FIXTURE.fetch("cases")
  X = XtandardImage

  # imgproxy.video_url with a pdf_password cannot be written through the
  # public API: video_url has no pdf_password keyword (Ruby raises
  # ArgumentError) and the /video chain has no with_pdf_password. The case
  # runs on URLs.imgproxy, the assembly video_url calls (see .video_url).

  def self.signing(cfg)
    s = cfg["signing"]
    return nil if s.nil?

    k = s == true ? KEYS.fetch("signing") : s
    size = cfg.fetch("signature_size", X::DEFAULT_SIGNATURE_SIZE)
    X::SigningKey.new(key: k.fetch("key"), salt: k.fetch("salt"), size: size)
  end

  def self.encryption(cfg)
    e = cfg["encryption"]
    return nil if e.nil?

    k = e == true ? KEYS.fetch("encryption") : e
    X::EncryptionKey.new(key: k.fetch("key"), iv: k["iv"])
  end

  def self.kwargs(cfg, keys)
    keys.filter_map { |k| [k.to_sym, cfg[k]] if cfg.key?(k) }.to_h
  end

  def self.imgproxy(cfg)
    X::ImgproxyURLBuilder.new(
      signing: signing(cfg), encryption: encryption(cfg),
      **kwargs(cfg, %w[base_url unsigned_signature source_encoding split_source separator option_names]),
    )
  end

  def self.query(cfg)
    X::QueryURLBuilder.new(signing: signing(cfg), encryption: encryption(cfg),
                           **kwargs(cfg, %w[base_url mount separator option_names]),)
  end

  # The fixture's pipelines as option Hashes (symbol keys, as Ruby code writes them).
  def self.hashes(inp)
    inp["options"]&.map { |pl| pl.to_h { |name, args| [name.to_sym, args] } }
  end

  def self.one(inp)
    h = hashes(inp)
    h && h.length == 1 ? h.first : h
  end

  def self.stream_kw(inp)
    kwargs(inp, %w[protocol renditions segment_duration segment_format])
  end

  def self.range(r)
    r.is_a?(Hash) && r.key?("suffix") && r.length == 1 ? X::ByteSuffix.new(length: r["suffix"]) : r
  end

  def self.video_url(cfg, inp)
    b = imgproxy(cfg)
    unless inp.key?("pdf_password")
      return b.video_url(inp["source"], one(inp), extension: inp["extension"], source_encoding: inp["source_encoding"])
    end

    # video_url has no pdf_password keyword; the assembly still refuses one.
    begin
      b.video_url(inp["source"], pdf_password: inp["pdf_password"])
    rescue ArgumentError
      nil
    else
      raise "video_url accepted pdf_password"
    end
    X::URLs.imgproxy(b.config, :video, X::Options.pipelines_of(one(inp)), inp["source"], inp["source_encoding"],
                     inp["extension"], inp["pdf_password"],)
  end

  # The case through the Hash form.
  def self.run_hash(c) # rubocop:disable Metrics
    cfg = c.fetch("config", {})
    inp = c.fetch("input")
    enc = kwargs(inp, %w[source_encoding])
    case c.fetch("op")
    when "imgproxy.url"
      imgproxy(cfg).url(inp["source"], hashes(inp), extension: inp["extension"], pdf_password: inp["pdf_password"], **enc)
    when "imgproxy.info_url"
      imgproxy(cfg).info_url(inp["source"], one(inp), pdf_password: inp["pdf_password"], **enc)
    when "imgproxy.video_url" then video_url(cfg, inp)
    when "imgproxy.stream_url" then imgproxy(cfg).stream_url(inp["source"], options: one(inp), **stream_kw(inp), **enc)
    when "query.url"
      query(cfg).url(inp["source"], params: inp.fetch("params", []), options: hashes(inp), extension: inp["extension"],
                                    pdf_password: inp["pdf_password"],)
    when "query.video_url" then query(cfg).video_url(inp["source"], hashes(inp), extension: inp["extension"])
    when "query.stream_url" then query(cfg).stream_url(inp["source"], options: one(inp), **stream_kw(inp))
    when "query.imaginary_url" then query(cfg).imaginary_url(inp["endpoint"], inp["source"], inp.fetch("params", []))
    when "query.query_request"
      # input.build names the query op that builds the URL from the other inputs.
      q = X.query_method_request(run_hash(c.merge("op" => inp.fetch("build"))))
      "#{q.http_method} #{q.url}\nContent-Type: #{q.content_type}\n\n#{q.body}"
    when "sign" then X.sign(inp["message"], signing(cfg))
    when "sign_xi_url" then X.sign_xi_url(inp["url"], signing(cfg), mount: inp.fetch("mount", ""))
    when "encrypt_source" then X.encrypt_source(inp["source"], encryption(cfg))
    when "encrypt_pdf_password" then X.encrypt_pdf_password(inp["password"], encryption(cfg))
    when "xi_source" then X.xi_source(inp["name"], inp["key"])
    when "source_url" then X.source_url(inp["scheme"], inp["authority"], inp["key"])
    when "range_header" then X.range_header(range(inp["range"]))
    when "escape_plain_source" then X.escape_plain_source(inp["value"])
    when "escape_query_value" then X.escape_query_value(inp["value"])
    else raise "unknown op #{c["op"]}"
    end
  end

  # The case through the fluent chains, for the ops that have one; nil otherwise.
  def self.run_fluent(c) # rubocop:disable Metrics
    cfg = c.fetch("config", {})
    inp = c.fetch("input")
    chain =
      case c.fetch("op")
      when "imgproxy.url" then imgproxy(cfg).image(inp["source"])
      when "imgproxy.info_url" then imgproxy(cfg).info(inp["source"])
      when "imgproxy.video_url"
        return nil if inp.key?("pdf_password")

        imgproxy(cfg).video(inp["source"])
      when "query.url"
        q = query(cfg).image(inp["source"])
        inp.fetch("params", []).each { |k, v| q.param(k, v) }
        q
      when "query.video_url" then query(cfg).video(inp["source"])
      else return nil
      end
    inp.fetch("options", []).each_with_index do |pl, i|
      if i.positive?
        # /info and /video chains cannot chain; the Hash form covers the error.
        return nil unless chain.respond_to?(:pipeline)

        chain.pipeline
      end
      pl.each { |name, args| chain.option(name, *args) }
    end
    chain.encoding(inp["source_encoding"]) if inp.key?("source_encoding")
    chain.extension(inp["extension"]) unless inp["extension"].nil?
    chain.with_pdf_password(inp["pdf_password"]) if inp.key?("pdf_password")
    chain.build
  end

  def test_fixture_covers_every_op
    assert_operator CASES.map { |c| c["op"] }.uniq.length, :>=, 17
    assert_operator CASES.length, :>=, 100
  end

  CASES.each do |c|
    define_method(TestHelper.test_name(c["op"], c["name"])) do
      expect = c.fetch("expect")
      if expect.key?("error")
        err = assert_raises(X::URLBuildError) { self.class.run_hash(c) }
        assert_equal expect["error"], err.code.to_s, err.message
        begin
          fluent = self.class.run_fluent(c)
        rescue X::URLBuildError => e
          assert_equal expect["error"], e.code.to_s, "fluent: #{e.message}"
        else
          assert_nil fluent, "fluent form built #{fluent.inspect} instead of raising #{expect["error"]}"
        end
      else
        assert_equal expect.fetch("value"), self.class.run_hash(c)
        fluent = self.class.run_fluent(c)
        assert_equal expect.fetch("value"), fluent, "fluent form" unless fluent.nil?
      end
    end
  end
end
