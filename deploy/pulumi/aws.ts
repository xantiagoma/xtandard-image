// ECS on Fargate behind an ALB with target-tracking autoscaling (awsx + aws).
import * as aws from "@pulumi/aws";
import * as awsx from "@pulumi/awsx";
import * as pulumi from "@pulumi/pulumi";
import { baseEnv, type Settings } from "./settings";

export function deployAws(s: Settings): pulumi.Output<string> {
  const keys = new aws.secretsmanager.Secret("xi-signing-keys");
  new aws.secretsmanager.SecretVersion("xi-signing-keys", { secretId: keys.id, secretString: s.signingKeys });
  const salts = new aws.secretsmanager.Secret("xi-signing-salts");
  new aws.secretsmanager.SecretVersion("xi-signing-salts", { secretId: salts.id, secretString: s.signingSalts });

  const cluster = new aws.ecs.Cluster("xtandard-image");

  const lb = new awsx.lb.ApplicationLoadBalancer("xtandard-image", {
    idleTimeout: 60,
    defaultTargetGroup: {
      port: 8080,
      protocol: "HTTP",
      targetType: "ip",
      deregistrationDelay: 30,
      healthCheck: { path: "/readyz", matcher: "200", interval: 10, timeout: 3 },
    },
  });

  const service = new awsx.ecs.FargateService("xtandard-image", {
    cluster: cluster.arn,
    desiredCount: s.minInstances,
    assignPublicIp: true,
    deploymentCircuitBreaker: { enable: true, rollback: true },
    healthCheckGracePeriodSeconds: 15,
    taskDefinitionArgs: {
      runtimePlatform: { cpuArchitecture: "ARM64", operatingSystemFamily: "LINUX" },
      volumes: [{ name: "tmp" }],
      container: {
        name: "xtandard-image",
        image: s.image,
        // Workers follow the vCPUs (2048 = 2), GOMEMLIMIT the memory.
        cpu: 2048,
        memory: 4096,
        essential: true,
        readonlyRootFilesystem: true,
        user: "65532:65532",
        stopTimeout: 40,
        portMappings: [{ containerPort: 8080, targetGroup: lb.defaultTargetGroup }],
        mountPoints: [{ sourceVolume: "tmp", containerPath: "/tmp", readOnly: false }],
        environment: Object.entries(baseEnv(s)).map(([name, value]) => ({ name, value })),
        secrets: [
          { name: "XI_SIGNING_KEYS", valueFrom: keys.arn },
          { name: "XI_SIGNING_SALTS", valueFrom: salts.arn },
        ],
        healthCheck: {
          command: ["CMD", "/usr/local/bin/xtandard-image", "health"],
          interval: 10,
          timeout: 3,
          retries: 3,
          startPeriod: 10,
        },
      },
    },
  });

  const target = new aws.appautoscaling.Target("xtandard-image", {
    serviceNamespace: "ecs",
    scalableDimension: "ecs:service:DesiredCount",
    resourceId: pulumi.interpolate`service/${cluster.name}/${service.service.name}`,
    minCapacity: s.minInstances,
    maxCapacity: s.maxInstances,
  });

  new aws.appautoscaling.Policy("xtandard-image-cpu", {
    policyType: "TargetTrackingScaling",
    serviceNamespace: target.serviceNamespace,
    resourceId: target.resourceId,
    scalableDimension: target.scalableDimension,
    targetTrackingScalingPolicyConfiguration: {
      targetValue: 60,
      scaleOutCooldown: 60,
      scaleInCooldown: 300,
      predefinedMetricSpecification: { predefinedMetricType: "ECSServiceAverageCPUUtilization" },
    },
  });

  return lb.loadBalancer.dnsName;
}
