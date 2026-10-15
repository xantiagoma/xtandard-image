package io.github.xantiagoma.xtandard.image;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import java.util.Map;

/** The shared fixtures, read from the repository by relative path (tests run in clients/java). */
final class Fixtures {
  private Fixtures() {}

  static final Path ROOT = Path.of("../..").toAbsolutePath().normalize();

  static Map<String, Object> load(String rel) {
    try {
      return obj(Json.parse(Files.readString(ROOT.resolve(rel), StandardCharsets.UTF_8)));
    } catch (IOException e) {
      throw new UncheckedIOException(e);
    }
  }

  @SuppressWarnings("unchecked")
  static Map<String, Object> obj(Object v) {
    return (Map<String, Object>) v;
  }

  @SuppressWarnings("unchecked")
  static List<Object> list(Object v) {
    return (List<Object>) v;
  }

  static String str(Map<String, Object> m, String k) {
    return (String) m.get(k);
  }
}
