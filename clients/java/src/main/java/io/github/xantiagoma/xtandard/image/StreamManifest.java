package io.github.xantiagoma.xtandard.image;

/**
 * A fetched HLS playlist or DASH MPD.
 *
 * @param url the (signed) manifest URL; hand it to a player
 * @param contentType {@code application/vnd.apple.mpegurl} or {@code application/dash+xml}
 * @param body the manifest; its child URLs ({@code /_stream/<ticket>/...}) carry a ticket and need
 *     no signing (fetch them with {@link XtandardClient#media(String)})
 */
public record StreamManifest(String url, String contentType, String body) {}
