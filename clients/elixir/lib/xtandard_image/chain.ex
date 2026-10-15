defmodule XtandardImage.Chain do
  @moduledoc """
  A fluent, pipe-friendly URL: start one with `XtandardImage.Imgproxy.image/2`,
  `info/2` or `video/2`, or `XtandardImage.Query.image/2` or `video/2`, append
  options with the per-option functions of `XtandardImage.Processing`,
  `XtandardImage.Info` or `XtandardImage.Video` (or `option/3`), and finish
  with `build/1` or `build!/1`.

      iex> alias XtandardImage.Processing, as: P
      iex> XtandardImage.imgproxy(source_encoding: :plain)
      ...> |> XtandardImage.Imgproxy.image("https://example.com/a.jpg")
      ...> |> P.resize([:fit, 100, 100])
      ...> |> P.pipeline()
      ...> |> P.blur(2)
      ...> |> P.build()
      {:ok, "/insecure/rs:fit:100:100/-/bl:2/plain/https://example.com/a.jpg"}

  Nothing is checked until `build/1`, so a chain never raises halfway; the
  errors are those of the non-fluent functions, in the same order. A `nil`
  option value skips the option, which suits optional settings
  (`|> P.quality(opts[:quality])`). `to_string/1` builds the URL, raising on
  errors.
  """

  alias XtandardImage.Error
  alias XtandardImage.Imgproxy
  alias XtandardImage.Options
  alias XtandardImage.Query

  @typedoc "One argument: strings and atoms as written, numbers as ECMAScript writes them, booleans as 1/0, nil as empty."
  @type arg :: String.t() | atom() | number() | boolean() | nil

  @typedoc "An option's arguments: a list, a tuple, or the first argument alone."
  @type arg_value :: arg() | tuple() | [arg()]

  @typedoc "Which URL the chain builds."
  @type endpoint :: :process | :info | :video

  @type t :: %__MODULE__{
          builder: Imgproxy.t() | Query.t(),
          endpoint: endpoint(),
          source: String.t(),
          pipelines: [[{term(), term()}]],
          params: [{term(), term()}],
          encoding: XtandardImage.Spec.source_encoding() | nil,
          ext: String.t() | atom() | nil,
          pdf_password: String.t() | nil
        }

  @enforce_keys [:builder, :endpoint, :source]
  defstruct [
    :builder,
    :endpoint,
    :source,
    :encoding,
    :ext,
    :pdf_password,
    pipelines: [[]],
    params: []
  ]

  @doc false
  @spec new(Imgproxy.t() | Query.t(), endpoint(), String.t()) :: t()
  def new(builder, endpoint, source),
    do: %__MODULE__{builder: builder, endpoint: endpoint, source: source}

  @doc """
  Appends one option occurrence: a name or alias (atom or string) and its
  arguments. The per-option functions call this.
  """
  @spec option(t(), atom() | String.t(), arg_value()) :: t()
  def option(%__MODULE__{pipelines: [current | done]} = chain, name, args),
    do: %{chain | pipelines: [[{name, args} | current] | done]}

  @doc "Appends every option of a keyword list or map, in order."
  @spec options(t(), keyword() | [{String.t(), arg_value()}] | map()) :: t()
  def options(%__MODULE__{} = chain, options) when is_map(options),
    do: options(chain, Map.to_list(options))

  def options(%__MODULE__{} = chain, options) when is_list(options),
    do: Enum.reduce(options, chain, &add_pair/2)

  defp add_pair({name, args}, chain), do: option(chain, name, args)

  defp add_pair(other, %__MODULE__{pipelines: [current | done]} = chain),
    do: %{chain | pipelines: [[{{:invalid, other}, nil} | current] | done]}

  @doc "Starts the next chained pipeline (`/-/`; `o=…/-/…` in the query dialect)."
  @spec pipeline(t()) :: t()
  def pipeline(%__MODULE__{pipelines: ps} = chain), do: %{chain | pipelines: [[] | ps]}

  @doc """
  Sets the output extension: `@webp` / `.webp` for images, a container for
  `/video` (`webm`, `mp4`, `mp3`, …), `format=` in the query dialect.
  """
  @spec extension(t(), String.t() | atom()) :: t()
  def extension(%__MODULE__{} = chain, ext), do: %{chain | ext: ext}

  @doc "Overrides the source encoding of an imgproxy URL: `:base64`, `:plain` or `:encrypted`."
  @spec encoding(t(), XtandardImage.Spec.source_encoding()) :: t()
  def encoding(%__MODULE__{} = chain, encoding), do: %{chain | encoding: encoding}

  @doc """
  Opens a password-protected PDF: the plain-text password is encrypted into
  `pdp` (it needs both the encryption and the signing key).
  """
  @spec with_pdf_password(t(), String.t()) :: t()
  def with_pdf_password(%__MODULE__{} = chain, password), do: %{chain | pdf_password: password}

  @doc "Appends a native query key (`w`, `fit`, `g`, …) of a `/q/` URL, written before `o=`."
  @spec param(t(), atom() | String.t(), String.t() | atom() | number() | boolean() | nil) :: t()
  def param(%__MODULE__{params: ps} = chain, key, value), do: %{chain | params: [{key, value} | ps]}

  @doc "Builds the URL."
  @spec build(t()) :: {:ok, String.t()} | {:error, Error.t()}
  def build(%__MODULE__{} = chain), do: Error.wrap(fn -> build!(chain) end)

  @doc "Builds the URL, raising `XtandardImage.Error`."
  @spec build!(t()) :: String.t()
  def build!(%__MODULE__{builder: %Imgproxy{} = b} = chain) do
    if chain.params != [],
      do: Error.raise!(:invalid_request, "param/3 is for query dialect URLs")

    Imgproxy.build!(b, chain.endpoint, pipelines!(chain), chain.source,
      encoding: chain.encoding,
      extension: chain.ext,
      pdf_password: chain.pdf_password
    )
  end

  def build!(%__MODULE__{builder: %Query{} = q} = chain) do
    if chain.encoding != nil,
      do: Error.raise!(:invalid_request, "query dialect URLs have no source encoding")

    Query.build!(q, chain.endpoint, pipelines!(chain), chain.source,
      params: Enum.reverse(chain.params),
      extension: chain.ext,
      pdf_password: chain.pdf_password
    )
  end

  defp pipelines!(%__MODULE__{pipelines: ps}) do
    ps
    |> Enum.reverse()
    |> Enum.map(fn p -> p |> Enum.reverse() |> Enum.map(&check_pair!/1) end)
    |> Enum.map(&Options.entries_of!/1)
  end

  defp check_pair!({{:invalid, other}, nil}),
    do:
      Error.raise!(
        :invalid_argument,
        "an option is a {name, arguments} pair, got #{inspect(other)}"
      )

  defp check_pair!(pair), do: pair

  defimpl String.Chars do
    @impl true
    def to_string(chain), do: XtandardImage.Chain.build!(chain)
  end
end
