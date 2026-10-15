package io.github.xantiagoma.xtandard.image;

import java.security.GeneralSecurityException;
import java.security.SecureRandom;
import javax.crypto.Cipher;
import javax.crypto.spec.IvParameterSpec;
import javax.crypto.spec.SecretKeySpec;

/**
 * Source URL and PDF password encryption, byte-compatible with the server ({@code
 * internal/security/crypt}): {@code token = base64url_nopad(IV[16] || AES-CBC(key, IV,
 * PKCS#7(utf8(plaintext))))}.
 *
 * <p>Encrypt on a server: the key decrypts every token.
 */
public final class Encryption {
  private Encryption() {}

  private static final SecureRandom RANDOM = new SecureRandom();

  /**
   * Encrypts a source URL into an {@code enc/} token.
   *
   * @param source the source URL
   * @param key the encryption key
   * @return the token (without {@code enc/})
   * @throws UrlBuildException {@link ErrorCode#INVALID_SOURCE} for an empty source or one with
   *     control characters
   */
  public static String encryptSource(String source, EncryptionKey key) {
    return seal(Text.checkText(source, ErrorCode.INVALID_SOURCE, "source"), key);
  }

  /**
   * Encrypts the password of a password-protected PDF into a {@code pdf_password} ({@code pdp})
   * token: the {@code enc/} format over the password, with the same key. The server accepts the
   * option on signed URLs only.
   *
   * @param password the plain-text password
   * @param key the encryption key
   * @return the token
   * @throws UrlBuildException {@link ErrorCode#INVALID_PASSWORD} for an empty password, one with
   *     control characters, or one longer than 1024 UTF-8 bytes
   */
  public static String encryptPdfPassword(String password, EncryptionKey key) {
    Text.checkText(password, ErrorCode.INVALID_PASSWORD, "password");
    if (Text.utf8(password).length > Constants.MAX_PDF_PASSWORD_BYTES) {
      throw new UrlBuildException(
          ErrorCode.INVALID_PASSWORD,
          "password is longer than " + Constants.MAX_PDF_PASSWORD_BYTES + " bytes");
    }
    return seal(password, key);
  }

  private static String seal(String plaintext, EncryptionKey key) {
    byte[] fixed = key.iv();
    byte[] iv;
    if (fixed == null) {
      iv = new byte[Constants.IV_SIZE];
      RANDOM.nextBytes(iv);
    } else {
      iv = fixed;
    }
    try {
      Cipher c = Cipher.getInstance("AES/CBC/PKCS5Padding");
      c.init(Cipher.ENCRYPT_MODE, new SecretKeySpec(key.key(), "AES"), new IvParameterSpec(iv));
      byte[] ct = c.doFinal(Text.utf8(plaintext));
      byte[] out = new byte[iv.length + ct.length];
      System.arraycopy(iv, 0, out, 0, iv.length);
      System.arraycopy(ct, 0, out, iv.length, ct.length);
      return Text.b64url(out);
    } catch (GeneralSecurityException e) {
      throw new IllegalStateException("AES/CBC/PKCS5Padding is not available", e);
    }
  }
}
