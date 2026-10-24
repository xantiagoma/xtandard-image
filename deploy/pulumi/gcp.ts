// Google Cloud Run (v2 API) with min/max instances and request concurrency.
import * as gcp from "@pulumi/gcp";
import type * as pulumi from "@pulumi/pulumi";
import { baseEnv, type Settings } from "./settings";

export function deployGcp(s: Settings): pulumi.Output<string> {
  const sa = new gcp.serviceaccount.Account("xtandard-image", { accountId: "xtandard-image" });

  const secret = (id: string, value: pulumi.Output<string>): gcp.secretmanager.Secret => {
    const sec = new gcp.secretmanager.Secret(id, { secretId: id, replication: { auto: {} } });
    new gcp.secretmanager.SecretVersion(id, { secret: sec.id, secretData: value });
    new gcp.secretmanager.SecretIamMember(id, {
      secretId: sec.id,
      role: "roles/secretmanager.secretAccessor",
      member: sa.email.apply((e) => `serviceAccount:${e}`),
    });
    return sec;
  };
  const keys = secret("xi-signing-keys", s.signingKeys);
  const salts = secret("xi-signing-salts", s.signingSalts);

  const service = new gcp.cloudrunv2.Service("xtandard-image", {
    location: "us-central1",
    ingress: "INGRESS_TRAFFIC_ALL",
    deletionProtection: false,
    template: {
      serviceAccount: sa.email,
      executionEnvironment: "EXECUTION_ENVIRONMENT_GEN2",
      timeout: "30s",
      maxInstanceRequestConcurrency: 16,
      scaling: { minInstanceCount: s.minInstances, maxInstanceCount: s.maxInstances },
      containers: [
        {
          // Cloud Run pulls from Artifact Registry: mirror the GHCR image there.
          image: s.image,
          ports: { name: "http1", containerPort: 8080 },
          resources: { limits: { cpu: "2", memory: "2Gi" }, cpuIdle: false, startupCpuBoost: true },
          envs: [
            { name: "XI_SIGNING_KEYS", valueSource: { secretKeyRef: { secret: keys.secretId, version: "latest" } } },
            { name: "XI_SIGNING_SALTS", valueSource: { secretKeyRef: { secret: salts.secretId, version: "latest" } } },
            ...Object.entries({
              ...baseEnv(s),
              XI_SERVER_SHUTDOWN_DELAY: "1s",
              XI_SERVER_SHUTDOWN_TIMEOUT: "8s",
            }).map(([name, value]) => ({ name, value })),
          ],
          startupProbe: { periodSeconds: 1, timeoutSeconds: 1, failureThreshold: 30, httpGet: { path: "/readyz", port: 8080 } },
          livenessProbe: { periodSeconds: 10, timeoutSeconds: 3, httpGet: { path: "/healthz", port: 8080 } },
          volumeMounts: [{ name: "tmp", mountPath: "/tmp" }],
        },
      ],
      volumes: [{ name: "tmp", emptyDir: { medium: "MEMORY", sizeLimit: "64Mi" } }],
    },
  });

  new gcp.cloudrunv2.ServiceIamMember("public", {
    name: service.name,
    location: service.location,
    role: "roles/run.invoker",
    member: "allUsers",
  });

  return service.uri;
}
