package io.github.xantiagoma.xtandard.image;

import org.jspecify.annotations.Nullable;

/**
 * Per-URL settings beside the options: the output extension, the source encoding and the password
 * of a protected PDF. Immutable; start from {@link #of()} and use the {@code with} methods.
 *
 * @param extension the output format ({@code @webp} / {@code .webp}) or, for {@code /video}, the
 *     container; null for none
 * @param sourceEncoding overrides the builder's source encoding; null keeps it
 * @param pdfPassword the plain-text password of a protected PDF, encrypted into {@code pdp} (needs
 *     both keys); null for none
 */
public record UrlParams(
    @Nullable String extension,
    @Nullable SourceEncoding sourceEncoding,
    @Nullable String pdfPassword) {

  private static final UrlParams NONE = new UrlParams(null, null, null);

  /**
   * No extension, the builder's source encoding, no PDF password.
   *
   * @return the empty settings
   */
  public static UrlParams of() {
    return NONE;
  }

  /**
   * Settings with only an extension.
   *
   * @param extension the output format or container
   * @return the settings
   */
  public static UrlParams ofExtension(String extension) {
    return new UrlParams(extension, null, null);
  }

  /**
   * These settings with an extension.
   *
   * @param extension the output format or container; null for none
   * @return new settings
   */
  public UrlParams withExtension(@Nullable String extension) {
    return new UrlParams(extension, sourceEncoding, pdfPassword);
  }

  /**
   * These settings with an image format as the extension.
   *
   * @param format the output format
   * @return new settings
   */
  public UrlParams withExtension(ImageFormat format) {
    return withExtension(format.value());
  }

  /**
   * These settings with a video container as the extension.
   *
   * @param container the output container
   * @return new settings
   */
  public UrlParams withExtension(VideoContainer container) {
    return withExtension(container.value());
  }

  /**
   * These settings with a source encoding.
   *
   * @param sourceEncoding the encoding; null keeps the builder's
   * @return new settings
   */
  public UrlParams withSourceEncoding(@Nullable SourceEncoding sourceEncoding) {
    return new UrlParams(extension, sourceEncoding, pdfPassword);
  }

  /**
   * These settings with the password of a protected PDF.
   *
   * @param pdfPassword the plain-text password; null for none
   * @return new settings
   */
  public UrlParams withPdfPassword(@Nullable String pdfPassword) {
    return new UrlParams(extension, sourceEncoding, pdfPassword);
  }

  @Override
  public String toString() {
    return "UrlParams[extension="
        + extension
        + ", sourceEncoding="
        + sourceEncoding
        + ", pdfPassword="
        + (pdfPassword == null ? "null" : "***")
        + "]";
  }
}
