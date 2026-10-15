package io.github.xantiagoma.xtandard.image;

import static io.github.xantiagoma.xtandard.image.Fixtures.list;
import static io.github.xantiagoma.xtandard.image.Fixtures.obj;
import static io.github.xantiagoma.xtandard.image.Fixtures.str;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.stream.Stream;
import org.junit.jupiter.api.Assumptions;
import org.junit.jupiter.api.DynamicTest;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestFactory;

/**
 * clients/spec/conformance.json: the cases every official client runs (ADR 0013).
 *
 * <p>Positive cases must produce expect.value byte for byte; error cases must throw {@link
 * UrlBuildException} with expect.error as its code. Each case is built through the object form
 * (option lists and {@link UrlParams}) and, where one exists, the fluent chain; both must agree.
 */
class ConformanceTest {
  private static final Map<String, Object> FIXTURE = Fixtures.load("clients/spec/conformance.json");
  private static final Map<String, Object> KEYS = obj(FIXTURE.get("keys"));
  private static final List<Object> CASES = list(FIXTURE.get("cases"));

  /** A case the Java API cannot express; the test is skipped with the reason. */
  static final class NotExpressible extends RuntimeException {
    private static final long serialVersionUID = 1L;

    NotExpressible(String reason) {
      super(reason);
    }
  }

  @Test
  void fixtureCoversEveryOp() {
    long ops = CASES.stream().map(c -> str(obj(c), "op")).distinct().count();
    assertTrue(ops >= 17, "ops: " + ops);
    assertTrue(CASES.size() >= 100, "cases: " + CASES.size());
  }

  @TestFactory
  Stream<DynamicTest> cases() {
    return CASES.stream()
        .map(Fixtures::obj)
        .map(c -> DynamicTest.dynamicTest(str(c, "op") + ":" + str(c, "name"), () -> run(c)));
  }

  private static void run(Map<String, Object> c) {
    Map<String, Object> expect = obj(c.get("expect"));
    String error = str(expect, "error");
    try {
      if (error != null) {
        UrlBuildException e = buildError(c);
        assertEquals(error, e.code().code(), e.getMessage());
        if (fluentReaches(c)) {
          UrlBuildException f = assertThrows(UrlBuildException.class, () -> fluent(c));
          assertEquals(error, f.code().code(), "fluent: " + f.getMessage());
        }
        return;
      }
      String want = str(expect, "value");
      assertEquals(want, mapping(c));
      String f = fluent(c);
      if (f != null) {
        assertEquals(want, f, "fluent form");
      }
    } catch (NotExpressible e) {
      Assumptions.abort("not expressible in Java: " + e.getMessage());
    }
  }

  /** The object form's error; {@link NotExpressible} passes through (the case is skipped). */
  private static UrlBuildException buildError(Map<String, Object> c) {
    String got;
    try {
      got = mapping(c);
    } catch (UrlBuildException e) {
      return e;
    }
    throw new AssertionError("want an error, got " + got);
  }

  /** Whether the fluent form can express the case (so it must reach the same error). */
  private static boolean fluentReaches(Map<String, Object> c) {
    try {
      return fluent(c) != null;
    } catch (UrlBuildException e) {
      return true;
    } catch (NotExpressible e) {
      return false;
    }
  }

  // ---- configuration ----------------------------------------------------------------------------

  static Map<String, Object> config(Map<String, Object> c) {
    Object cfg = c.get("config");
    return cfg == null ? Map.of() : obj(cfg);
  }

  static SigningKey signing(Map<String, Object> cfg) {
    Object s = cfg.get("signing");
    if (s == null) {
      return null;
    }
    Map<String, Object> k = Boolean.TRUE.equals(s) ? obj(KEYS.get("signing")) : obj(s);
    SigningKey key = SigningKey.of(str(k, "key"), str(k, "salt"));
    Object size = cfg.get("signature_size");
    return size == null ? key : key.withSize(((Number) size).intValue());
  }

  static EncryptionKey encryption(Map<String, Object> cfg) {
    Object e = cfg.get("encryption");
    if (e == null) {
      return null;
    }
    Map<String, Object> k = Boolean.TRUE.equals(e) ? obj(KEYS.get("encryption")) : obj(e);
    EncryptionKey key = EncryptionKey.of(str(k, "key"));
    return k.get("iv") == null ? key : key.withIv(str(k, "iv"));
  }

  static SourceEncoding encoding(Object v) {
    return v == null ? null : SourceEncoding.valueOf(((String) v).toUpperCase(Locale.ROOT));
  }

  static OptionNameStyle style(Object v) {
    return OptionNameStyle.valueOf(((String) v).toUpperCase(Locale.ROOT));
  }

  static ImgproxyUrlBuilder imgproxy(Map<String, Object> cfg) {
    ImgproxyUrlBuilder.Builder b =
        ImgproxyUrlBuilder.builder().signing(signing(cfg)).encryption(encryption(cfg));
    if (cfg.containsKey("base_url")) {
      b.baseUrl(str(cfg, "base_url"));
    }
    if (cfg.containsKey("unsigned_signature")) {
      b.unsignedSignature(str(cfg, "unsigned_signature"));
    }
    if (cfg.containsKey("source_encoding")) {
      b.sourceEncoding(encoding(cfg.get("source_encoding")));
    }
    if (cfg.containsKey("split_source")) {
      b.splitSource(((Number) cfg.get("split_source")).intValue());
    }
    if (cfg.containsKey("separator")) {
      b.separator(str(cfg, "separator"));
    }
    if (cfg.containsKey("option_names")) {
      b.optionNames(style(cfg.get("option_names")));
    }
    return b.build();
  }

  static QueryUrlBuilder query(Map<String, Object> cfg) {
    QueryUrlBuilder.Builder b =
        QueryUrlBuilder.builder().signing(signing(cfg)).encryption(encryption(cfg));
    if (cfg.containsKey("base_url")) {
      b.baseUrl(str(cfg, "base_url"));
    }
    if (cfg.containsKey("mount")) {
      b.mount(str(cfg, "mount"));
    }
    if (cfg.containsKey("separator")) {
      b.separator(str(cfg, "separator"));
    }
    if (cfg.containsKey("option_names")) {
      b.optionNames(style(cfg.get("option_names")));
    }
    return b.build();
  }

  // ---- inputs -----------------------------------------------------------------------------------

  /** The pipelines: lists of [name, [args...]]. */
  static List<List<Object>> pipelines(Map<String, Object> in) {
    Object p = in.get("options");
    List<List<Object>> out = new ArrayList<>();
    if (p != null) {
      for (Object pl : list(p)) {
        out.add(list(pl));
      }
    }
    return out;
  }

  static <O extends OptionList<O>> O fill(O options, List<Object> pipeline) {
    for (Object e : pipeline) {
      List<Object> pair = list(e);
      options.option((String) pair.get(0), list(pair.get(1)).toArray());
    }
    return options;
  }

  static List<ProcessingOptions> processing(Map<String, Object> in) {
    List<ProcessingOptions> out = new ArrayList<>();
    for (List<Object> pl : pipelines(in)) {
      out.add(fill(ProcessingOptions.create(), pl));
    }
    return out;
  }

  static List<VideoOptions> video(Map<String, Object> in) {
    List<VideoOptions> out = new ArrayList<>();
    for (List<Object> pl : pipelines(in)) {
      out.add(fill(VideoOptions.create(), pl));
    }
    return out;
  }

  /** One pipeline's options, for the endpoints that take a single options list. */
  static <O extends OptionList<O>> O single(Map<String, Object> in, O empty, String what) {
    List<List<Object>> p = pipelines(in);
    if (p.size() > 1) {
      throw new NotExpressible(
          what + " takes one options list; chained pipelines are not in its type");
    }
    return p.isEmpty() ? empty : fill(empty, p.get(0));
  }

  static UrlParams urlParams(Map<String, Object> in) {
    return UrlParams.of()
        .withExtension(str(in, "extension"))
        .withSourceEncoding(encoding(in.get("source_encoding")))
        .withPdfPassword(str(in, "pdf_password"));
  }

  static StreamParams stream(Map<String, Object> in) {
    StreamParams s = StreamParams.of(str(in, "protocol"));
    if (in.get("renditions") != null) {
      List<Integer> r = new ArrayList<>();
      for (Object o : list(in.get("renditions"))) {
        r.add(((Number) o).intValue());
      }
      s = s.withRenditions(r);
    }
    if (in.get("segment_duration") != null) {
      s = s.withSegmentDuration(((Number) in.get("segment_duration")).intValue());
    }
    s = s.withSegmentFormat(str(in, "segment_format"));
    if (in.get("options") != null) {
      s = s.withOptions(single(in, VideoOptions.create(), "streamUrl"));
    }
    return s.withSourceEncoding(encoding(in.get("source_encoding")));
  }

  static QueryParams queryParams(Map<String, Object> in) {
    QueryParams q = QueryParams.create();
    Object p = in.get("params");
    if (p != null) {
      for (Object e : list(p)) {
        List<Object> pair = list(e);
        q.param((String) pair.get(0), pair.get(1));
      }
    }
    return q;
  }

  static ImaginaryParams imaginaryParams(Map<String, Object> in) {
    ImaginaryParams q = ImaginaryParams.create();
    Object p = in.get("params");
    if (p != null) {
      for (Object e : list(p)) {
        List<Object> pair = list(e);
        q.param((String) pair.get(0), pair.get(1));
      }
    }
    return q;
  }

  static ByteRange range(Object r) {
    if (r instanceof String s) {
      return ByteRange.raw(s);
    }
    Map<String, Object> m = obj(r);
    if (m.containsKey("suffix")) {
      return ByteRange.suffix(((Number) m.get("suffix")).longValue());
    }
    long start = ((Number) m.get("start")).longValue();
    return m.get("end") == null
        ? ByteRange.from(start)
        : ByteRange.of(start, ((Number) m.get("end")).longValue());
  }

  // ---- the two forms ----------------------------------------------------------------------------

  /** The case through the object form. */
  static String mapping(Map<String, Object> c) {
    Map<String, Object> cfg = config(c);
    Map<String, Object> in = obj(c.get("input"));
    String source = str(in, "source");
    return switch (str(c, "op")) {
      case "imgproxy.url" -> imgproxy(cfg).url(source, processing(in), urlParams(in));
      case "imgproxy.info_url" ->
          imgproxy(cfg).infoUrl(source, single(in, InfoOptions.create(), "infoUrl"), urlParams(in));
      case "imgproxy.video_url" ->
          imgproxy(cfg)
              .videoUrl(source, single(in, VideoOptions.create(), "videoUrl"), urlParams(in));
      case "imgproxy.stream_url" -> imgproxy(cfg).streamUrl(source, stream(in));
      case "query.url" -> query(cfg).url(source, queryParams(in), processing(in), urlParams(in));
      case "query.video_url" -> query(cfg).videoUrl(source, video(in), str(in, "extension"));
      case "query.stream_url" -> query(cfg).streamUrl(source, stream(in));
      case "query.imaginary_url" ->
          query(cfg).imaginaryUrl(str(in, "endpoint"), source, imaginaryParams(in));
      case "sign" -> Signing.sign(str(in, "message"), signing(cfg));
      case "sign_xi_url" ->
          Signing.signXiUrl(
              str(in, "url"), signing(cfg), in.containsKey("mount") ? str(in, "mount") : "");
      case "encrypt_source" -> Encryption.encryptSource(source, encryption(cfg));
      case "encrypt_pdf_password" ->
          Encryption.encryptPdfPassword(str(in, "password"), encryption(cfg));
      case "xi_source" -> Sources.xiSource(str(in, "name"), str(in, "key"));
      case "source_url" ->
          Sources.sourceUrl(str(in, "scheme"), str(in, "authority"), str(in, "key"));
      case "range_header" -> XtandardClient.rangeHeader(range(in.get("range")));
      case "escape_plain_source" -> Sources.escapePlainSource(str(in, "value"));
      case "escape_query_value" -> Sources.escapeQueryValue(str(in, "value"));
      default -> throw new AssertionError("unknown op " + str(c, "op"));
    };
  }

  /** The case through the fluent chains, for the ops that have one; null otherwise. */
  static String fluent(Map<String, Object> c) {
    Map<String, Object> cfg = config(c);
    Map<String, Object> in = obj(c.get("input"));
    String source = str(in, "source");
    List<List<Object>> pipelines = pipelines(in);
    String ext = str(in, "extension");
    SourceEncoding enc = encoding(in.get("source_encoding"));
    String pdp = str(in, "pdf_password");
    switch (str(c, "op")) {
      case "imgproxy.url" -> {
        ImgproxyUrlChain chain = imgproxy(cfg).image(source);
        for (int i = 0; i < pipelines.size(); i++) {
          if (i > 0) {
            chain.pipeline();
          }
          addAll(chain, pipelines.get(i));
        }
        if (enc != null) {
          chain.encoding(enc);
        }
        if (ext != null) {
          chain.extension(ext);
        }
        if (pdp != null) {
          chain.withPdfPassword(pdp);
        }
        return chain.build();
      }
      case "imgproxy.info_url" -> {
        if (pipelines.size() > 1) {
          throw new NotExpressible("the /info chain has no pipeline()");
        }
        ImgproxyInfoUrlChain chain = imgproxy(cfg).info(source);
        pipelines.forEach(p -> addAll(chain, p));
        if (enc != null) {
          chain.encoding(enc);
        }
        if (pdp != null) {
          chain.withPdfPassword(pdp);
        }
        return chain.build();
      }
      case "imgproxy.video_url" -> {
        if (pipelines.size() > 1 || pdp != null) {
          throw new NotExpressible("the /video chain has no pipeline() and no withPdfPassword()");
        }
        ImgproxyVideoUrlChain chain = imgproxy(cfg).video(source);
        pipelines.forEach(p -> addAll(chain, p));
        if (enc != null) {
          chain.encoding(enc);
        }
        if (ext != null) {
          chain.extension(ext);
        }
        return chain.build();
      }
      case "query.url" -> {
        QueryUrlChain chain = query(cfg).image(source);
        Object p = in.get("params");
        if (p != null) {
          for (Object e : list(p)) {
            chain.param((String) list(e).get(0), list(e).get(1));
          }
        }
        for (int i = 0; i < pipelines.size(); i++) {
          if (i > 0) {
            chain.pipeline();
          }
          addAll(chain, pipelines.get(i));
        }
        if (ext != null) {
          chain.extension(ext);
        }
        if (pdp != null) {
          chain.withPdfPassword(pdp);
        }
        return chain.build();
      }
      case "query.video_url" -> {
        // Several option lists are written in order, as one chain writes them.
        QueryVideoUrlChain chain = query(cfg).video(source);
        pipelines.forEach(p -> addAll(chain, p));
        if (ext != null) {
          chain.extension(ext);
        }
        return chain.build();
      }
      default -> {
        return null;
      }
    }
  }

  private static void addAll(UrlChain<?> chain, List<Object> pipeline) {
    for (Object e : pipeline) {
      List<Object> pair = list(e);
      chain.option((String) pair.get(0), list(pair.get(1)).toArray());
    }
  }
}
