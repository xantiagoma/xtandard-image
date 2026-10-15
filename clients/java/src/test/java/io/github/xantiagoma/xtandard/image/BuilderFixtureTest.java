package io.github.xantiagoma.xtandard.image;

import static io.github.xantiagoma.xtandard.image.Fixtures.list;
import static io.github.xantiagoma.xtandard.image.Fixtures.obj;
import static io.github.xantiagoma.xtandard.image.Fixtures.str;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.stream.Stream;
import org.junit.jupiter.api.DynamicTest;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestFactory;

/**
 * testdata/urls/imgproxy-builder.json: 400+ cases derived from the server's golden tables. The URLs
 * were written by the TypeScript client and parsed back by the Go dialect tests; every client must
 * write the same ones: {@code url} with option names as given, {@code url_short} with the default
 * short names, and {@code query_url} where the case has one.
 */
class BuilderFixtureTest {
  private static final Map<String, Object> FIXTURE =
      Fixtures.load("testdata/urls/imgproxy-builder.json");
  private static final List<Object> CASES = list(FIXTURE.get("cases"));

  @Test
  void coversTheTables() {
    assertTrue(CASES.size() >= 400, "cases: " + CASES.size());
  }

  @TestFactory
  Stream<DynamicTest> cases() {
    return CASES.stream()
        .map(Fixtures::obj)
        .map(c -> DynamicTest.dynamicTest(str(c, "name"), () -> check(c)));
  }

  private static void check(Map<String, Object> c) {
    assertEquals(str(c, "url"), imgproxy(c, OptionNameStyle.GIVEN), "url (names as given)");
    assertEquals(str(c, "url_short"), imgproxy(c, OptionNameStyle.SHORT), "url_short");
    if (Boolean.TRUE.equals(c.get("query"))) {
      assertEquals(str(c, "query_url"), query(c), "query_url");
    }
  }

  private static SigningKey signing(Map<String, Object> c) {
    Object size = c.get("signature_size");
    if (size == null || ((Number) size).intValue() == 0) {
      return null;
    }
    Map<String, Object> k = obj(FIXTURE.get("signing"));
    return SigningKey.of(str(k, "key"), str(k, "salt")).withSize(((Number) size).intValue());
  }

  private static void addAll(UrlChain<?> chain, Object pipeline) {
    for (Object entry : list(pipeline)) {
      for (Map.Entry<String, Object> e : obj(entry).entrySet()) {
        chain.option(e.getKey(), list(e.getValue()).toArray());
      }
    }
  }

  private static String imgproxy(Map<String, Object> c, OptionNameStyle style) {
    Map<String, Object> enc = obj(FIXTURE.get("encryption"));
    ImgproxyUrlBuilder b =
        ImgproxyUrlBuilder.builder()
            .unsignedSignature("unsafe")
            .encryption(EncryptionKey.of(str(enc, "key")).withIv(str(enc, "iv")))
            .signing(signing(c))
            .separator(c.containsKey("separator") ? str(c, "separator") : ":")
            .optionNames(style)
            .build();
    SourceEncoding encoding = SourceEncoding.valueOf(str(c, "encoding").toUpperCase(Locale.ROOT));
    List<Object> pipelines = list(c.get("pipelines"));
    String ext = str(c, "extension");
    String endpoint = str(c, "endpoint");
    if ("info".equals(endpoint)) {
      ImgproxyInfoUrlChain chain = b.info(str(c, "source")).encoding(encoding);
      pipelines.forEach(p -> addAll(chain, p));
      return chain.build();
    }
    if ("video".equals(endpoint)) {
      ImgproxyVideoUrlChain chain = b.video(str(c, "source")).encoding(encoding);
      pipelines.forEach(p -> addAll(chain, p));
      if (ext != null && !ext.isEmpty()) {
        chain.extension(ext);
      }
      return chain.build();
    }
    ImgproxyUrlChain chain = b.image(str(c, "source")).encoding(encoding);
    for (int i = 0; i < pipelines.size(); i++) {
      if (i > 0) {
        chain.pipeline();
      }
      addAll(chain, pipelines.get(i));
    }
    if (ext != null && !ext.isEmpty()) {
      chain.extension(ext);
    }
    return chain.build();
  }

  private static String query(Map<String, Object> c) {
    QueryUrlBuilder q = QueryUrlBuilder.builder().signing(signing(c)).build();
    List<Object> pipelines = list(c.get("pipelines"));
    String ext = str(c, "extension");
    if ("video".equals(str(c, "endpoint"))) {
      QueryVideoUrlChain chain = q.video(str(c, "source"));
      pipelines.forEach(p -> addAll(chain, p));
      if (ext != null && !ext.isEmpty()) {
        chain.extension(ext);
      }
      return chain.build();
    }
    QueryUrlChain chain = q.image(str(c, "source"));
    for (int i = 0; i < pipelines.size(); i++) {
      if (i > 0) {
        chain.pipeline();
      }
      addAll(chain, pipelines.get(i));
    }
    if (ext != null && !ext.isEmpty()) {
      chain.extension(ext);
    }
    return chain.build();
  }
}
