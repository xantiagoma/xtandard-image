# frozen_string_literal: true

# Typical calls, type-checked by Steep against sig/ (target :usage in the
# Steepfile) so the generated overloads and records accept what users write,
# and run by test/typecheck_test.rb so they stay valid code.
module Usage
  module_function

  def urls
    key = XtandardImage::SigningKey.new(key: "736563726574", salt: "68656C6C6F")
    enc = XtandardImage::EncryptionKey.new(key: "00" * 32)
    img = XtandardImage::ImgproxyURLBuilder.new(base_url: "https://img.example.com", signing: key, encryption: enc)
    q = XtandardImage::QueryURLBuilder.new(signing: key, option_names: :canonical)
    [
      img.url("s3://bucket/cat.jpg", { rs: ["fill", 300, 400], q: 80, g: :sm, bl: nil }, extension: "webp"),
      img.url("s3://bucket/cat.jpg", [{ rs: [:fit, 100, 100] }, { bl: 2 }], source_encoding: :plain),
      img.url("s3://bucket/doc.pdf", { page: 2 }, pdf_password: "secret"),
      img.image("s3://bucket/cat.jpg").resize("fill", 300, 400).quality(80).gravity(:sm).extension("webp").build,
      img.image("s3://bucket/cat.jpg").rs(:fit, 10, 10, true).pipeline.bl(2).min_width(5).to_s,
      img.info_url("s3://bucket/cat.jpg", { size: true, format: true }),
      img.info("s3://bucket/cat.jpg").size(true).encoding(:plain).build,
      img.video_url("s3://m/talk.mov", { rs: ["fill", 640, 360], vs: 1.5, vd: 8 }, extension: :webm),
      img.video("s3://m/talk.mov").start(2).mute(true).extension("mp4").build,
      img.stream_url("s3://m/talk.mov", protocol: :hls, renditions: [360, 720], segment_duration: 4, segment_format: :ts),
      q.url("s3://bucket/cat.jpg", params: { w: 300, f: "webp", enlarge: true }, options: { bl: 2 }),
      q.url("s3://bucket/cat.jpg", params: [["w", 300], ["h", nil]]),
      q.image("s3://bucket/cat.jpg").param(:w, 300).bl(2).build,
      q.video_url("s3://m/talk.mov", { vs: 2, vd: 4 }, extension: "webm"),
      q.stream_url("s3://m/talk.mov", protocol: "dash"),
      q.imaginary_url(:resize, "s3://bucket/cat.jpg", { width: 300, height: 200 }),
    ]
  end

  def helpers
    key = XtandardImage::SigningKey.new(key: "736563726574", salt: "68656C6C6F", size: 8)
    enc = XtandardImage::EncryptionKey.new(key: "00" * 16, iv: "01" * 16)
    sig, msg = XtandardImage.xi_message("/", "w=1&xi_sig=x")
    [
      XtandardImage.sign("/rs:fit:10:10/plain/a", key),
      XtandardImage.sign_xi_url("https://img.example.com/q/?url=a", key, mount: "/q"),
      XtandardImage.encrypt_source("s3://bucket/cat.jpg", enc),
      XtandardImage.encrypt_pdf_password("secret", enc),
      XtandardImage.xi_source("photos", "2026/cat.jpg"),
      XtandardImage.source_url(:s3, "bucket", "cat.jpg"),
      XtandardImage.range_header(0..1023),
      XtandardImage.range_header(XtandardImage::ByteSuffix.new(length: 500)),
      sig.to_s, msg,
    ]
  end

  # Not run: needs a server. Type-checked only.
  def client(base)
    xi = XtandardImage::Client.new(base, headers: { "X-Trace" => "1" }, read_timeout: 5)
    info = xi.info("s3://bucket/cat.jpg", { size: true })
    width = info["width"]
    res = xi.image("s3://bucket/cat.jpg", { w: 100 }, accept: "image/avif", method: :head)
    total = 0
    xi.video("s3://m/talk.mov", { vd: 2 }, extension: "webm", range: 0..99) { |chunk| total += chunk.bytesize }
    manifest = xi.stream("s3://m/talk.mov", protocol: :hls)
    [width, res.content_type, total, manifest.body, xi.readyz.ready?]
  rescue XtandardImage::NotFoundError => e
    [e.status, e.kind, e.request_id]
  end
end
