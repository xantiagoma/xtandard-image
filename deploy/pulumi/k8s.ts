// Kubernetes through the repository's Helm chart (deploy/helm/xtandard-image, WP-148).
import * as k8s from "@pulumi/kubernetes";
import * as pulumi from "@pulumi/pulumi";
import type { Settings } from "./settings";

export function deployK8s(s: Settings): pulumi.Output<string> {
  const config = new pulumi.Config();
  const ns = new k8s.core.v1.Namespace("xtandard-image", { metadata: { name: "xtandard-image" } });

  new k8s.core.v1.Secret("xi-signing", {
    metadata: { name: "xi-signing", namespace: ns.metadata.name },
    stringData: { XI_SIGNING_KEYS: s.signingKeys, XI_SIGNING_SALTS: s.signingSalts },
  });

  // Chart values (names from the WP-148 chart; the only place this program sets them).
  const values = {
    role: config.get("role") ?? "all",
    image: { tag: s.image.split(":").pop() ?? "0.1.0-slim" },
    autoscaling: { enabled: true, minReplicas: s.minInstances, maxReplicas: s.maxInstances },
  };

  const release = new k8s.helm.v3.Release("xtandard-image", {
    chart: "../helm/xtandard-image",
    namespace: ns.metadata.name,
    atomic: true,
    values,
  });

  return release.status.status;
}
