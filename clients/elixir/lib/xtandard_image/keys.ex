defmodule XtandardImage.SigningKey do
  @moduledoc """
  A signing key: `XI_SIGNING_KEYS` / `XI_SIGNING_SALTS` (imgproxy's
  `IMGPROXY_KEY` / `IMGPROXY_SALT`).

  The key and salt are hex strings, as in the server's config, or raw bytes as
  `{:raw, bytes}`. `size` is the signature size in bytes before encoding, 1 to
  32 (`XI_SIGNING_SIZE`, default 32). `inspect/1` hides the key and the salt.

      iex> {:ok, key} = XtandardImage.SigningKey.new("736563726574", "68656C6C6F")
      iex> key.size
      32
      iex> XtandardImage.SigningKey.new("zz", "00")
      {:error, %XtandardImage.Error{code: :invalid_key, message: "signing key is not a hex string"}}
  """

  alias XtandardImage.Error
  alias XtandardImage.Spec
  alias XtandardImage.Text

  @derive {Inspect, only: [:size]}
  @enforce_keys [:key, :salt, :size]
  defstruct [:key, :salt, :size]

  @typedoc "Decoded key and salt bytes, and the signature size."
  @type t :: %__MODULE__{key: binary(), salt: binary(), size: 1..32}

  @doc "Builds a signing key; `opts` takes `:size`."
  @spec new(XtandardImage.key_material(), XtandardImage.key_material(), keyword()) ::
          {:ok, t()} | {:error, Error.t()}
  def new(key, salt, opts \\ []), do: Error.wrap(fn -> new!(key, salt, opts) end)

  @doc "Like `new/3`, raising `XtandardImage.Error`."
  @spec new!(XtandardImage.key_material(), XtandardImage.key_material(), keyword()) :: t()
  def new!(key, salt, opts \\ []) do
    size = Keyword.get(opts, :size, Spec.default_signature_size())

    if not (is_integer(size) and size >= Spec.min_signature_size() and
              size <= Spec.max_signature_size()) do
      Error.raise!(
        :invalid_key,
        "signature size #{inspect(size)} out of range " <>
          "[#{Spec.min_signature_size()}, #{Spec.max_signature_size()}]"
      )
    end

    %__MODULE__{
      key: Text.key_bytes!(key, "signing key"),
      salt: Text.key_bytes!(salt, "signing salt"),
      size: size
    }
  end
end

defmodule XtandardImage.EncryptionKey do
  @moduledoc """
  An encryption key: the first of `XI_ENCRYPTION_KEYS` (imgproxy's
  `IMGPROXY_SOURCE_URL_ENCRYPTION_KEY`), 16, 24 or 32 bytes as hex or
  `{:raw, bytes}`.

  Leave `:iv` unset for a fresh random IV per token, as the server's own
  encoder does. A fixed 16-byte IV makes URLs deterministic and reveals when
  two URLs share a source; use it only for tests or cache keys. `inspect/1`
  hides the key and the IV.

      iex> {:ok, key} = XtandardImage.EncryptionKey.new(String.duplicate("ab", 32))
      iex> key.iv
      nil
      iex> {:error, e} = XtandardImage.EncryptionKey.new("0011")
      iex> e.message
      "encryption key is 2 bytes, want 16, 24 or 32"
  """

  alias XtandardImage.Error
  alias XtandardImage.Spec
  alias XtandardImage.Text

  @derive {Inspect, only: []}
  @enforce_keys [:key]
  defstruct [:key, :iv]

  @typedoc "Decoded key bytes, and the fixed IV if any."
  @type t :: %__MODULE__{key: binary(), iv: binary() | nil}

  @doc "Builds an encryption key; `opts` takes a fixed `:iv`."
  @spec new(XtandardImage.key_material(), keyword()) :: {:ok, t()} | {:error, Error.t()}
  def new(key, opts \\ []), do: Error.wrap(fn -> new!(key, opts) end)

  @doc "Like `new/2`, raising `XtandardImage.Error`."
  @spec new!(XtandardImage.key_material(), keyword()) :: t()
  def new!(key, opts \\ []) do
    k = Text.key_bytes!(key, "encryption key")

    if byte_size(k) not in Spec.encryption_key_sizes() do
      Error.raise!(:invalid_key, "encryption key is #{byte_size(k)} bytes, want 16, 24 or 32")
    end

    iv =
      case Keyword.get(opts, :iv) do
        nil ->
          nil

        material ->
          iv = Text.key_bytes!(material, "iv")

          if byte_size(iv) != Spec.iv_size(),
            do: Error.raise!(:invalid_key, "iv is not #{Spec.iv_size()} bytes")

          iv
      end

    %__MODULE__{key: k, iv: iv}
  end
end
