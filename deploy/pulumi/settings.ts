import type * as pulumi from "@pulumi/pulumi";

/** Settings shared by every target. */
export interface Settings {
  image: string;
  allowedHosts: string;
  minInstances: number;
  maxInstances: number;
  signingKeys: pulumi.Output<string>;
  signingSalts: pulumi.Output<string>;
}

/** XI_* variables every target sets (docs/CONFIG.md). */
export function baseEnv(s: Settings): Record<string, string> {
  return {
    XI_SOURCES: "web",
    XI_SOURCES_WEB_TYPE: "http",
    XI_SOURCES_WEB_ALLOW: s.allowedHosts,
    XI_CACHE_RESULT_STORES: "memory",
  };
}
