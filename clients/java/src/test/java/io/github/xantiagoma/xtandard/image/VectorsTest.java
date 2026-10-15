package io.github.xantiagoma.xtandard.image;

import static io.github.xantiagoma.xtandard.image.Fixtures.list;
import static io.github.xantiagoma.xtandard.image.Fixtures.str;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.Base64;
import java.util.HexFormat;
import java.util.List;
import java.util.Map;
import java.util.function.Consumer;
import java.util.stream.Stream;
import javax.crypto.Cipher;
import javax.crypto.spec.IvParameterSpec;
import javax.crypto.spec.SecretKeySpec;
import org.junit.jupiter.api.DynamicTest;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestFactory;

/**
 * testdata/security/vectors.json: the signing, enc/ and pdf_password vectors shared with Go and TS.
 */
class VectorsTest {
  private static final Map<String, Object> V = Fixtures.load("testdata/security/vectors.json");

  private static Stream<DynamicTest> each(String group, Consumer<Map<String, Object>> check) {
    return list(V.get(group)).stream()
        .map(Fixtures::obj)
        .map(v -> DynamicTest.dynamicTest(group + ":" + str(v, "name"), () -> check.accept(v)));
  }

  private static int size(Map<String, Object> v) {
    return ((Number) v.get("size")).intValue();
  }

  static String decrypt(String token, String keyHex) throws Exception {
    byte[] raw = Base64.getUrlDecoder().decode(token);
    Cipher c = Cipher.getInstance("AES/CBC/PKCS5Padding");
    c.init(
        Cipher.DECRYPT_MODE,
        new SecretKeySpec(HexFormat.of().parseHex(keyHex), "AES"),
        new IvParameterSpec(Arrays.copyOf(raw, 16)));
    return new String(c.doFinal(raw, 16, raw.length - 16), StandardCharsets.UTF_8);
  }

  @TestFactory
  Stream<DynamicTest> imgproxy() {
    return each(
        "imgproxy",
        v -> {
          SigningKey k = SigningKey.of(str(v, "key"), str(v, "salt")).withSize(size(v));
          String path = str(v, "path");
          assertEquals(str(v, "signature"), Signing.sign(path, k));
          String p = path.startsWith("/") ? path : "/" + path;
          assertEquals("/" + str(v, "signature") + p, Signing.signImgproxyPath(path, k));
          SigningKey raw =
              SigningKey.of(
                      HexFormat.of().parseHex(str(v, "key")),
                      HexFormat.of().parseHex(str(v, "salt")))
                  .withSize(size(v));
          assertEquals(str(v, "signature"), Signing.sign(path, raw));
        });
  }

  @TestFactory
  Stream<DynamicTest> xiSig() {
    return each(
        "xi_sig",
        v -> {
          SigningKey k = SigningKey.of(str(v, "key"), str(v, "salt")).withSize(size(v));
          String path = str(v, "path");
          assertEquals(str(v, "signed_query"), Signing.signXiQuery(path, str(v, "query"), k));
          assertEquals(
              str(v, "signed_query"), Signing.signXiQuery(path, str(v, "signed_query"), k));
          assertEquals(
              "https://img.example.com" + path + "?" + str(v, "signed_query"),
              Signing.signXiUrl("https://img.example.com" + path + "?" + str(v, "query"), k));
        });
  }

  @TestFactory
  Stream<DynamicTest> encryption() {
    return each(
        "encryption",
        v -> {
          String token =
              Encryption.encryptSource(
                  str(v, "plaintext"), EncryptionKey.of(str(v, "key")).withIv(str(v, "iv")));
          assertEquals(str(v, "token"), token);
          try {
            assertEquals(str(v, "plaintext"), decrypt(token, str(v, "key")));
          } catch (Exception e) {
            throw new AssertionError(e);
          }
        });
  }

  @TestFactory
  Stream<DynamicTest> pdfPassword() {
    return each(
        "pdf_password",
        v ->
            assertEquals(
                str(v, "token"),
                Encryption.encryptPdfPassword(
                    str(v, "password"), EncryptionKey.of(str(v, "key")).withIv(str(v, "iv")))));
  }

  @Test
  void randomIv() throws Exception {
    String key = str(Fixtures.obj(list(V.get("pdf_password")).get(0)), "key");
    EncryptionKey k = EncryptionKey.of(key);
    String a = Encryption.encryptPdfPassword("s3cret", k);
    String b = Encryption.encryptPdfPassword("s3cret", k);
    assertNotEquals(a, b);
    assertEquals("s3cret", decrypt(a, key));
  }

  @Test
  void xiMessage() {
    assertEquals(
        new Signing.XiMessage("s", "/?url=x&w=1"), Signing.xiMessage("/", "url=x&&xi_sig=s&w=1"));
    Signing.XiMessage none = Signing.xiMessage("/", "");
    assertNull(none.signature());
    assertEquals("/", none.message());
    UrlBuildException e =
        assertThrows(UrlBuildException.class, () -> Signing.xiMessage("/", "xi_sig=a&xi_sig=b"));
    assertEquals(ErrorCode.INVALID_REQUEST, e.code());
  }

  @Test
  void keysHideTheirSecrets() {
    SigningKey k = SigningKey.of("00".repeat(32), "11".repeat(32));
    assertFalse(k.toString().contains("00"));
    assertFalse(k.toString().contains("11"));
    EncryptionKey e = EncryptionKey.of("ab".repeat(32));
    assertFalse(e.toString().contains("ab"));
    assertEquals(List.of(16, 24, 32), Constants.ENCRYPTION_KEY_SIZES);
  }

  @Test
  void hexKeysAreStrict() {
    for (String bad : List.of("0", "zz", "0g", "１２")) {
      UrlBuildException e = assertThrows(UrlBuildException.class, () -> SigningKey.of(bad, "00"));
      assertEquals(ErrorCode.INVALID_KEY, e.code());
    }
    SigningKey.of(" 0A0b ", "");
  }
}
