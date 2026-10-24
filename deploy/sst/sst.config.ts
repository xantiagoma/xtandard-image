/// <reference path="./.sst/platform/config.d.ts" />

// xtandard-image on ECS Fargate with SST (docs/DEPLOY.md "SST"). `sst.aws.Service` on a
// `sst.aws.Cluster`: an ALB in front, autoscaling between scaling.min and scaling.max on CPU,
// memory and ALB requests per task. Never deployed by this repository; `bun run typecheck`
// checks it after `sst install` has generated .sst/platform.
//
// Secrets: create two Secrets Manager secrets and export their ARNs before `sst deploy`:
//   XI_SIGNING_KEYS_ARN, XI_SIGNING_SALTS_ARN.
export default $config({
  app(input) {
    return {
      name: "xtandard-image",
      home: "aws",
      removal: input?.stage === "production" ? "retain" : "remove",
      providers: { aws: { region: "us-east-1" } },
    };
  },
  async run() {
    const vpc = new sst.aws.Vpc("Vpc", { nat: "ec2" });
    const cluster = new sst.aws.Cluster("Cluster", { vpc });

    const service = new sst.aws.Service("XtandardImage", {
      cluster,
      image: "ghcr.io/xantiagoma/xtandard-image:0.1.0-slim",
      architecture: "arm64",
      // Workers follow the vCPUs; GOMEMLIMIT follows the memory.
      cpu: "2 vCPU",
      memory: "4 GB",
      scaling: {
        min: 2,
        max: 20,
        cpuUtilization: 60,
        memoryUtilization: 80,
        // ALB requests per task per minute.
        requestCount: 1200,
        scaleOutCooldown: "60 seconds",
        scaleInCooldown: "300 seconds",
      },
      loadBalancer: {
        rules: [{ listen: "80/http", forward: "8080/http" }],
        health: {
          "8080/http": {
            path: "/readyz",
            interval: "10 seconds",
            timeout: "3 seconds",
            healthyThreshold: 2,
            unhealthyThreshold: 3,
          },
        },
      },
      health: {
        command: ["CMD", "/usr/local/bin/xtandard-image", "health"],
        interval: "10 seconds",
        timeout: "3 seconds",
        retries: 3,
        startPeriod: "10 seconds",
      },
      environment: {
        XI_SOURCES: "web",
        XI_SOURCES_WEB_TYPE: "http",
        XI_SOURCES_WEB_ALLOW: "images.example.com",
        XI_CACHE_RESULT_STORES: "memory",
      },
      ssm: {
        XI_SIGNING_KEYS:
          process.env.XI_SIGNING_KEYS_ARN ?? "arn:aws:secretsmanager:us-east-1:123456789012:secret:xi-signing-keys",
        XI_SIGNING_SALTS:
          process.env.XI_SIGNING_SALTS_ARN ?? "arn:aws:secretsmanager:us-east-1:123456789012:secret:xi-signing-salts",
      },
      transform: {
        // SIGTERM → /readyz 503 → 5 s → drain up to 30 s; read-only rootfs with /tmp on a volume.
        taskDefinition: (args) => {
          args.volumes = [{ name: "tmp" }];
          args.containerDefinitions = $jsonStringify(
            $resolve([args.containerDefinitions]).apply(([defs]) =>
              (JSON.parse(defs as string) as Record<string, unknown>[]).map((c) => ({
                ...c,
                readonlyRootFilesystem: true,
                user: "65532:65532",
                stopTimeout: 40,
                mountPoints: [{ sourceVolume: "tmp", containerPath: "/tmp", readOnly: false }],
              })),
            ),
          );
        },
        target: (args) => {
          args.deregistrationDelay = 30;
        },
      },
    });

    return { url: service.url };
  },
});
