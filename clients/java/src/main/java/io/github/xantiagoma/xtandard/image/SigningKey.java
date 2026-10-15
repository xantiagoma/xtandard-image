package io.github.xantiagoma.xtandard.image;

/**
 * A signing key: {@code XI_SIGNING_KEYS} and {@code XI_SIGNING_SALTS} (imgproxy {@code
 * IMGPROXY_KEY} and {@code IMGPROXY_SALT}), plus the signature size ({@code XI_SIGNING_SIZE}).
 *
 * <p>Immutable. {@link #toString()} does not reveal the key or the salt.
 */
public final class SigningKey {
  private final byte[] key;
  private final byte[] salt;
  private final int size;

  private SigningKey(byte[] key, byte[] salt, int size) {
    if (size < Constants.MIN_SIGNATURE_SIZE || size > Constants.MAX_SIGNATURE_SIZE) {
      throw new UrlBuildException(
          ErrorCode.INVALID_KEY,
          "signature size "
              + size
              + " out of range ["
              + Constants.MIN_SIGNATURE_SIZE
              + ", "
              + Constants.MAX_SIGNATURE_SIZE
              + "]");
    }
    this.key = key;
    this.salt = salt;
    this.size = size;
  }

  /**
   * A key from hex strings, as in the server configuration, with 32-byte signatures.
   *
   * @param keyHex the HMAC key, hex (either case)
   * @param saltHex the salt, hex
   * @return the key
   * @throws UrlBuildException {@link ErrorCode#INVALID_KEY} when either is not hex
   */
  public static SigningKey of(String keyHex, String saltHex) {
    return new SigningKey(
        Text.hex(keyHex, "signing key"),
        Text.hex(saltHex, "signing salt"),
        Constants.DEFAULT_SIGNATURE_SIZE);
  }

  /**
   * A key from raw bytes (copied), with 32-byte signatures.
   *
   * @param key the HMAC key
   * @param salt the salt
   * @return the key
   */
  public static SigningKey of(byte[] key, byte[] salt) {
    return new SigningKey(key.clone(), salt.clone(), Constants.DEFAULT_SIGNATURE_SIZE);
  }

  /**
   * This key with another signature size.
   *
   * @param size the signature size in bytes before encoding, 1 to 32
   * @return a new key
   * @throws UrlBuildException {@link ErrorCode#INVALID_KEY} for a size out of range
   */
  public SigningKey withSize(int size) {
    return new SigningKey(key, salt, size);
  }

  /**
   * The signature size in bytes before encoding.
   *
   * @return 1 to 32
   */
  public int size() {
    return size;
  }

  byte[] key() {
    return key;
  }

  byte[] salt() {
    return salt;
  }

  @Override
  public String toString() {
    return "SigningKey[size=" + size + "]";
  }
}
