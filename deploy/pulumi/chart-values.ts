// Chart values for deploy/helm/xtandard-image, from values.<fleet>.json (the one place they
// live). Pure, so deploy/check/helm.sh can compare chartValues({ fleet }) with the file and
// render the file through helm template + kubeconform.
import { readFileSync } from "node:fs";
import { join } from "node:path";

export type Fleet = "image" | "video";

export interface ChartOptions {
  fleet: Fleet;
  /** image.flavor: slim, full or gpl. */
  imageFlavor?: string;
  /** image.tag; empty uses <appVersion>-<flavor>. Must end in -<flavor>. */
  imageTag?: string;
  /** XI_SOURCES and its XI_SOURCES_<NAME>_* keys, replacing the file's. */
  sources?: Record<string, string>;
  autoscalingType?: "hpa" | "keda";
  minReplicas?: number;
  maxReplicas?: number;
}

export interface ChartValues {
  role: string;
  image: { flavor: string; tag: string };
  existingSecret: string;
  config: Record<string, string>;
  autoscaling: { enabled: boolean; type: string; minReplicas: number; maxReplicas: number };
  [key: string]: unknown;
}

export function chartValues(o: ChartOptions): ChartValues {
  const base = JSON.parse(readFileSync(join(__dirname, `values.${o.fleet}.json`), "utf8")) as ChartValues;
  const image = { ...base.image, ...(o.imageFlavor ? { flavor: o.imageFlavor } : {}), ...(o.imageTag !== undefined ? { tag: o.imageTag } : {}) };
  if (!["slim", "full", "gpl"].includes(image.flavor)) throw new Error(`imageFlavor ${image.flavor}: slim, full or gpl`);
  // The chart's flavor checks (video needs FFmpeg) read image.flavor; a tag of another flavour would pass them.
  if (image.tag !== "" && !image.tag.endsWith(`-${image.flavor}`)) throw new Error(`imageTag ${image.tag} must end in -${image.flavor}`);
  if (base.role === "video" && image.flavor === "slim") throw new Error("the video fleet needs imageFlavor full or gpl");

  let config = base.config;
  if (o.sources) {
    if (!("XI_SOURCES" in o.sources)) throw new Error("sources must contain XI_SOURCES");
    config = { ...Object.fromEntries(Object.entries(base.config).filter(([k]) => !k.startsWith("XI_SOURCES"))), ...o.sources };
  }

  const autoscaling = {
    ...base.autoscaling,
    ...(o.autoscalingType ? { type: o.autoscalingType } : {}),
    ...(o.minReplicas !== undefined ? { minReplicas: o.minReplicas } : {}),
    ...(o.maxReplicas !== undefined ? { maxReplicas: o.maxReplicas } : {}),
  };
  return { ...base, image, config, autoscaling };
}
