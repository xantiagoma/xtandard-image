# frozen_string_literal: true

require "socket"

# A minimal HTTP/1.1 server on 127.0.0.1 for the client tests: one thread
# per connection, Connection: close, the handler returns [status, headers,
# body] for a request Hash ("method", "path", "query", "headers" with
# lower-case names, "body"). Stdlib only (WEBrick left the default gems).
class StandInServer
  REASONS = { 200 => "OK", 206 => "Partial Content", 403 => "Forbidden", 404 => "Not Found", 429 => "Too Many Requests",
              502 => "Bad Gateway", 503 => "Service Unavailable", }.freeze

  attr_reader :requests

  def initialize(&handler)
    @handler = handler
    @server = TCPServer.new("127.0.0.1", 0)
    @requests = Queue.new
    @thread = Thread.new { accept_loop }
  end

  def base_url = "http://127.0.0.1:#{@server.addr[1]}"

  # The requests seen so far, oldest first.
  def seen
    out = []
    out << @requests.pop until @requests.empty?
    out
  end

  def close
    @server.close
    @thread.join
  end

  private

  def accept_loop
    loop do
      sock = @server.accept
      Thread.new(sock) { |s| serve(s) }
    end
  rescue IOError, Errno::EBADF
    nil
  end

  def serve(sock)
    req = read_request(sock)
    return if req.nil?

    @requests << req
    status, headers, body = @handler.call(req)
    write_response(sock, req, status, headers, body)
  ensure
    sock.close
  end

  def read_request(sock)
    line = sock.gets("\r\n")
    return nil if line.nil?

    method, target, = line.split(" ", 3)
    headers = {}
    while (h = sock.gets("\r\n")) && h != "\r\n"
      name, value = h.split(":", 2)
      headers[name.to_s.strip.downcase] = value.to_s.strip
    end
    path, query = target.to_s.split("?", 2)
    length = headers["content-length"].to_i
    body = length.positive? ? sock.read(length).to_s : ""
    { "method" => method, "path" => path, "query" => query, "target" => target, "headers" => headers, "body" => body }
  end

  def write_response(sock, req, status, headers, body)
    body = body.to_s.b
    head = ["HTTP/1.1 #{status} #{REASONS.fetch(status, "Status")}", "Content-Length: #{body.bytesize}", "Connection: close"]
    headers.each { |k, v| head << "#{k}: #{v}" }
    sock.write("#{head.join("\r\n")}\r\n\r\n")
    sock.write(body) unless req["method"] == "HEAD"
  end
end
