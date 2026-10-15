package io.github.xantiagoma.xtandard.image;

import java.util.ArrayList;
import java.util.List;
import java.util.Set;
import org.jspecify.annotations.Nullable;

/**
 * The {@code /video} options and manifest container of a stream request.
 *
 * @param entries the options, then {@code stream}, {@code segment_duration} and {@code
 *     segment_format} as requested
 * @param container {@code m3u8} or {@code mpd}
 */
record StreamParts(List<OptionEntry> entries, String container) {

  private static final Set<String> RESERVED =
      Set.of("stream", "segment_duration", "segment_format", "format");

  static StreamParts of(StreamParams p) {
    List<OptionEntry> entries = new ArrayList<>();
    VideoOptions options = p.options();
    if (options != null) {
      entries.addAll(options.entries());
    }
    for (OptionEntry e : entries) {
      String canonical = Catalogue.VIDEO.names().get(e.name());
      if (canonical != null && RESERVED.contains(canonical)) {
        throw new UrlBuildException(
            ErrorCode.INVALID_REQUEST,
            "stream requests set "
                + e.name()
                + " through the protocol, the renditions and the segment settings");
      }
    }
    StreamProtocol protocol = protocol(p.protocol());
    if (!p.renditions().isEmpty()) {
      List<@Nullable Object> args = new ArrayList<>();
      args.add(protocol.value());
      args.addAll(p.renditions());
      entries.add(new OptionEntry("stream", args));
    }
    Integer duration = p.segmentDuration();
    if (duration != null) {
      entries.add(OptionEntry.of("segment_duration", duration));
    }
    String format = p.segmentFormat();
    if (format != null) {
      SegmentFormat f = segmentFormat(format);
      if (f == null || !f.protocols().contains(protocol.value())) {
        throw new UrlBuildException(
            ErrorCode.INVALID_REQUEST,
            "segment format \"" + format + "\" is not available for " + protocol.value());
      }
      if (f != SegmentFormat.DEFAULT) {
        entries.add(OptionEntry.of("segment_format", f.value()));
      }
    }
    return new StreamParts(entries, protocol.container());
  }

  private static StreamProtocol protocol(String name) {
    for (StreamProtocol p : StreamProtocol.values()) {
      if (p.value().equals(name)) {
        return p;
      }
    }
    throw new UrlBuildException(
        ErrorCode.INVALID_REQUEST, "unknown stream protocol \"" + name + "\"");
  }

  private static @Nullable SegmentFormat segmentFormat(String name) {
    for (SegmentFormat f : SegmentFormat.values()) {
      if (f.value().equals(name)) {
        return f;
      }
    }
    return null;
  }
}
