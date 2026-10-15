package io.github.xantiagoma.xtandard.image;

/**
 * A byte range for the {@code Range} header of media requests ({@code /video} results and stream
 * parts answer 206 with {@code Content-Range}).
 *
 * <pre>{@code
 * ByteRange.from(0)        // bytes=0-
 * ByteRange.of(0, 1023)    // bytes=0-1023
 * ByteRange.suffix(500)    // bytes=-500
 * ByteRange.raw("bytes=1-2,5-6")
 * }</pre>
 */
public sealed interface ByteRange
    permits ByteRange.From, ByteRange.Between, ByteRange.Suffix, ByteRange.Raw {

  /**
   * The {@code Range} header value.
   *
   * @return e.g. {@code bytes=0-1023}
   */
  String header();

  /**
   * Bytes from {@code start} to the end.
   *
   * @param start the first byte, at least 0
   * @return the range
   * @throws UrlBuildException {@link ErrorCode#INVALID_RANGE} for a negative start
   */
  static ByteRange from(long start) {
    return new From(start);
  }

  /**
   * Bytes {@code start} to {@code end}, inclusive.
   *
   * @param start the first byte, at least 0
   * @param end the last byte, at least {@code start}
   * @return the range
   * @throws UrlBuildException {@link ErrorCode#INVALID_RANGE} for a negative value or an end before
   *     the start
   */
  static ByteRange of(long start, long end) {
    return new Between(start, end);
  }

  /**
   * The last {@code length} bytes.
   *
   * @param length the number of bytes, at least 0
   * @return the range
   * @throws UrlBuildException {@link ErrorCode#INVALID_RANGE} for a negative length
   */
  static ByteRange suffix(long length) {
    return new Suffix(length);
  }

  /**
   * A header value written verbatim.
   *
   * @param header the value, e.g. {@code bytes=1-2}
   * @return the range
   */
  static ByteRange raw(String header) {
    return new Raw(header);
  }

  /**
   * Bytes from {@code start} to the end.
   *
   * @param start the first byte
   */
  record From(long start) implements ByteRange {
    /**
     * Checks the start.
     *
     * @param start the first byte
     */
    public From {
      nonNegative(start, "start");
    }

    @Override
    public String header() {
      return "bytes=" + start + "-";
    }
  }

  /**
   * Bytes {@code start} to {@code end}, inclusive.
   *
   * @param start the first byte
   * @param end the last byte
   */
  record Between(long start, long end) implements ByteRange {
    /**
     * Checks the bounds.
     *
     * @param start the first byte
     * @param end the last byte
     */
    public Between {
      nonNegative(start, "start");
      nonNegative(end, "end");
      if (end < start) {
        throw new UrlBuildException(
            ErrorCode.INVALID_RANGE, "range end " + end + " is before start " + start);
      }
    }

    @Override
    public String header() {
      return "bytes=" + start + "-" + end;
    }
  }

  /**
   * The last {@code length} bytes.
   *
   * @param length the number of bytes
   */
  record Suffix(long length) implements ByteRange {
    /**
     * Checks the length.
     *
     * @param length the number of bytes
     */
    public Suffix {
      nonNegative(length, "suffix");
    }

    @Override
    public String header() {
      return "bytes=-" + length;
    }
  }

  /**
   * A header value written verbatim.
   *
   * @param header the value
   */
  record Raw(String header) implements ByteRange {}

  private static void nonNegative(long n, String what) {
    if (n < 0) {
      throw new UrlBuildException(
          ErrorCode.INVALID_RANGE, "range " + what + " " + n + " is not a non-negative integer");
    }
  }
}
