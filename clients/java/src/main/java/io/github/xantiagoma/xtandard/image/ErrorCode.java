package io.github.xantiagoma.xtandard.image;

/**
 * Why a URL, token or header could not be built: the language-neutral codes of {@code
 * clients/spec/conformance.json}, shared by every official client.
 */
public enum ErrorCode {
  /** An option name not in the catalogue. */
  UNKNOWN_OPTION("unknown_option"),
  /**
   * No arguments, a forbidden character, a non-finite number, an unsupported type, or {@code :} in
   * a {@code /q/video} argument.
   */
  INVALID_ARGUMENT("invalid_argument"),
  /** An empty source, or a source with control characters. */
  INVALID_SOURCE("invalid_source"),
  /** An extension that is not alphanumeric. */
  INVALID_EXTENSION("invalid_extension"),
  /** A separator that is not one character, or is forbidden. */
  INVALID_SEPARATOR("invalid_separator"),
  /** A PDF password without a signing key. */
  MISSING_SIGNING("missing_signing"),
  /** An encrypted source or a PDF password without an encryption key. */
  MISSING_ENCRYPTION("missing_encryption"),
  /** Key material that is not hex, a wrong key or IV size, or a signature size outside 1 to 32. */
  INVALID_KEY("invalid_key"),
  /** An empty password, one with control characters, or one over 1024 UTF-8 bytes. */
  INVALID_PASSWORD("invalid_password"),
  /**
   * Chained {@code /info} or {@code /video} pipelines, a PDF password on {@code /video}, a stream
   * option given as an option, an unknown protocol, {@code ts} on DASH, a URL outside the mount, or
   * two {@code xi_sig}.
   */
  INVALID_REQUEST("invalid_request"),
  /** A bad {@code xi://} source name or authority. */
  INVALID_NAME("invalid_name"),
  /** An imaginary endpoint that is not lower-case letters, or is {@code video}. */
  INVALID_ENDPOINT("invalid_endpoint"),
  /** A negative range value, or an end before the start. */
  INVALID_RANGE("invalid_range");

  private final String code;

  ErrorCode(String code) {
    this.code = code;
  }

  /**
   * The code as written in the conformance fixture.
   *
   * @return the code, e.g. {@code invalid_source}
   */
  public String code() {
    return code;
  }

  @Override
  public String toString() {
    return code;
  }
}
