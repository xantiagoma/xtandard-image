// Kubernetes through the repository's Helm chart (deploy/helm/xtandard-image, WP-148). The chart
// values come from values.<fleet>.json through chartValues() (chart-values.ts); config keys:
//   fleet (image|video), imageFlavor, imageTag, sources (object), autoscalingType,
//   minReplicas, maxReplicas; secrets signingKeys, signingSalts, streamKeys.
import * as k8s from "@pulumi/kubernetes";
import * as pulumi from "@pulumi/pulumi";
import { chartValues, type Fleet } from "./chart-values";
import type { Settings } from "./settings";

export function deployK8s(s: Settings): pulumi.Output<string> {
  const config = new pulumi.Config();
  const values = chartValues({
    fleet: (config.get("fleet") ?? "image") as Fleet,
    imageFlavor: config.get("imageFlavor"),
    imageTag: config.get("imageTag"),
    sources: config.getObject<Record<string, string>>("sources"),
    autoscalingType: config.get("autoscalingType") as "hpa" | "keda" | undefined,
    minReplicas: config.getNumber("minReplicas"),
    maxReplicas: config.getNumber("maxReplicas"),
  });

  const ns = new k8s.core.v1.Namespace("xtandard-image", { metadata: { name: "xtandard-image" } });

  // The chart loads secrets only from existingSecret: this is that Secret.
  const secret = new k8s.core.v1.Secret("xi-signing", {
    metadata: { name: values.existingSecret, namespace: ns.metadata.name },
    stringData: {
      XI_SIGNING_KEYS: s.signingKeys,
      XI_SIGNING_SALTS: s.signingSalts,
      XI_VIDEO_TRANSFORM_STREAM_KEYS: config.getSecret("streamKeys") ?? s.signingKeys,
    },
  });

  const release = new k8s.helm.v3.Release(
    "xtandard-image",
    { chart: "../helm/xtandard-image", namespace: ns.metadata.name, atomic: true, values },
    { dependsOn: [secret] },
  );

  return release.status.status;
}
