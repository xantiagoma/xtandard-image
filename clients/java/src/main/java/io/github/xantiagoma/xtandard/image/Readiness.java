package io.github.xantiagoma.xtandard.image;

/**
 * The {@code /readyz} answer.
 *
 * @param ready true only for 200 {@code ready}
 * @param state {@code ready}, {@code starting} or {@code draining}
 */
public record Readiness(boolean ready, String state) {}
