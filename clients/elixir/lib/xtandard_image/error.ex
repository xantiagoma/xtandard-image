defmodule XtandardImage.Error do
  @moduledoc """
  A URL, token or header cannot be built from the given input.

  `code` is the language-neutral error code of `clients/spec/conformance.json`:

  | Code | Raised for |
  |---|---|
  | `:unknown_option` | an option name not in the catalogue |
  | `:invalid_argument` | no arguments, a forbidden character, an unsupported type, `:` in a `/q/video` argument |
  | `:invalid_source` | an empty source, or a source with control characters |
  | `:invalid_extension` | an extension that is not alphanumeric |
  | `:invalid_separator` | a separator that is not one character, or is forbidden |
  | `:missing_signing` | a PDF password without a signing key |
  | `:missing_encryption` | an encrypted source or a PDF password without an encryption key |
  | `:invalid_key` | key material that is not hex, a wrong key or IV size, a signature size outside 1–32 |
  | `:invalid_password` | an empty password, one with control characters, or over 1024 UTF-8 bytes |
  | `:invalid_request` | chained `/info` or `/video` pipelines, a PDF password on `/video`, a stream option given as an option, an unknown protocol, `ts` on DASH, a URL outside the mount, two `xi_sig` |
  | `:invalid_name` | a bad `xi://` source name or authority |
  | `:invalid_endpoint` | an imaginary endpoint that is not lower-case letters, or is `video` |
  | `:invalid_range` | a negative range value, or an end before the start |

  Functions return `{:error, %XtandardImage.Error{}}`; their bang variants raise it.
  """

  @typedoc "Why a URL could not be built."
  @type code ::
          :unknown_option
          | :invalid_argument
          | :invalid_source
          | :invalid_extension
          | :invalid_separator
          | :missing_signing
          | :missing_encryption
          | :invalid_key
          | :invalid_password
          | :invalid_request
          | :invalid_name
          | :invalid_endpoint
          | :invalid_range

  @type t :: %__MODULE__{code: code(), message: String.t()}

  defexception [:code, :message]

  @doc false
  @spec new(code(), String.t()) :: t()
  def new(code, message), do: %__MODULE__{code: code, message: message}

  @doc false
  @spec raise!(code(), String.t()) :: no_return()
  def raise!(code, message), do: raise(new(code, message))

  @doc false
  # Runs a function that raises XtandardImage.Error and returns {:ok, value}
  # or {:error, error}: the non-bang variants of the public API.
  @spec wrap((-> value)) :: {:ok, value} | {:error, t()} when value: term()
  def wrap(fun) do
    {:ok, fun.()}
  rescue
    e in __MODULE__ -> {:error, e}
  end
end

defmodule XtandardImage.HTTPError do
  @moduledoc """
  A non-2xx response from the server.

  `kind`, `message` and `request_id` come from the server's JSON error envelope
  (`XtandardImage.ErrorResponse`); a body that is not the envelope (a proxy's
  error page, say) gives kind `"unknown"` and the body text.

  `reason` names the status class, so callers can match without numbers:

  | Status | `reason` |
  |---|---|
  | 400 | `:bad_request` (kind `invalid_url`) |
  | 403 | `:forbidden` (a bad or missing signature, a refused source or option) |
  | 404 | `:not_found` (no source, or an expired URL) |
  | 406 | `:not_acceptable` |
  | 422 | `:unprocessable` (too large, unsupported, corrupt, over a limit) |
  | 429 | `:too_many_requests` (overloaded; retry later) |
  | 502, 504 | `:gateway` |
  | 503 | `:service_unavailable` |
  | other 5xx | `:server_error` |
  | other | `:http_error` |
  """

  @typedoc "The status class of the response."
  @type reason ::
          :bad_request
          | :forbidden
          | :not_found
          | :not_acceptable
          | :unprocessable
          | :too_many_requests
          | :gateway
          | :service_unavailable
          | :server_error
          | :http_error

  @type t :: %__MODULE__{
          status: non_neg_integer(),
          reason: reason(),
          kind: String.t(),
          message: String.t(),
          request_id: String.t() | nil,
          body: XtandardImage.ErrorResponse.t() | nil,
          url: String.t()
        }

  defexception [:status, :reason, :kind, :message, :request_id, :body, :url]

  @doc """
  The status class of an HTTP status.

      iex> XtandardImage.HTTPError.reason(504)
      :gateway
  """
  @spec reason(non_neg_integer()) :: reason()
  def reason(400), do: :bad_request
  def reason(403), do: :forbidden
  def reason(404), do: :not_found
  def reason(406), do: :not_acceptable
  def reason(422), do: :unprocessable
  def reason(429), do: :too_many_requests
  def reason(503), do: :service_unavailable
  def reason(status) when status in [502, 504], do: :gateway
  def reason(status) when status >= 500, do: :server_error
  def reason(_), do: :http_error

  @doc """
  Builds the error of a non-2xx response from its status, URL and body.

      iex> e = XtandardImage.HTTPError.from_response(404, "/x", ~s({"error":{"kind":"not_found","message":"source image not found"}}))
      iex> {e.reason, e.kind, e.message}
      {:not_found, "not_found", "source image not found"}
  """
  @spec from_response(non_neg_integer(), String.t(), binary()) :: t()
  def from_response(status, url, body) when is_binary(body) do
    case envelope(body) do
      %XtandardImage.ErrorResponse{error: %XtandardImage.ErrorBody{} = e} = env ->
        %__MODULE__{
          status: status,
          reason: reason(status),
          kind: e.kind,
          message: e.message,
          request_id: e.request_id,
          body: env,
          url: url
        }

      nil ->
        text = String.trim(body)

        %__MODULE__{
          status: status,
          reason: reason(status),
          kind: "unknown",
          message: if(text == "", do: "HTTP #{status}", else: text),
          request_id: nil,
          body: nil,
          url: url
        }
    end
  end

  defp envelope(body) do
    case XtandardImage.JSON.decode(body) do
      {:ok, %{"error" => %{"kind" => k, "message" => m}} = map}
      when is_binary(k) and is_binary(m) ->
        XtandardImage.ErrorResponse.from_map(map)

      _ ->
        nil
    end
  end
end

defmodule XtandardImage.TransportError do
  @moduledoc """
  The request got no response: connection refused, TLS, timeout. `reason` is the
  underlying error (a `Req.TransportError` reason such as `:econnrefused`, or an
  exception).
  """

  @type t :: %__MODULE__{reason: term(), url: String.t(), message: String.t()}

  defexception [:reason, :url, :message]
end
