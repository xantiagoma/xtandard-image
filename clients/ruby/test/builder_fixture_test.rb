# frozen_string_literal: true

# testdata/urls/imgproxy-builder.json: 400+ cases derived from the server's golden tables.
#
# The URLs were written by the TypeScript client and parsed back by the Go
# dialect tests (TestBuilderFixture); every client must write the same
# ones: url with option names as given, url_short with the default short
# names, and query_url (query dialect) where the case has one.

require_relative "test_helper"

class BuilderFixtureTest < Minitest::Test
  FIXTURE = TestHelper.load_json("testdata/urls/imgproxy-builder.json")
  CASES = FIXTURE.fetch("cases")
  X = XtandardImage

  def self.signing(c)
    size = c["signature_size"]
    return nil if size.nil? || size.zero?

    X::SigningKey.new(key: FIXTURE.dig("signing", "key"), salt: FIXTURE.dig("signing", "salt"), size: size)
  end

  def self.apply(chain, c)
    c.fetch("pipelines").each_with_index do |pl, i|
      chain.pipeline if i.positive?
      pl.each { |entry| entry.each { |name, args| chain.option(name, *args) } }
    end
    chain.extension(c["extension"]) unless c["extension"].to_s.empty?
    chain.build
  end

  def self.fluent(c, given:)
    b = X::ImgproxyURLBuilder.new(
      unsigned_signature: "unsafe",
      encryption: X::EncryptionKey.new(key: FIXTURE.dig("encryption", "key"), iv: FIXTURE.dig("encryption", "iv")),
      signing: signing(c), separator: c.fetch("separator", ":"), option_names: given ? :given : :short,
    )
    chain =
      case c["endpoint"]
      when "info" then b.info(c["source"])
      when "video" then b.video(c["source"])
      else b.image(c["source"])
      end
    chain.encoding(c["encoding"])
    apply(chain, c)
  end

  def self.query(c)
    q = X::QueryURLBuilder.new(signing: signing(c))
    apply(c["endpoint"] == "video" ? q.video(c["source"]) : q.image(c["source"]), c)
  end

  def test_covers_the_tables
    assert_operator CASES.length, :>=, 400
  end

  CASES.each_with_index do |c, i|
    define_method(TestHelper.test_name(format("%03d", i), c["name"])) do
      assert_equal c["url"], self.class.fluent(c, given: true), "url"
      assert_equal c["url_short"], self.class.fluent(c, given: false), "url_short"
      assert_equal c["query_url"], self.class.query(c), "query_url" if c["query"]
    end
  end
end
