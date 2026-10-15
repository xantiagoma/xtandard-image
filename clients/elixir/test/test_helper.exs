# The smoke test needs a running server: XI_SMOKE_URL=http://127.0.0.1:8080 mix test
exclude = if System.get_env("XI_SMOKE_URL", "") == "", do: [:smoke], else: []
ExUnit.start(exclude: exclude)
