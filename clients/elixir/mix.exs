defmodule XtandardImage.MixProject do
  use Mix.Project

  # Lockstep with the server's release tags (ADR 0013); 0.0.0 until the first
  # release. The release job sets it from the tag.
  @version "0.0.0"
  @source_url "https://github.com/xantiagoma/xtandard-image"

  @spec project() :: keyword()
  def project do
    [
      app: :xtandard_image,
      version: @version,
      elixir: "~> 1.17",
      elixirc_paths: elixirc_paths(Mix.env()),
      deps: deps(),
      description:
        "Typed URL builders (imgproxy, /info, /video, HLS/DASH, query dialect), signing, " <>
          "source encryption and an HTTP client for xtandard-image.",
      package: package(),
      name: "xtandard_image",
      source_url: @source_url,
      homepage_url: "#{@source_url}/tree/main/clients/elixir#readme",
      docs: docs(),
      dialyzer: [
        plt_local_path: "priv/plts",
        plt_core_path: "priv/plts",
        plt_add_apps: [:req, :crypto],
        flags: [:error_handling, :extra_return, :missing_return, :unmatched_returns]
      ]
    ]
  end

  @spec application() :: keyword()
  def application do
    [extra_applications: [:crypto]]
  end

  defp elixirc_paths(:test), do: ["lib", "test/support"]
  defp elixirc_paths(_), do: ["lib"]

  defp deps do
    [
      # The HTTP client (XtandardImage.Client) needs Req; URL building, signing
      # and encryption need nothing beyond OTP's :crypto.
      {:req, "~> 0.7", optional: true},
      {:credo, "~> 1.7", only: [:dev, :test], runtime: false},
      {:dialyxir, "~> 1.4", only: [:dev, :test], runtime: false},
      {:ex_doc, "~> 0.40", only: :dev, runtime: false}
    ]
  end

  defp package do
    [
      licenses: ["MIT"],
      links: %{
        "GitHub" => @source_url,
        "Changelog" => "#{@source_url}/releases"
      },
      files: ~w(lib mix.exs README.md LICENSE .formatter.exs)
    ]
  end

  defp docs do
    [
      main: "readme",
      extras: ["README.md"],
      source_url_pattern: "#{@source_url}/blob/main/clients/elixir/%{path}#L%{line}",
      groups_for_modules: [
        "URL builders": [
          XtandardImage.Imgproxy,
          XtandardImage.Query,
          XtandardImage.Chain,
          XtandardImage.Processing,
          XtandardImage.Info,
          XtandardImage.Video
        ],
        "Signing and encryption": [
          XtandardImage.SigningKey,
          XtandardImage.EncryptionKey,
          XtandardImage.Signing,
          XtandardImage.Encryption,
          XtandardImage.Sources
        ],
        "HTTP client": [
          XtandardImage.Client,
          XtandardImage.Readiness,
          XtandardImage.StreamManifest,
          XtandardImage.Range
        ],
        Errors: [XtandardImage.Error, XtandardImage.HTTPError, XtandardImage.TransportError],
        "Response bodies": ~r/^XtandardImage\.(Info|Error)[A-Z]/,
        Constants: [XtandardImage.Spec]
      ]
    ]
  end
end
