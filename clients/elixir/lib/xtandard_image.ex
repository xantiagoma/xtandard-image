defmodule XtandardImage do
  @moduledoc """
  The official Elixir client of [xtandard-image](https://github.com/xantiagoma/xtandard-image).

    * URL builders for the imgproxy dialect (processing, `/info`, `/video`,
      HLS and DASH; `XtandardImage.Imgproxy`) and the query dialect (`/q/`,
      `/q/video`, imaginary endpoints; `XtandardImage.Query`), with an options
      form (keyword lists) and a pipe-friendly fluent form
      (`XtandardImage.Processing`, `XtandardImage.Info`, `XtandardImage.Video`);
    * imgproxy and `xi_sig` signing (`XtandardImage.Signing`);
    * `enc/` source and PDF password encryption (`XtandardImage.Encryption`);
    * an HTTP client on [Req](https://hex.pm/packages/req) (`XtandardImage.Client`),
      when Req is a dependency of your project.

  URL building, signing and encryption need nothing beyond OTP's `:crypto`.
  **Build and sign URLs on a server**: the signing key signs every URL, and the
  encryption key decrypts every `enc/` source and PDF password.

      iex> key = XtandardImage.SigningKey.new!("736563726574", "68656C6C6F")
      iex> img = XtandardImage.imgproxy(base_url: "https://img.example.com", signing: key)
      iex> XtandardImage.Imgproxy.url!(img, "s3://photos/cat.jpg", [rs: {:fill, 300, 400}, q: 80], extension: :webp)
      "https://img.example.com/dVfV-AD0lslbun93lUn2xNUun0etnBYY2VrTMHhDjBg/rs:fill:300:400/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp"

  Functions that can fail return `{:ok, value}` or
  `{:error, %XtandardImage.Error{code: code}}` and have a bang variant that
  raises. The codes are the language-neutral ones of the shared conformance
  fixture (`XtandardImage.Error`).

  This module gathers the most used functions; each delegates to the module
  that documents it.
  """

  alias XtandardImage.Encryption
  alias XtandardImage.Signing
  alias XtandardImage.Sources

  @typedoc """
  Key material: a hex string, as in the server's config (`XI_SIGNING_KEYS`,
  `XI_ENCRYPTION_KEYS`), or raw bytes as `{:raw, bytes}`.
  """
  @type key_material :: String.t() | {:raw, binary()}

  @doc "An imgproxy-dialect URL builder; see `XtandardImage.Imgproxy` for the options."
  @spec imgproxy(keyword()) :: XtandardImage.Imgproxy.t()
  defdelegate imgproxy(opts \\ []), to: XtandardImage.Imgproxy, as: :new

  @doc "A query dialect URL builder; see `XtandardImage.Query` for the options."
  @spec query_urls(keyword()) :: XtandardImage.Query.t()
  defdelegate query_urls(opts \\ []), to: XtandardImage.Query, as: :new

  @doc "See `XtandardImage.Signing.sign/2`."
  @spec sign(String.t(), XtandardImage.SigningKey.t()) :: String.t()
  defdelegate sign(message, key), to: Signing

  @doc "See `XtandardImage.Signing.sign_imgproxy_path/2`."
  @spec sign_imgproxy_path(String.t(), XtandardImage.SigningKey.t()) :: String.t()
  defdelegate sign_imgproxy_path(path, key), to: Signing

  @doc "See `XtandardImage.Signing.xi_message/2`."
  @spec xi_message(String.t(), String.t()) ::
          {:ok, {String.t() | nil, String.t()}} | {:error, XtandardImage.Error.t()}
  defdelegate xi_message(path, raw_query), to: Signing

  @doc "See `XtandardImage.Signing.sign_xi_query/3`."
  @spec sign_xi_query(String.t(), String.t(), XtandardImage.SigningKey.t()) ::
          {:ok, String.t()} | {:error, XtandardImage.Error.t()}
  defdelegate sign_xi_query(path, raw_query, key), to: Signing

  @doc "See `XtandardImage.Signing.sign_xi_url/3`."
  @spec sign_xi_url(String.t(), XtandardImage.SigningKey.t(), keyword()) ::
          {:ok, String.t()} | {:error, XtandardImage.Error.t()}
  defdelegate sign_xi_url(url, key, opts \\ []), to: Signing

  @doc "See `XtandardImage.Encryption.encrypt_source/2`."
  @spec encrypt_source(String.t(), XtandardImage.EncryptionKey.t()) ::
          {:ok, String.t()} | {:error, XtandardImage.Error.t()}
  defdelegate encrypt_source(source, key), to: Encryption

  @doc "See `XtandardImage.Encryption.encrypt_pdf_password/2`."
  @spec encrypt_pdf_password(String.t(), XtandardImage.EncryptionKey.t()) ::
          {:ok, String.t()} | {:error, XtandardImage.Error.t()}
  defdelegate encrypt_pdf_password(password, key), to: Encryption

  @doc "See `XtandardImage.Sources.xi_source/2`."
  @spec xi_source(String.t(), String.t()) :: {:ok, String.t()} | {:error, XtandardImage.Error.t()}
  defdelegate xi_source(name, key), to: Sources

  @doc "See `XtandardImage.Sources.xi_source!/2`."
  @spec xi_source!(String.t(), String.t()) :: String.t()
  defdelegate xi_source!(name, key), to: Sources

  @doc "See `XtandardImage.Sources.source_url/3`."
  @spec source_url(XtandardImage.Spec.source_scheme(), String.t(), String.t()) ::
          {:ok, String.t()} | {:error, XtandardImage.Error.t()}
  defdelegate source_url(scheme, authority, key), to: Sources

  @doc "See `XtandardImage.Sources.escape_plain_source/1`."
  @spec escape_plain_source(String.t()) :: String.t()
  defdelegate escape_plain_source(source), to: Sources

  @doc "See `XtandardImage.Sources.escape_query_value/1`."
  @spec escape_query_value(String.t()) :: String.t()
  defdelegate escape_query_value(value), to: Sources

  @doc "See `XtandardImage.QueryMethodRequest.from_url/1`."
  @spec query_method_request(String.t()) ::
          {:ok, XtandardImage.QueryMethodRequest.t()} | {:error, XtandardImage.Error.t()}
  defdelegate query_method_request(url), to: XtandardImage.QueryMethodRequest, as: :from_url

  @doc "See `XtandardImage.QueryMethodRequest.from_url!/1`."
  @spec query_method_request!(String.t()) :: XtandardImage.QueryMethodRequest.t()
  defdelegate query_method_request!(url), to: XtandardImage.QueryMethodRequest, as: :from_url!

  @doc "See `XtandardImage.Range.header/1`."
  @spec range_header(XtandardImage.Range.t()) ::
          {:ok, String.t()} | {:error, XtandardImage.Error.t()}
  defdelegate range_header(range), to: XtandardImage.Range, as: :header
end
