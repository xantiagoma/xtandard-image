package io.github.xantiagoma.xtandard.image;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import org.jspecify.annotations.Nullable;

/**
 * An HLS or DASH manifest request: the protocol, the rendition ladder, the segment settings and
 * other {@code /video} options. Immutable; start from {@link #hls()} or {@link #dash()}.
 *
 * <pre>{@code
 * StreamParams.hls().withRenditions(360, 720).withSegmentDuration(4)
 * }</pre>
 *
 * @param protocol {@code hls} or {@code dash} (another name is an error when the URL is built)
 * @param renditions rungs by the short side of the frame (at most 8); empty for the server's ladder
 * @param segmentDuration segment length in whole seconds (1 to 30); null for the server's default
 * @param segmentFormat {@code fmp4} (CMAF, the default) or {@code ts} (MPEG-TS, HLS only); null for
 *     the default
 * @param options other {@code /video} options (trim, crop, quality, audio, ...) applied to every
 *     rendition; null for none
 * @param sourceEncoding overrides the builder's source encoding (imgproxy dialect only); null keeps
 *     it
 */
public record StreamParams(
    String protocol,
    List<Integer> renditions,
    @Nullable Integer segmentDuration,
    @Nullable String segmentFormat,
    @Nullable VideoOptions options,
    @Nullable SourceEncoding sourceEncoding) {

  /**
   * Copies the renditions.
   *
   * @param protocol the protocol
   * @param renditions the renditions
   * @param segmentDuration the segment duration
   * @param segmentFormat the segment format
   * @param options other video options
   * @param sourceEncoding the source encoding
   */
  public StreamParams {
    renditions = List.copyOf(renditions);
  }

  /**
   * An HLS manifest ({@code @m3u8}).
   *
   * @return the request
   */
  public static StreamParams hls() {
    return of(StreamProtocol.HLS);
  }

  /**
   * A DASH manifest ({@code @mpd}).
   *
   * @return the request
   */
  public static StreamParams dash() {
    return of(StreamProtocol.DASH);
  }

  /**
   * A manifest for a protocol.
   *
   * @param protocol the protocol
   * @return the request
   */
  public static StreamParams of(StreamProtocol protocol) {
    return of(protocol.value());
  }

  /**
   * A manifest for a protocol given by name.
   *
   * @param protocol {@code hls} or {@code dash}
   * @return the request
   */
  public static StreamParams of(String protocol) {
    return new StreamParams(protocol, List.of(), null, null, null, null);
  }

  /**
   * This request with a rendition ladder.
   *
   * @param renditions short sides of the frame, e.g. 360, 720
   * @return a new request
   */
  public StreamParams withRenditions(int... renditions) {
    List<Integer> r = new ArrayList<>(renditions.length);
    for (int h : renditions) {
      r.add(h);
    }
    return withRenditions(r);
  }

  /**
   * This request with a rendition ladder.
   *
   * @param renditions short sides of the frame
   * @return a new request
   */
  public StreamParams withRenditions(List<Integer> renditions) {
    return new StreamParams(
        protocol,
        Collections.unmodifiableList(new ArrayList<>(renditions)),
        segmentDuration,
        segmentFormat,
        options,
        sourceEncoding);
  }

  /**
   * This request with a segment duration.
   *
   * @param seconds whole seconds, 1 to 30; null for the server's default
   * @return a new request
   */
  public StreamParams withSegmentDuration(@Nullable Integer seconds) {
    return new StreamParams(protocol, renditions, seconds, segmentFormat, options, sourceEncoding);
  }

  /**
   * This request with a segment format.
   *
   * @param format the segment format
   * @return a new request
   */
  public StreamParams withSegmentFormat(SegmentFormat format) {
    return withSegmentFormat(format.value());
  }

  /**
   * This request with a segment format given by name.
   *
   * @param format {@code fmp4} or {@code ts}; null for the default
   * @return a new request
   */
  public StreamParams withSegmentFormat(@Nullable String format) {
    return new StreamParams(protocol, renditions, segmentDuration, format, options, sourceEncoding);
  }

  /**
   * This request with other {@code /video} options.
   *
   * @param options the options; null for none
   * @return a new request
   */
  public StreamParams withOptions(@Nullable VideoOptions options) {
    return new StreamParams(
        protocol, renditions, segmentDuration, segmentFormat, options, sourceEncoding);
  }

  /**
   * This request with a source encoding (imgproxy dialect).
   *
   * @param sourceEncoding the encoding; null keeps the builder's
   * @return a new request
   */
  public StreamParams withSourceEncoding(@Nullable SourceEncoding sourceEncoding) {
    return new StreamParams(
        protocol, renditions, segmentDuration, segmentFormat, options, sourceEncoding);
  }
}
