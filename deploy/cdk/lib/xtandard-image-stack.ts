// xtandard-image on ECS Fargate behind an ALB with autoscaling (docs/DEPLOY.md "AWS CDK").
import { Duration, Stack, type StackProps } from "aws-cdk-lib";
import * as ec2 from "aws-cdk-lib/aws-ec2";
import * as ecs from "aws-cdk-lib/aws-ecs";
import * as ecsPatterns from "aws-cdk-lib/aws-ecs-patterns";
import * as logs from "aws-cdk-lib/aws-logs";
import * as secretsmanager from "aws-cdk-lib/aws-secretsmanager";
import type { Construct } from "constructs";

export interface XtandardImageStackProps extends StackProps {
  /** Image reference, <version>-slim|-full|-gpl. */
  image?: string;
  /** XI_SOURCES_WEB_ALLOW: host globs of the http source. */
  allowedHosts?: string;
  minTasks?: number;
  maxTasks?: number;
}

export class XtandardImageStack extends Stack {
  constructor(scope: Construct, id: string, props: XtandardImageStackProps = {}) {
    super(scope, id, props);

    const vpc = new ec2.Vpc(this, "Vpc", { maxAzs: 2, natGateways: 1 });
    const cluster = new ecs.Cluster(this, "Cluster", { vpc, containerInsightsV2: ecs.ContainerInsights.ENABLED });

    // Values are set out of band (aws secretsmanager put-secret-value), never in the template.
    const signingKeys = new secretsmanager.Secret(this, "SigningKeys", { description: "XI_SIGNING_KEYS" });
    const signingSalts = new secretsmanager.Secret(this, "SigningSalts", { description: "XI_SIGNING_SALTS" });

    // Workers follow the vCPUs (2048 = 2); GOMEMLIMIT follows the memory.
    const taskDefinition = new ecs.FargateTaskDefinition(this, "Task", {
      cpu: 2048,
      memoryLimitMiB: 4096,
      runtimePlatform: {
        cpuArchitecture: ecs.CpuArchitecture.ARM64,
        operatingSystemFamily: ecs.OperatingSystemFamily.LINUX,
      },
      volumes: [{ name: "tmp" }],
    });

    const container = taskDefinition.addContainer("xtandard-image", {
      image: ecs.ContainerImage.fromRegistry(props.image ?? "ghcr.io/xantiagoma/xtandard-image:0.1.1-slim"),
      readonlyRootFilesystem: true,
      user: "65532:65532",
      // SIGTERM → /readyz 503 → 5 s → drain up to 30 s.
      stopTimeout: Duration.seconds(40),
      portMappings: [{ containerPort: 8080, name: "http" }],
      environment: {
        XI_SOURCES: "web",
        XI_SOURCES_WEB_TYPE: "http",
        XI_SOURCES_WEB_ALLOW: props.allowedHosts ?? "images.example.com",
        XI_CACHE_RESULT_STORES: "memory",
      },
      secrets: {
        XI_SIGNING_KEYS: ecs.Secret.fromSecretsManager(signingKeys),
        XI_SIGNING_SALTS: ecs.Secret.fromSecretsManager(signingSalts),
      },
      healthCheck: {
        command: ["CMD", "/usr/local/bin/xtandard-image", "health"],
        interval: Duration.seconds(10),
        timeout: Duration.seconds(3),
        retries: 3,
        startPeriod: Duration.seconds(10),
      },
      logging: ecs.LogDrivers.awsLogs({ streamPrefix: "xi", logRetention: logs.RetentionDays.ONE_MONTH }),
    });
    container.addMountPoints({ sourceVolume: "tmp", containerPath: "/tmp", readOnly: false });

    const minTasks = props.minTasks ?? 2;
    const service = new ecsPatterns.ApplicationLoadBalancedFargateService(this, "Service", {
      cluster,
      taskDefinition,
      desiredCount: minTasks,
      publicLoadBalancer: true,
      idleTimeout: Duration.seconds(60),
      healthCheckGracePeriod: Duration.seconds(15),
      circuitBreaker: { rollback: true },
      minHealthyPercent: 100,
      maxHealthyPercent: 200,
    });

    service.targetGroup.configureHealthCheck({
      path: "/readyz",
      healthyHttpCodes: "200",
      interval: Duration.seconds(10),
      timeout: Duration.seconds(3),
    });
    service.targetGroup.setAttribute("deregistration_delay.timeout_seconds", "30");

    const scaling = service.service.autoScaleTaskCount({ minCapacity: minTasks, maxCapacity: props.maxTasks ?? 20 });
    scaling.scaleOnCpuUtilization("Cpu", {
      targetUtilizationPercent: 60,
      scaleOutCooldown: Duration.seconds(60),
      scaleInCooldown: Duration.seconds(300),
    });
    scaling.scaleOnRequestCount("Requests", {
      requestsPerTarget: 1200,
      targetGroup: service.targetGroup,
      scaleOutCooldown: Duration.seconds(60),
      scaleInCooldown: Duration.seconds(300),
    });
  }
}
