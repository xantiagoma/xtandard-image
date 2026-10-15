/**
 * The official Java client of xtandard-image (ADR 0013).
 *
 * <ul>
 *   <li>{@link io.github.xantiagoma.xtandard.image.ImgproxyUrlBuilder}: processing, {@code /info},
 *       {@code /video}, HLS and DASH URLs in the imgproxy dialect;
 *   <li>{@link io.github.xantiagoma.xtandard.image.QueryUrlBuilder}: the query dialect ({@code
 *       /q/}, {@code /q/video}, imaginary endpoints);
 *   <li>{@link io.github.xantiagoma.xtandard.image.Signing}, {@link
 *       io.github.xantiagoma.xtandard.image.Encryption} and {@link
 *       io.github.xantiagoma.xtandard.image.Sources}: the helpers the builders use;
 *   <li>{@link io.github.xantiagoma.xtandard.image.XtandardClient} and {@link
 *       io.github.xantiagoma.xtandard.image.XtandardAsyncClient}: HTTP clients.
 * </ul>
 *
 * <p>Build and sign URLs on a server: the signing key signs every URL, and the encryption key
 * decrypts every {@code enc/} source and PDF password.
 *
 * <p>Everything is non-null unless annotated {@code @Nullable} (JSpecify {@code @NullMarked}).
 */
@NullMarked
package io.github.xantiagoma.xtandard.image;

import org.jspecify.annotations.NullMarked;
