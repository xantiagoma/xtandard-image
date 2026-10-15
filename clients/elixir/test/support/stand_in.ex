defmodule XtandardImage.StandIn do
  @moduledoc false
  # A stand-in HTTP/1.1 server on :gen_tcp for the client tests: it parses
  # the request line and headers (packet: :http_bin), asks a handler for the
  # response and closes the connection. The handler returns
  # {status, headers, body}, or :close to drop the connection unanswered.

  @type request :: %{
          method: String.t(),
          path: String.t(),
          query: String.t(),
          headers: %{optional(String.t()) => String.t()}
        }
  @type response :: {pos_integer(), [{String.t(), String.t()}], binary()} | :close

  @doc "Starts a server; returns its base URL and the listening socket."
  @spec start((request() -> response())) :: {String.t(), :gen_tcp.socket()}
  def start(handler) do
    {:ok, listen} =
      :gen_tcp.listen(0, [
        :binary,
        packet: :http_bin,
        active: false,
        reuseaddr: true,
        ip: {127, 0, 0, 1}
      ])

    {:ok, port} = :inet.port(listen)
    pid = spawn(fn -> accept(listen, handler) end)
    :ok = :gen_tcp.controlling_process(listen, pid)
    {"http://127.0.0.1:#{port}", listen}
  end

  @doc "Stops a server."
  @spec stop(:gen_tcp.socket()) :: :ok
  def stop(listen), do: :gen_tcp.close(listen)

  defp accept(listen, handler) do
    case :gen_tcp.accept(listen) do
      {:ok, socket} ->
        serve(socket, handler)
        accept(listen, handler)

      {:error, _} ->
        :ok
    end
  end

  defp serve(socket, handler) do
    with {:ok, {:http_request, method, {:abs_path, target}, _}} <- :gen_tcp.recv(socket, 0, 5_000),
         {:ok, headers} <- read_headers(socket, %{}) do
      [path | query] = String.split(target, "?", parts: 2)
      method = to_string(method)

      request = %{method: method, path: path, query: List.first(query, ""), headers: headers}
      respond(socket, method, handler.(request))
    end

    :gen_tcp.close(socket)
  end

  defp read_headers(socket, acc) do
    case :gen_tcp.recv(socket, 0, 5_000) do
      {:ok, {:http_header, _, name, _, value}} ->
        read_headers(socket, Map.put(acc, String.downcase(to_string(name)), value))

      {:ok, :http_eoh} ->
        {:ok, acc}

      other ->
        other
    end
  end

  defp respond(_socket, _method, :close), do: :ok

  defp respond(socket, method, {status, headers, body}) do
    head =
      [
        "HTTP/1.1 #{status} X\r\n",
        "content-length: #{byte_size(body)}\r\n",
        "connection: close\r\n"
      ] ++
        Enum.map(headers, fn {k, v} -> "#{k}: #{v}\r\n" end) ++ ["\r\n"]

    :gen_tcp.send(socket, [head, if(method == "HEAD", do: "", else: body)])
  end
end
