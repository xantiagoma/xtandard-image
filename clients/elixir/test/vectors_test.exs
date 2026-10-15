defmodule XtandardImage.VectorsTest do
  # testdata/security/vectors.json: the signing, enc/ and pdf_password
  # vectors shared with Go, TypeScript and the other clients.
  use ExUnit.Case, async: true

  alias XtandardImage.{Encryption, EncryptionKey, Error, Fixtures, Signing, SigningKey}

  @v Fixtures.load("testdata/security/vectors.json")
  @external_resource Fixtures.path("testdata/security/vectors.json")

  defp decrypt(token, key) do
    raw = Base.url_decode64!(token, padding: false)
    <<iv::binary-16, ct::binary>> = raw
    k = Base.decode16!(key, case: :mixed)
    cipher = %{16 => :aes_128_cbc, 24 => :aes_192_cbc, 32 => :aes_256_cbc}[byte_size(k)]
    padded = :crypto.crypto_one_time(cipher, k, iv, ct, false)
    pad = :binary.last(padded)
    binary_part(padded, 0, byte_size(padded) - pad)
  end

  for v <- @v["imgproxy"] do
    @vec v
    test "imgproxy #{v["name"]}" do
      v = @vec
      k = SigningKey.new!(v["key"], v["salt"], size: v["size"])
      assert Signing.sign(v["path"], k) == v["signature"]
      p = if String.starts_with?(v["path"], "/"), do: v["path"], else: "/" <> v["path"]
      assert Signing.sign_imgproxy_path(v["path"], k) == "/#{v["signature"]}#{p}"

      raw =
        SigningKey.new!(
          {:raw, Base.decode16!(v["key"], case: :mixed)},
          {:raw, Base.decode16!(v["salt"], case: :mixed)},
          size: v["size"]
        )

      assert Signing.sign(v["path"], raw) == v["signature"]
    end
  end

  for v <- @v["xi_sig"] do
    @vec v
    test "xi_sig #{v["name"]}" do
      v = @vec
      k = SigningKey.new!(v["key"], v["salt"], size: v["size"])
      assert Signing.sign_xi_query(v["path"], v["query"], k) == {:ok, v["signed_query"]}
      assert Signing.sign_xi_query(v["path"], v["signed_query"], k) == {:ok, v["signed_query"]}

      assert Signing.sign_xi_url("https://img.example.com#{v["path"]}?#{v["query"]}", k) ==
               {:ok, "https://img.example.com#{v["path"]}?#{v["signed_query"]}"}
    end
  end

  for v <- @v["encryption"] do
    @vec v
    test "encryption #{v["name"]}" do
      v = @vec

      {:ok, token} =
        Encryption.encrypt_source(v["plaintext"], EncryptionKey.new!(v["key"], iv: v["iv"]))

      assert token == v["token"]
      assert decrypt(token, v["key"]) == v["plaintext"]
    end
  end

  for v <- @v["pdf_password"] do
    @vec v
    test "pdf_password #{v["name"]}" do
      v = @vec
      key = EncryptionKey.new!(v["key"], iv: v["iv"])
      assert Encryption.encrypt_pdf_password!(v["password"], key) == v["token"]
    end
  end

  test "a random IV per token" do
    hex = hd(@v["pdf_password"])["key"]
    k = EncryptionKey.new!(hex)
    a = Encryption.encrypt_pdf_password!("s3cret", k)
    b = Encryption.encrypt_pdf_password!("s3cret", k)
    assert a != b
    assert decrypt(a, hex) == "s3cret"
  end

  test "xi_message" do
    assert Signing.xi_message("/", "url=x&&xi_sig=s&w=1") == {:ok, {"s", "/?url=x&w=1"}}
    assert Signing.xi_message("/", "") == {:ok, {nil, "/"}}
    assert {:error, %Error{code: :invalid_request}} = Signing.xi_message("/", "xi_sig=a&xi_sig=b")
  end

  test "inspect hides the key material" do
    k = SigningKey.new!(String.duplicate("ab", 32), String.duplicate("cd", 32))
    refute inspect(k) =~ "ab"
    refute inspect(k) =~ <<0xAB>>
    e = EncryptionKey.new!(String.duplicate("ef", 32), iv: String.duplicate("01", 16))
    assert inspect(e) == "#XtandardImage.EncryptionKey<...>"
  end
end
