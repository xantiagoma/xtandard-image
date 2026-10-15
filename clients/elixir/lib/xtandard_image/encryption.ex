defmodule XtandardImage.Encryption do
  @moduledoc """
  Source URL and PDF password encryption, byte-compatible with the server
  (`internal/security/crypt`):

      token = base64url_unpadded(IV[16] <> AES-CBC(key, IV, PKCS#7(plaintext)))

  Encrypt on a server: the key decrypts every token.

      iex> key = XtandardImage.EncryptionKey.new!(
      ...>   "1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1",
      ...>   iv: "30313233343536373839616263646566"
      ...> )
      iex> XtandardImage.Encryption.encrypt_pdf_password("secret", key)
      {:ok, "MDEyMzQ1Njc4OWFiY2RlZlsN0ZQuFO2sitmy5C87M2o"}
  """

  alias XtandardImage.EncryptionKey
  alias XtandardImage.Error
  alias XtandardImage.Spec
  alias XtandardImage.Text

  @doc """
  Encrypts a source URL into an `enc/` token. An empty source, or one with
  control characters, is an `:invalid_source`.
  """
  @spec encrypt_source(String.t(), EncryptionKey.t()) :: {:ok, String.t()} | {:error, Error.t()}
  def encrypt_source(source, key), do: Error.wrap(fn -> encrypt_source!(source, key) end)

  @doc "Like `encrypt_source/2`, raising `XtandardImage.Error`."
  @spec encrypt_source!(String.t(), EncryptionKey.t()) :: String.t()
  def encrypt_source!(source, %EncryptionKey{} = key),
    do: source |> Text.check_text!(:invalid_source, "source") |> seal(key)

  @doc """
  Encrypts the password of a password-protected PDF into a `pdf_password`
  (`pdp`) token: the `enc/` format over the password, with the same key. The
  server accepts the option on signed URLs only.

  An empty password, one with control characters, or one longer than 1024
  UTF-8 bytes is an `:invalid_password`.
  """
  @spec encrypt_pdf_password(String.t(), EncryptionKey.t()) ::
          {:ok, String.t()} | {:error, Error.t()}
  def encrypt_pdf_password(password, key),
    do: Error.wrap(fn -> encrypt_pdf_password!(password, key) end)

  @doc "Like `encrypt_pdf_password/2`, raising `XtandardImage.Error`."
  @spec encrypt_pdf_password!(String.t(), EncryptionKey.t()) :: String.t()
  def encrypt_pdf_password!(password, %EncryptionKey{} = key) do
    password = Text.check_text!(password, :invalid_password, "password")
    max = Spec.max_pdf_password_bytes()

    if byte_size(password) > max,
      do: Error.raise!(:invalid_password, "password is longer than #{max} bytes")

    seal(password, key)
  end

  defp seal(plaintext, %EncryptionKey{key: k, iv: fixed}) do
    iv = fixed || :crypto.strong_rand_bytes(Spec.iv_size())
    pad = 16 - rem(byte_size(plaintext), 16)
    data = plaintext <> :binary.copy(<<pad>>, pad)
    Text.b64url(iv <> :crypto.crypto_one_time(cipher(k), k, iv, data, true))
  end

  defp cipher(k) when byte_size(k) == 16, do: :aes_128_cbc
  defp cipher(k) when byte_size(k) == 24, do: :aes_192_cbc
  defp cipher(k) when byte_size(k) == 32, do: :aes_256_cbc
end
