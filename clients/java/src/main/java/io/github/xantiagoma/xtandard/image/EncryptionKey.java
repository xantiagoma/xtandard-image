package io.github.xantiagoma.xtandard.image;

import org.jspecify.annotations.Nullable;

/**
 * An encryption key: the first of {@code XI_ENCRYPTION_KEYS} (imgproxy {@code
 * IMGPROXY_SOURCE_URL_ENCRYPTION_KEY}), for {@code enc/} sources and PDF passwords.
 *
 * <p>Without a fixed IV every token gets a fresh random one, as the server's own encoder does. A
 * fixed IV ({@link #withIv(String)}) makes URLs deterministic and reveals when two URLs share a
 * source: use it only for tests or cache keys.
 *
 * <p>Immutable. {@link #toString()} does not reveal the key.
 */
public final class EncryptionKey {
  private final byte[] key;
  private final byte @Nullable [] iv;

  private EncryptionKey(byte[] key, byte @Nullable [] iv) {
    if (!Constants.ENCRYPTION_KEY_SIZES.contains(key.length)) {
      throw new UrlBuildException(
          ErrorCode.INVALID_KEY, "encryption key is " + key.length + " bytes, want 16, 24 or 32");
    }
    if (iv != null && iv.length != Constants.IV_SIZE) {
      throw new UrlBuildException(
          ErrorCode.INVALID_KEY, "iv is not " + Constants.IV_SIZE + " bytes");
    }
    this.key = key;
    this.iv = iv;
  }

  /**
   * A key from hex, as in the server configuration.
   *
   * @param keyHex the AES key, hex: 16, 24 or 32 bytes
   * @return the key
   * @throws UrlBuildException {@link ErrorCode#INVALID_KEY} when it is not hex or has another size
   */
  public static EncryptionKey of(String keyHex) {
    return new EncryptionKey(Text.hex(keyHex, "encryption key"), null);
  }

  /**
   * A key from raw bytes (copied).
   *
   * @param key the AES key: 16, 24 or 32 bytes
   * @return the key
   * @throws UrlBuildException {@link ErrorCode#INVALID_KEY} for another size
   */
  public static EncryptionKey of(byte[] key) {
    return new EncryptionKey(key.clone(), null);
  }

  /**
   * This key with a fixed IV (deterministic tokens).
   *
   * @param ivHex the IV, hex: 16 bytes
   * @return a new key
   * @throws UrlBuildException {@link ErrorCode#INVALID_KEY} when it is not 16 bytes of hex
   */
  public EncryptionKey withIv(String ivHex) {
    return new EncryptionKey(key, Text.hex(ivHex, "iv"));
  }

  /**
   * This key with a fixed IV (deterministic tokens).
   *
   * @param iv the IV: 16 bytes (copied)
   * @return a new key
   * @throws UrlBuildException {@link ErrorCode#INVALID_KEY} when it is not 16 bytes
   */
  public EncryptionKey withIv(byte[] iv) {
    return new EncryptionKey(key, iv.clone());
  }

  byte[] key() {
    return key;
  }

  byte @Nullable [] iv() {
    return iv;
  }

  @Override
  public String toString() {
    return "EncryptionKey[bytes=" + key.length + (iv == null ? "" : ", fixed iv") + "]";
  }
}
