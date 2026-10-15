# frozen_string_literal: true

# testdata/security/vectors.json: the signing, enc/ and pdf_password vectors shared with Go and TypeScript.

require_relative "test_helper"

class VectorsTest < Minitest::Test
  V = TestHelper.load_json("testdata/security/vectors.json")
  X = XtandardImage

  def self.decrypt(token, key)
    raw = (token.tr("-_", "+/") + ("=" * (-token.length % 4))).unpack1("m0")
    c = OpenSSL::Cipher.new("aes-#{key.length * 4}-cbc")
    c.decrypt
    c.key = [key].pack("H*")
    c.iv = raw.byteslice(0, 16)
    (c.update(raw.byteslice(16..)) + c.final).force_encoding(Encoding::UTF_8)
  end

  V.fetch("imgproxy").each do |v|
    define_method(TestHelper.test_name("imgproxy", v["name"])) do
      k = X::SigningKey.new(key: v["key"], salt: v["salt"], size: v["size"])
      assert_equal v["signature"], X.sign(v["path"], k)
      p = v["path"].start_with?("/") ? v["path"] : "/#{v["path"]}"
      assert_equal "/#{v["signature"]}#{p}", X.sign_imgproxy_path(v["path"], k)
      raw = X::SigningKey.new(key: [v["key"]].pack("H*"), salt: [v["salt"]].pack("H*"), size: v["size"])
      assert_equal v["signature"], X.sign(v["path"], raw), "raw bytes"
    end
  end

  V.fetch("xi_sig").each do |v|
    define_method(TestHelper.test_name("xi_sig", v["name"])) do
      k = X::SigningKey.new(key: v["key"], salt: v["salt"], size: v["size"])
      assert_equal v["signed_query"], X.sign_xi_query(v["path"], v["query"], k)
      assert_equal v["signed_query"], X.sign_xi_query(v["path"], v["signed_query"], k), "re-signing"
      assert_equal "https://img.example.com#{v["path"]}?#{v["signed_query"]}",
                   X.sign_xi_url("https://img.example.com#{v["path"]}?#{v["query"]}", k)
    end
  end

  V.fetch("encryption").each do |v|
    define_method(TestHelper.test_name("encryption", v["name"])) do
      token = X.encrypt_source(v["plaintext"], X::EncryptionKey.new(key: v["key"], iv: v["iv"]))
      assert_equal v["token"], token
      assert_equal v["plaintext"], self.class.decrypt(token, v["key"])
    end
  end

  V.fetch("pdf_password").each do |v|
    define_method(TestHelper.test_name("pdf_password", v["name"])) do
      assert_equal v["token"], X.encrypt_pdf_password(v["password"], X::EncryptionKey.new(key: v["key"], iv: v["iv"]))
    end
  end

  def test_random_iv
    key = V.dig("pdf_password", 0, "key")
    k = X::EncryptionKey.new(key: key)
    a = X.encrypt_pdf_password("s3cret", k)
    b = X.encrypt_pdf_password("s3cret", k)
    refute_equal a, b
    assert_equal "s3cret", self.class.decrypt(a, key)
  end

  def test_xi_message
    assert_equal ["s", "/?url=x&w=1"], X.xi_message("/", "url=x&&xi_sig=s&w=1")
    assert_equal [nil, "/"], X.xi_message("/", "")
    err = assert_raises(X::URLBuildError) { X.xi_message("/", "xi_sig=a&xi_sig=b") }
    assert_equal :invalid_request, err.code
  end

  def test_keys_hide_secrets
    s = X::SigningKey.new(key: "ab" * 32, salt: "cd" * 32)
    e = X::EncryptionKey.new(key: "ef" * 32, iv: "01" * 16)
    [s.inspect, s.to_s, e.inspect, e.to_s].each do |text|
      %w[abab cdcd efef 0101].each { |secret| refute_includes text, secret }
    end
  end

  def test_key_validation
    bad = [
      -> { X::SigningKey.new(key: "zz", salt: "00") },
      -> { X::SigningKey.new(key: "abc", salt: "00") },
      -> { X::SigningKey.new(key: "00", salt: "00", size: 0) },
      -> { X::SigningKey.new(key: "00", salt: "00", size: 33) },
      -> { X::EncryptionKey.new(key: "00" * 15) },
      -> { X::EncryptionKey.new(key: "00" * 32, iv: "00" * 15) },
    ]
    bad.each { |f| assert_equal :invalid_key, assert_raises(X::URLBuildError, &f).code }
  end
end
