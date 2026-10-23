defmodule XtandardImage.QueryMethodRequest do
  @moduledoc """
  A query dialect request sent with the HTTP QUERY method (RFC 10008,
  URL-DIALECTS §4.4).

    * `method` - `"QUERY"`.
    * `url` - the URL without its query.
    * `content_type` - `application/x-www-form-urlencoded`.
    * `body` - the URL's query exactly as built, `xi_sig` included: the server
      signs, limits and caches it as the GET URL, since `xi_sig` does not
      cover the method.

  The parameters move from the URL to the request content, so long option
  chains, watermark text or encrypted sources stay out of URLs. The server
  must have `XI_SERVER_QUERY_METHOD` on (the default); many CDNs and proxies
  neither cache nor forward QUERY.

      iex> XtandardImage.QueryMethodRequest.from_url("/q/?url=s3://b/cat.jpg&w=300")
      {:ok,
       %XtandardImage.QueryMethodRequest{
         method: "QUERY",
         url: "/q/",
         content_type: "application/x-www-form-urlencoded",
         body: "url=s3://b/cat.jpg&w=300"
       }}
  """

  alias XtandardImage.Error

  @content_type "application/x-www-form-urlencoded"

  @type t :: %__MODULE__{
          method: String.t(),
          url: String.t(),
          content_type: String.t(),
          body: String.t()
        }

  @enforce_keys [:url, :body]
  defstruct method: "QUERY", url: nil, content_type: @content_type, body: nil

  @doc "The content type of a QUERY request's content: `#{@content_type}`."
  @spec content_type() :: String.t()
  def content_type, do: @content_type

  @doc """
  Splits a built query dialect URL (`/q/…?url=…`) at its first `?`. A URL
  without a query, with an empty one, or with a fragment is an
  `:invalid_request` error.
  """
  @spec from_url(String.t()) :: {:ok, t()} | {:error, Error.t()}
  def from_url(url) when is_binary(url) do
    with [path, query] when query != "" <- String.split(url, "?", parts: 2),
         false <- String.contains?(url, "#") do
      {:ok, %__MODULE__{url: path, body: query}}
    else
      _ ->
        {:error,
         Error.new(
           :invalid_request,
           "a QUERY request needs a query dialect URL with a query and no fragment"
         )}
    end
  end

  @doc "Like `from_url/1`, raising the error."
  @spec from_url!(String.t()) :: t()
  def from_url!(url) do
    case from_url(url) do
      {:ok, request} -> request
      {:error, error} -> raise error
    end
  end
end
