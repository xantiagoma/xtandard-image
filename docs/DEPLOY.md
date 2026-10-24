# Deploying xtandard-image

Ready-to-use files for every common platform and infrastructure-as-code tool, each with autoscaling
set up (minimum and maximum instances, the scaling signal and its target, cooldowns). The files live
in [`deploy/`](../deploy/). Sizing, the theory behind the scaling signals, CDN recipes and
observability are in [OPERATIONS.md](OPERATIONS.md); Kubernetes (Kustomize, the Helm chart, HPA and
KEDA) is in `deploy/kubernetes` and `deploy/helm` and is documented there too. Every variable used
below is in [CONFIG.md](CONFIG.md).

Nothing here has been deployed by the project: each file was validated offline by its tool (and the
Docker ones were run locally), as the [matrix](#platform-matrix) says. The images are a private GHCR
package for now, so every platform needs registry credentials (a GitHub token with `read:packages`)
or a copy of the image in its own registry.

## Contents

- [What every deployment needs](#what-every-deployment-needs)
- [Image and video fleets](#image-and-video-fleets)
- [Platform matrix](#platform-matrix)
- Containers: [docker run](#docker-run) · [Docker Compose](#docker-compose) · [Docker Swarm](#docker-swarm) · [Nomad](#nomad) · [Kubernetes](#kubernetes)
- Self-hosted PaaS: [Dokploy](#dokploy) · [Coolify](#coolify) · [Openship](#openship)
- Managed: [Fly.io](#flyio) · [Google Cloud Run](#google-cloud-run) · [AWS ECS on Fargate](#aws-ecs-on-fargate) · [AWS App Runner](#aws-app-runner) · [Azure Container Apps](#azure-container-apps) · [Railway](#railway) · [Render](#render) · [DigitalOcean App Platform](#digitalocean-app-platform)
- Infrastructure as code: [Terraform / OpenTofu](#terraform--opentofu) · [Pulumi](#pulumi) · [SST](#sst) · [AWS CDK](#aws-cdk)
- [Verification](#verification) · [Licences](#licences)

## What every deployment needs

| Concern | What to do |
|---|---|
| Image | `ghcr.io/xantiagoma/xtandard-image:<version>-slim` for stills; `-full` for video, detection and LGPL FFmpeg; `-gpl` for H.264/HEVC output (GPL, read the README's licence note first). Pin a version, never `latest`. Multi-arch (amd64, arm64). |
| Secrets | `XI_SIGNING_KEYS` and `XI_SIGNING_SALTS` (hex, `openssl rand -hex 32` each; comma-separated lists rotate) from the platform's secret store, never in the file. Any variable also takes a `_FILE` form for mounted secrets. A video fleet also needs `XI_VIDEO_TRANSFORM_STREAM_KEYS`, the same on every instance, or the HLS/DASH child URLs of one instance fail on another. |
| Sources | The allowlist of origins: `XI_SOURCES=web`, `XI_SOURCES_WEB_TYPE=http`, `XI_SOURCES_WEB_ALLOW=<hosts>` in these files; S3, GCS, Azure and the rest in [SOURCES.md](SOURCES.md). On a cloud, prefer the platform identity (task role, service account, managed identity) over keys. |
| Filesystem | Read-only root filesystem and a writable `/tmp` (64 MiB is enough for images: fontconfig cache only). [RELEASE.md "Running read-only"](RELEASE.md#running-read-only). |
| Health | Liveness `/healthz` (no libvips call), readiness and load-balancer health `/readyz` (503 while starting or draining). The image's `HEALTHCHECK` runs `xtandard-image health` (GET `/healthz`; no curl in the image). |
| Shutdown | SIGTERM → `/readyz` 503 → `XI_SERVER_SHUTDOWN_DELAY` (5 s) for load balancers to notice → drain for up to `XI_SERVER_SHUTDOWN_TIMEOUT` (30 s). Give the platform a grace period above the sum (40 s in these files); on platforms with a shorter fixed grace (Cloud Run: 10 s) shorten both. |
| CPU and memory | Always set limits. Workers follow the CPU limit (`GOMAXPROCS`, `XI_PROCESSING_WORKERS` defaults to it) and `GOMEMLIMIT` is 30 % of the memory limit. 2 vCPU / 2–4 GiB is a good image instance; video wants 4 vCPU / 8 GiB or more. [OPERATIONS.md](OPERATIONS.md) has the sizing. |
| Admin listener | `/metrics`, pprof and the docs are on `XI_ADMIN_ADDR` (default `127.0.0.1:9090`, loopback only). Set `:9090` only where the port stays on a private network (compose network, Fly private network, an internal port); never route it publicly. |
| CDN | Put one in front: results carry long `Cache-Control` and an `ETag`, and the cache key must include `Accept` when the format follows it. Recipes in [OPERATIONS.md](OPERATIONS.md). |

### Picking the scaling signal

The server bounds its own work: at most `XI_PROCESSING_WORKERS` images at once (one per vCPU),
`XI_PROCESSING_QUEUE_SIZE` waiting (workers × 16), then 429 with `Retry-After`. A platform should
add instances **before** the queue fills:

- **Requests in flight per instance** (Cloud Run `containerConcurrency`, Fly `soft_limit`, Container
  Apps `concurrentRequests`, App Runner `MaxConcurrency`) is the best signal the platforms offer:
  about 2–4 × the vCPU count for images (8–16 for 2 vCPU), 2–4 for video.
- **CPU utilization** (ECS, Render, DigitalOcean, Container Apps custom rule) works because busy
  workers are busy CPUs: target 60–70 %.
- **The server's own metrics** (`xi_workers_busy`, `xi_queue_depth`, `xi_overload_total`) are the
  most precise and need a metrics pipeline: KEDA or a Prometheus adapter on Kubernetes
  ([OPERATIONS.md](OPERATIONS.md)), the Nomad Autoscaler.
- Scale in slowly (5 minute cooldowns or stabilization windows here) and out quickly (1 minute).

## Image and video fleets

Video transformations (`XI_VIDEO_TRANSFORM_ENABLED`, full and gpl images) take minutes of CPU and
gigabytes of spool space, so run them on their own instances: `XI_ROLE=image` instances answer 404
to video, `XI_ROLE=video` instances answer 404 to everything but video (plus health and admin), and
the router sends these paths to the video fleet:

| Path | What |
|---|---|
| `/video/…` | imgproxy dialect video transformations and their HLS/DASH manifests ([URL-DIALECTS §3.8](URL-DIALECTS.md#38-video-transformations-wp-122-wp-123)) |
| `/q/video` | query dialect video transformations (GET and QUERY) |
| `/_stream/…` | child URLs of manifests: playlists, init and media segments (under `XI_SERVER_PATH_PREFIX`) |
| `/<cloud>/video/upload/…`, `/<cloud>/video/fetch/…` | Cloudinary video, if that dialect is mounted (it also serves stills of a video; the video fleet answers those too) |
| `/cdn-cgi/media/…` | Cloudflare Media Transformations, if mounted |

A video instance also needs a **disk-backed** spool (`XI_VIDEO_TRANSFORM_SPOOL_DIR`, writable by UID
65532, with `XI_VIDEO_TRANSFORM_SPOOL_MAX_BYTES` below its size): a tmpfs is RAM, which is what the
spool saves. Video results belong in the disk or S3 result cache tier, not memory (ARCHITECTURE
§11.1). Its drain is longer: `XI_VIDEO_TRANSFORM_TIMEOUT` is 2 minutes, so the files here give the
video fleet `XI_SERVER_SHUTDOWN_TIMEOUT=130s` and a 150 s grace period where the platform allows it.

[`deploy/compose/compose.split.yml`](../deploy/compose/compose.split.yml) with
[`nginx.conf`](../deploy/compose/nginx.conf) is the reference router; the same table goes into an
ALB listener rule, a GCP URL map, a DigitalOcean ingress rule or a CDN origin rule. With one fleet
(`XI_ROLE=all`, the default) nothing needs routing.

## Platform matrix

"Run locally" means started and exercised with `xtandard-image:main-gpl` on Docker; "validated"
means the file passed its tool or schema offline and the server accepted its environment
(`xtandard-image config validate`), see [Verification](#verification); "not verified on the
platform" means nobody deployed it.

| Platform | File | Scales on | Min / max knobs | Scale to zero | Disk for the video spool | Admin / metrics | Verified |
|---|---|---|---|---|---|---|---|
| docker run | [`deploy/docker/run.sh`](../deploy/docker/run.sh) | — (one container) | — | no | `-v` volume | loopback in the container | shellcheck; same flags run locally by the compose run |
| Docker Compose | [`deploy/compose/`](../deploy/compose/) | manual `--scale` | `--scale svc=N` | no | per-replica anonymous volume | `:9090` on the compose network, not published | **run locally** (single, split + nginx routing) |
| Docker Swarm | [`deploy/swarm/stack.yml`](../deploy/swarm/stack.yml) | manual (no autoscaler) | `replicas`, `docker service scale` | no | volume per node | overlay network `xi` | **run locally** (throwaway swarm: 2/2 replicas, update and rollback config) |
| Nomad | [`deploy/nomad/`](../deploy/nomad/xtandard-image.nomad.hcl) | Nomad Autoscaler on `xi_workers_busy` | `scaling { min max }` | no | host volume / CSI | `admin` port, service `xtandard-image-metrics` | validated (`nomad job validate`) |
| Kubernetes | `deploy/kubernetes`, `deploy/helm` (WP-148) | HPA (CPU) or KEDA (server metrics) | chart values | with KEDA | `emptyDir` without `medium` | ClusterIP / pod annotations | see [OPERATIONS.md](OPERATIONS.md) |
| Dokploy | [`deploy/dokploy/compose.yml`](../deploy/dokploy/compose.yml) | manual (Swarm replicas) | `deploy.replicas` / Cluster Settings | no | volume | `dokploy-network` | validated (`docker stack config`, `config validate`); not verified on the platform |
| Coolify | [`deploy/coolify/compose.yml`](../deploy/coolify/compose.yml) | none (one container per service) | — | no | named volume | compose network | validated (`docker compose config`, `config validate`); not verified on the platform |
| Openship | [`deploy/openship/docker-compose.yml`](../deploy/openship/docker-compose.yml) | unknown (see below) | unknown | unknown | volume | compose network | validated (`docker compose config`); **not verified**: platform behaviour undocumented |
| Fly.io | [`deploy/fly/fly.toml`](../deploy/fly/fly.toml), [`fly.video.toml`](../deploy/fly/fly.video.toml) | requests per Machine (`soft_limit`) via autostop/autostart | `min_machines_running`; max = Machines created (`fly scale count`) | yes (`min_machines_running = 0`) | Fly volume (one per Machine) | `[[metrics]]` on the private network | validated (SchemaStore `fly.json`, `config validate`); not verified on the platform |
| Google Cloud Run | [`deploy/cloud-run/`](../deploy/cloud-run/) | concurrent requests and CPU | `minScale`, `maxScale` annotations | yes (`minScale: "0"`) | none (in-memory volume counts against memory) | loopback: a sidecar scrapes it | validated (kubeconform Knative v1, `config validate`); not verified on the platform |
| AWS ECS on Fargate | [`deploy/aws-ecs/`](../deploy/aws-ecs/) | target tracking: CPU and/or ALB requests per target | scalable target `MinCapacity`, `MaxCapacity` | no | task ephemeral storage (21–200 GiB) | loopback, or a sidecar | validated (botocore models, `config validate`); not verified on the platform |
| AWS App Runner | [`deploy/aws-app-runner/`](../deploy/aws-app-runner/) | concurrent requests (`MaxConcurrency`) | `MinSize` (≥ 1), `MaxSize` | no | none (3 GB ephemeral) | not reachable | validated (botocore); closed to new customers |
| Azure Container Apps | [`deploy/azure-container-apps/containerapp.yaml`](../deploy/azure-container-apps/containerapp.yaml) | KEDA http (concurrent requests) + cpu rule | `minReplicas`, `maxReplicas` (≤ 1000) | yes (min 0, http rule only) | `EmptyDir` (replica disk) | loopback, or `additionalPortMappings` (internal) | validated (Azure REST spec 2026-01-01, `config validate`); not verified on the platform |
| Railway | [`deploy/railway/railway.json`](../deploy/railway/railway.json) | none horizontally (vertical up to plan limits) | `numReplicas` | app sleeping (`sleepApplication`) | one volume, not with replicas | private network `*.railway.internal` | validated (`railway.schema.json`); not verified on the platform |
| Render | [`deploy/render/render.yaml`](../deploy/render/render.yaml) | CPU and memory targets | `scaling.minInstances` (≥ 1), `maxInstances` | no | a disk blocks scaling | private network | validated (`render.yaml.json`, `config validate`); not verified on the platform |
| DigitalOcean App Platform | [`deploy/digitalocean/app.yaml`](../deploy/digitalocean/app.yaml) | CPU (dedicated sizes), requests per second, p95 latency | `autoscaling.min/max_instance_count` | no | none | `internal_ports` | validated (DigitalOcean OpenAPI `app_spec`, `config validate`); not verified on the platform |
| Terraform / OpenTofu | [`deploy/terraform/`](../deploy/terraform/) | as the platform | variables `min_*`, `max_*` | as the platform | — | — | validated (`tofu`/`terraform validate`, tflint) |
| Pulumi | [`deploy/pulumi/`](../deploy/pulumi/) | ECS CPU target tracking; Cloud Run; Helm | config `minInstances`, `maxInstances` | — | — | — | validated (`tsc --noEmit`) |
| SST | [`deploy/sst/`](../deploy/sst/) | CPU, memory, ALB requests | `scaling.min`, `scaling.max` | no | — | — | validated (`sst install` + `tsc --noEmit`) |
| AWS CDK | [`deploy/cdk/`](../deploy/cdk/) | CPU and ALB requests | `autoScaleTaskCount` | no | — | — | validated (`tsc --noEmit`, `cdk synth` without credentials) |

## Containers

### docker run

[`deploy/docker/run.sh`](../deploy/docker/run.sh) starts one container with the production flags:
`--read-only`, a 64 MiB `/tmp` tmpfs, `--cap-drop ALL`, `no-new-privileges`, `--cpus 2 --memory 2g`
(workers and `GOMEMLIMIT` follow them), `--stop-timeout 40`, the signing keys from your environment
and the http source. One container does not scale; use Compose or Swarm for replicas.

### Docker Compose

- [`compose.yml`](../deploy/compose/compose.yml): one service, `.env` from
  [`.env.example`](../deploy/compose/.env.example). `docker compose up -d --scale xtandard-image=3`
  starts replicas, but only one can publish the host port: put a proxy in front, as below.
- [`compose.split.yml`](../deploy/compose/compose.split.yml): the image fleet (`XI_ROLE=image`,
  slim), the video fleet (`XI_ROLE=video`, full, one anonymous disk volume per replica at `/var/tmp`
  for the spool and the disk cache) and nginx routing the [video paths](#image-and-video-fleets).
  nginx re-resolves the service names every 10 s, so `docker compose up -d --scale image=4 --scale video=2`
  works without touching the proxy. nginx adds `X-Xi-Fleet` to responses (which fleet answered);
  drop that line in production if you prefer.

Compose has no autoscaler. `stop_grace_period: 40s`, `read_only`, `tmpfs`, `cap_drop` and the CPU
and memory limits are set; the image's own `HEALTHCHECK` is used.

### Docker Swarm

[`deploy/swarm/stack.yml`](../deploy/swarm/stack.yml): `replicas: 2`, CPU/memory limits and
reservations, `max_replicas_per_node`, rolling updates `start-first` one at a time with
`failure_action: rollback` after a 30 s monitor window, `rollback_config`, `restart_policy`, an
attachable overlay network for Prometheus to reach `:9090`, and secrets through `_FILE` variables
(commented). Stack-file details checked against the docker/cli converter: the service-level
`tmpfs:` key is ignored by `docker stack deploy`, so `/tmp` is a `type: tmpfs` volume; `security_opt`
and `cap_drop` are not applied by stack deploy either.

**Swarm has no autoscaler.** Options: scale by hand or from a cron job/CI on a metric
(`docker service scale xi_xtandard-image=N` driven by `xi_workers_busy` or CPU from Prometheus), or
a third-party autoscaler (not evaluated here). If you need autoscaling, use a platform below or
Kubernetes.

### Nomad

[`deploy/nomad/xtandard-image.nomad.hcl`](../deploy/nomad/xtandard-image.nomad.hcl): Docker driver
with `readonly_rootfs`, a tmpfs `/tmp`, `cap_drop = ["ALL"]`, `cores = 2` (workers follow the
cpuset) and `memory_max`, secrets from Nomad Variables through a template, readiness check on
`/readyz`, `shutdown_delay = "5s"` (deregister before SIGTERM) and `kill_timeout = "40s"`, rolling
updates with `auto_revert`. The `scaling` block sets min 2 / max 20 and a
[Nomad Autoscaler](https://developer.hashicorp.com/nomad/tools/autoscaling) policy (target-value on
`avg(xi_workers_busy)` from Prometheus, 2 minute cooldown); without the autoscaler agent, `count`
is fixed. The metrics port is a second Nomad service for Prometheus' Nomad service discovery.

### Kubernetes

Owned by WP-148: `deploy/kubernetes` (Kustomize) and `deploy/helm/xtandard-image` (the chart, with
`role`, HPA or KEDA autoscaling and min/max replicas), documented in [OPERATIONS.md](OPERATIONS.md).
[CONFIG.md §6](CONFIG.md#6-kubernetes) has a minimal manifest. The Terraform and Pulumi examples below
install that chart.

## Self-hosted PaaS

### Dokploy

Dokploy runs applications on Docker Swarm. Two ways:

- **Application**: image `ghcr.io/xantiagoma/xtandard-image:<version>-slim`, port 8080, the
  variables of the file below in *Environment*, a domain in *Domains*. Replicas are under
  *Advanced → Cluster Settings*; the *Swarm Settings* JSON editors take the same `UpdateConfig`,
  `RollbackConfig`, `RestartPolicy` and health check as the stack file. Redeploy after changing them.
- **Compose, type "Stack"**: paste [`deploy/dokploy/compose.yml`](../deploy/dokploy/compose.yml)
  (Swarm stack syntax, Traefik labels under `deploy.labels`, the external `dokploy-network`).

Dokploy does not autoscale: replicas are fixed. [CONFIG.md §4](CONFIG.md#4-dokploy) has the
minimal Application setup.

### Coolify

[`deploy/coolify/compose.yml`](../deploy/coolify/compose.yml) for the *Docker Compose* build pack:
`SERVICE_FQDN_XI_8080` makes Coolify generate a domain routed to port 8080, the secrets come from
the *Environment Variables* tab, and a named volume holds a disk result cache. Coolify v4 runs one
container per service: no replicas and no autoscaling (its Swarm mode is deprecated and will be
removed in v5). Scale by running the same service on several servers behind a load balancer, or
use another platform. [CONFIG.md §5](CONFIG.md#5-coolify-docker-compose) has a variant.

### Openship

[Openship](https://openship.io) ([oblien/openship](https://github.com/oblien/openship),
Apache-2.0) is a young self-hosted PaaS that deploys an existing `docker-compose.yml` as is (its
Compose mode on a Linux server with Docker, or bare mode over SSH) behind its own OpenResty edge.
Its README claims autoscaling on its cloud offering and multi-node self-hosting, but its
documentation did not say (at the time of writing) how prebuilt images, replicas, health checks or
secrets are configured. [`deploy/openship/docker-compose.yml`](../deploy/openship/docker-compose.yml)
is therefore the plain compose service; set the secrets in Openship's environment settings and route
the domain to port 8080. **Not verified on the platform**; autoscaling behaviour unknown.

## Managed platforms

### Fly.io

[`deploy/fly/fly.toml`](../deploy/fly/fly.toml) (image fleet) and
[`fly.video.toml`](../deploy/fly/fly.video.toml) (video fleet, a separate app with a volume).

- **How it scales**: Fly never creates Machines on demand. `fly scale count N` creates the
  **maximum**; the proxy stops idle Machines (`auto_stop_machines = "stop"`, or `"suspend"` for a
  faster resume) and starts stopped ones when the running ones pass `soft_limit`
  (`auto_start_machines = true`). `min_machines_running` (2 here, primary region only) never stop;
  `0` scales to zero.
- **Concurrency**: `type = "requests"`, `soft_limit = 8` (2 × workers on `performance-2x`),
  `hard_limit = 64` (the proxy stops sending more; the server's own queue still answers 429 past
  workers × 16). The video app uses 4 / 16.
- **Size**: `[[vm]] size = "performance-2x"`, `memory = "4gb"`; workers follow the vCPUs.
- **Health**: `[[http_service.checks]]` GET `/readyz` (10 s grace).
- **Shutdown**: `kill_signal = "SIGTERM"`, `kill_timeout = 40` (Fly's maximum is 300; video 150).
- **Metrics**: `XI_ADMIN_ADDR=":9090"` with `[[metrics]] port = 9090`: Fly scrapes it over the
  private network; the port is not in `[http_service]`, so it is not public.
- **Video**: `[mounts]` gives each Machine its own volume at `/data` for the spool and the disk
  cache. Fly creates the volume root-owned; the image runs as UID 65532, so create
  `/data/spool` and `/data/cache` owned by 65532 once (`fly ssh console`) if the server reports the
  spool directory not writable. Not verified on the platform.
- Secrets: `fly secrets set XI_SIGNING_KEYS=… XI_SIGNING_SALTS=…`. Private GHCR: deploy from an
  image Fly can pull (`fly deploy --image`, Fly's registry).

`flyctl config validate` needs a Fly session and an app, so the files are checked against
SchemaStore's `fly.json` instead. There is no maintained Terraform provider (the
`fly-apps/fly` provider is archived), so Fly has no Terraform module here.

### Google Cloud Run

[`deploy/cloud-run/service.yaml`](../deploy/cloud-run/service.yaml) (`gcloud run services replace`)
and [`service.video.yaml`](../deploy/cloud-run/service.video.yaml).

- **How it scales**: on requests in flight per instance (`containerConcurrency: 16`, about 8 per
  vCPU) and CPU, between `autoscaling.knative.dev/minScale` (1; `"0"` scales to zero, with a cold
  start) and `maxScale` (20).
- **CPU allocation**: `run.googleapis.com/cpu-throttling: "false"` keeps CPU allocated outside
  requests (instance-based billing), which background cache writes and stream prefetch need; the
  default (request-based) throttles it between requests. `startup-cpu-boost` and the second
  generation execution environment are on.
- **Probes**: startup probe on `/readyz` (1 s period, 30 tries), liveness on `/healthz`.
- **Shutdown**: Cloud Run sends SIGTERM and kills 10 s later, so the files set
  `XI_SERVER_SHUTDOWN_DELAY=1s` and `XI_SERVER_SHUTDOWN_TIMEOUT=8s`.
- **Registry**: Cloud Run pulls from Artifact Registry (or Docker Hub): copy the GHCR image there or
  use a remote repository; `PROJECT` is a placeholder.
- **Secrets**: Secret Manager through `secretKeyRef`.
- **Metrics**: the admin listener stays on loopback; scrape it with a sidecar container in the same
  service (Google's Managed Service for Prometheus sidecar or an OpenTelemetry Collector), or rely
  on Cloud Run's request metrics. Not included here.
- **Video**: Cloud Run has no local disk; the spool is an in-memory `emptyDir` that counts against
  the memory limit (8 GiB here, `XI_VIDEO_TRANSFORM_SPOOL_MAX_BYTES=2GiB`), `timeoutSeconds: 300`,
  `containerConcurrency: 4`, ingress `internal-and-cloud-load-balancing` behind a URL map that
  routes the video paths. Large-video workloads fit better on a platform with disks.

### AWS ECS on Fargate

[`deploy/aws-ecs/`](../deploy/aws-ecs/) has the AWS CLI inputs: `task-definition.json`
(`aws ecs register-task-definition --cli-input-json file://…`), `service.json` (`create-service`),
`scalable-target.json` (`aws application-autoscaling register-scalable-target`) and two policies
(`put-scaling-policy`). The [Terraform](#terraform--opentofu), [Pulumi](#pulumi), [SST](#sst) and
[CDK](#aws-cdk) programs build the same thing with the ALB.

- **How it scales**: Application Auto Scaling target tracking between `MinCapacity` 2 and
  `MaxCapacity` 20, on `ECSServiceAverageCPUUtilization` 60 % and optionally
  `ALBRequestCountPerTarget` (requests per task per minute); scale-out cooldown 60 s, scale-in
  300 s. With two policies the larger desired count wins and scale-in needs both to agree. No scale
  to zero on demand.
- **Task**: 2 vCPU / 4 GiB on Graviton (ARM64), `readonlyRootFilesystem`, `user 65532`, `/tmp` as a
  task volume (Fargate has no tmpfs; it is the task's ephemeral storage), capabilities dropped, the
  image's health check, `stopTimeout: 40` (Fargate allows up to 120).
- **Load balancer**: target group health check `/readyz`, deregistration delay 30 s, deployment
  circuit breaker with rollback, 100 % / 200 % deployment bounds.
- **Secrets**: Secrets Manager through `secrets`; a private GHCR needs `repositoryCredentials`.
- **Metrics**: loopback; add an ADOT or Prometheus sidecar container to scrape `127.0.0.1:9090`.
- **Video**: a second service with `XI_ROLE=video`, `ephemeralStorage` up to 200 GiB for the
  spool and an ALB listener rule on the video paths.

### AWS App Runner

[`deploy/aws-app-runner/`](../deploy/aws-app-runner/): `autoscaling.json`
(`aws apprunner create-auto-scaling-configuration`) and `create-service.json`. **App Runner no longer
accepts new customers** (since 2026-04-30; AWS points to ECS Express Mode); the files are for
existing accounts.

- **How it scales**: on concurrent requests per instance: `MaxConcurrency` 16, `MinSize` 1 (cannot
  be 0; provisioned instances bill memory only), `MaxSize` 10 (up to 25 by default).
- 2 vCPU / 4 GB, HTTP health check on `/readyz`, secrets from Secrets Manager. App Runner pulls only
  from ECR (copy the image), exposes one port, has 3 GB of ephemeral storage including the image and
  a 120 s request timeout: images only, no video fleet. The admin listener is not reachable.

### Azure Container Apps

[`deploy/azure-container-apps/containerapp.yaml`](../deploy/azure-container-apps/containerapp.yaml)
(`az containerapp create --yaml`), and the [Terraform module](#terraform--opentofu).

- **How it scales**: KEDA rules. An `http` rule adds a replica per `concurrentRequests` (8) in
  flight (measured over 15 s windows) and a custom `cpu` rule targets 65 %; the larger result wins.
  `minReplicas` 1, `maxReplicas` 20 (up to 1000). Scale-up is immediate, scale-down waits for a
  300 s stabilization window. `minReplicas: 0` scales to zero with the http rule (the cpu rule does
  not wake a revision from zero).
- 2 vCPU / 4 GiB on the Consumption profile, startup/readiness probes on `/readyz`, liveness on
  `/healthz`, `terminationGracePeriodSeconds: 40`, `/tmp` on an `EmptyDir` (replica-local disk).
- Secrets: app secrets (or Key Vault references) mapped with `secretRef`; GHCR credentials as a
  registry secret.
- Metrics: loopback by default; expose `:9090` as an internal port (`additionalPortMappings`,
  internal ingress) for a collector in the same environment.
- Video: a second app with `XI_ROLE=video`; the `EmptyDir` is bounded by the replica's ephemeral
  storage, so large spools need an Azure Files volume (not verified here).

### Railway

[`deploy/railway/railway.json`](../deploy/railway/railway.json) (config as code): `numReplicas: 2`,
`healthcheckPath: /readyz` (used during deploys), `drainingSeconds: 40` (SIGTERM to SIGKILL),
`overlapSeconds`, a restart policy and `limitOverride` for 2 vCPU / 4 GB. The image itself is set as
the service source in the dashboard or CLI (config as code has no image key); variables and secrets
in the service's *Variables*.

- **How it scales**: Railway has **no horizontal autoscaling**: replicas are a fixed number
  (`numReplicas`, per region with `multiRegionConfig`), and each replica scales vertically up to the
  plan's limits. Traffic goes to the nearest region and randomly across its replicas.
  `sleepApplication` stops an idle service (off here).
- A service has at most one volume and volumes cannot be used with replicas: run a video service as
  one replica with a volume, or keep the spool in memory. `:9090` is reachable only on the private
  network (`<service>.railway.internal`) unless you expose it.

### Render

[`deploy/render/render.yaml`](../deploy/render/render.yaml) (a Blueprint) with an image web service
and a video web service.

- **How it scales**: autoscaling keeps average CPU (60 %) and memory (80 %) near their targets
  between `minInstances` (2; at least 1, no scale to zero) and `maxInstances` (10). It needs a
  Professional workspace or higher; `numInstances` is ignored when `scaling` is set.
- `plan: 2c-4g`, `healthCheckPath: /readyz`, `maxShutdownDelaySeconds: 40` (video 150; maximum
  300), secrets as `sync: false` (asked for at Blueprint creation), `PORT=8080`. Private GHCR
  through a registry credential named `ghcr`.
- A service with a persistent disk cannot scale, so the video service keeps its spool in the
  container filesystem (`XI_VIDEO_TRANSFORM_SPOOL_MAX_BYTES=4GiB`). The admin port is reachable on
  Render's private network only.

### DigitalOcean App Platform

[`deploy/digitalocean/app.yaml`](../deploy/digitalocean/app.yaml) (`doctl apps create --spec`): an
`image` service and a `video` service, and `ingress.rules` routing `/video/`, `/q/video` and
`/_stream/` to the video one with `preserve_path_prefix` (App Platform strips the prefix otherwise).

- **How it scales**: `autoscaling.min_instance_count` 2 / `max_instance_count` 10 on CPU 60 %. CPU
  autoscaling needs dedicated-CPU sizes (`apps-d-*`); `requests_per_second.per_instance` and
  `request_duration.p95_milliseconds` metrics are also accepted. No scale to zero. Do not set
  `instance_count` with autoscaling.
- Readiness `health_check` and `liveness_health_check`, `termination` with `drain_seconds: 5` and
  `grace_period_seconds: 40` (video 150), secrets as `type: SECRET`, GHCR with
  `registry_credentials`, the admin port as an `internal_ports` entry.
- App Platform has no persistent disk; the video spool lives in the container filesystem.
- `doctl apps spec validate` needs an API token (without one, `--schema-only` only parses the YAML),
  so the spec is validated against DigitalOcean's published OpenAPI `app_spec` schema instead.

## Infrastructure as code

Each tool's directory is a self-contained example, not a published module. Settings match the
platform files above.

### Terraform / OpenTofu

[`deploy/terraform/`](../deploy/terraform/), one root module per target, providers pinned to exact
versions (lock files are not committed: OpenTofu and Terraform use different registries):

| Module | What | Autoscaling |
|---|---|---|
| [`cloud-run`](../deploy/terraform/cloud-run/) | `google_cloud_run_v2_service`, Secret Manager secrets, a service account, public invoker | `scaling { min_instance_count, max_instance_count }`, `max_instance_request_concurrency`, `cpu_idle = false` |
| [`aws-ecs-fargate`](../deploy/terraform/aws-ecs-fargate/) | VPC inputs, ALB + target group (`/readyz`) + listener (HTTP, or HTTPS with a certificate), ECS cluster, task definition, service with circuit breaker, Secrets Manager, IAM, optional CloudFront in front (`cloudfront.tf`, cache key with `Accept`) | `aws_appautoscaling_target` min/max, CPU target tracking and optional `ALBRequestCountPerTarget`, 60 s / 300 s cooldowns |
| [`azure-container-apps`](../deploy/terraform/azure-container-apps/) | resource group, Log Analytics, environment, `azurerm_container_app` | `min_replicas`, `max_replicas`, `http_scale_rule`, `custom_scale_rule` (cpu) |
| [`kubernetes-helm`](../deploy/terraform/kubernetes-helm/) | namespace, signing Secret, `helm_release` of `deploy/helm/xtandard-image` | chart values `autoscaling.enabled/minReplicas/maxReplicas`, `role`, `image.tag`, all in `local.values` |

```sh
cd deploy/terraform/aws-ecs-fargate
tofu init && tofu plan -var vpc_id=… -var 'public_subnet_ids=[…]' -var 'private_subnet_ids=[…]'
```

Fly.io has no maintained Terraform provider; Cloudflare in front of any of these is a DNS record
plus cache rules (see [OPERATIONS.md](OPERATIONS.md)). Providers: hashicorp/google 8.8.1,
hashicorp/aws 6.70.0, hashicorp/azurerm 5.11.0, hashicorp/helm 3.3.0, hashicorp/kubernetes 3.4.0.

### Pulumi

[`deploy/pulumi/`](../deploy/pulumi/), TypeScript with Bun (`Pulumi.yaml` sets
`packagemanager: bun`), one program and three targets: `pulumi config set target aws|gcp|k8s`.

- `aws` ([`aws.ts`](../deploy/pulumi/aws.ts)): `awsx.lb.ApplicationLoadBalancer` (health check
  `/readyz`, deregistration 30 s) + `awsx.ecs.FargateService` (ARM64, read-only root, `/tmp`
  volume, the image health check, circuit breaker) + an Application Auto Scaling target (min/max
  from config) with a CPU 60 % target-tracking policy.
- `gcp` ([`gcp.ts`](../deploy/pulumi/gcp.ts)): `gcp.cloudrunv2.Service` with min/max instances,
  concurrency 16, CPU always allocated, Secret Manager secrets.
- `k8s` ([`k8s.ts`](../deploy/pulumi/k8s.ts)): `k8s.helm.v3.Release` of `../helm/xtandard-image`
  with `role`, `image.tag` and `autoscaling.*` (the only place it sets chart values).

```sh
cd deploy/pulumi && bun install
pulumi config set target aws && pulumi config set --secret signingKeys <hex> && pulumi config set --secret signingSalts <hex>
pulumi preview
```

### SST

[`deploy/sst/sst.config.ts`](../deploy/sst/sst.config.ts): `sst.aws.Vpc` + `sst.aws.Cluster` +
`sst.aws.Service` (SST 4.17; the `Service` and `Cluster` components are the v3 API, v4 moved the
underlying Pulumi AWS provider to v7). `scaling: { min: 2, max: 20, cpuUtilization: 60,
memoryUtilization: 80, requestCount: 1200, scaleOutCooldown: "60 seconds", scaleInCooldown: "300
seconds" }`, `cpu: "2 vCPU"`, `memory: "4 GB"`, ARM64, ALB rule `80/http → 8080/http` with the
`/readyz` health check, the container health check, secrets through `ssm` (Secrets Manager ARNs),
and a `transform` that makes the root filesystem read-only with a `/tmp` volume, UID 65532 and a
40 s stop timeout.

```sh
cd deploy/sst && bun install && bunx sst install   # generates .sst/platform (types)
XI_SIGNING_KEYS_ARN=… XI_SIGNING_SALTS_ARN=… bunx sst deploy --stage production
```

### AWS CDK

[`deploy/cdk/`](../deploy/cdk/): `ApplicationLoadBalancedFargateService` with an explicit
`FargateTaskDefinition` (ARM64, 2 vCPU / 4 GiB, read-only root with a `/tmp` volume, UID 65532,
40 s stop timeout, the image health check, Secrets Manager secrets), target group health check
`/readyz` and 30 s deregistration, circuit breaker with rollback, and
`autoScaleTaskCount({ minCapacity: 2, maxCapacity: 20 })` with `scaleOnCpuUtilization` (60 %) and
`scaleOnRequestCount` (1200 per target), 60 s / 300 s cooldowns. The stack is environment-agnostic,
so `cdk synth` works without credentials.

```sh
cd deploy/cdk && bun install && bunx cdk synth && bunx cdk deploy
```

## Verification

Two tasks, both runnable without a cloud account (Docker and Bun on the host; every other tool runs
from a pinned container or a pinned package):

```sh
task deploy:platforms:check   # everything below except the local run
task deploy:platforms:run     # compose single + split + Swarm against $XI_IMAGE (default xtandard-image:main-gpl)
```

| Check | Tool | Files |
|---|---|---|
| Compose and stack files render | `docker compose config`, `docker stack config` | compose, split, Swarm, Dokploy, Coolify, Openship |
| nginx router | `nginx -t` (nginx 1.29) | `deploy/compose/nginx.conf` |
| JSON Schemas | `check-jsonschema` 0.38.2 | `fly.toml` (SchemaStore `fly.json`), `railway.json` (`railway.schema.json`), `render.yaml` (`render.yaml.json`) |
| OpenAPI schemas, unknown keys rejected | `deploy/check/validate.py` | DigitalOcean app spec (`app_spec` of DigitalOcean's public OpenAPI), Container Apps YAML (`ContainerApp` of the Azure REST spec 2026-01-01) |
| AWS inputs | botocore 1.43 service models | ECS task definition, service, scalable target and policies; App Runner service and autoscaling |
| Cloud Run YAML | kubeconform 0.7 with the Knative Serving v1 schema (Cloud Run annotations are not checked) | `deploy/cloud-run/*.yaml` |
| Nomad | `nomad job validate` (Nomad 2.0.7, no agent) | `deploy/nomad` |
| The server accepts each environment | `xtandard-image config validate` in `$XI_IMAGE` (unknown `XI_*` variables, bad values, roles) | every container of the compose, Swarm, Dokploy, Coolify, Fly, Cloud Run, Container Apps, Render, DigitalOcean, ECS and App Runner files |
| Terraform | OpenTofu 1.13.2 `fmt -check`, `init -backend=false`, `validate`, tflint 0.65 (aws, google, azurerm rulesets); also passes with `XI_TF_IMAGE=hashicorp/terraform:1.17.0` | `deploy/terraform/*` |
| TypeScript | `bun install --frozen-lockfile`, `tsc --noEmit`, `sst install`, `cdk synth` without credentials | `deploy/pulumi`, `deploy/sst`, `deploy/cdk` |
| Node licences | `bun deploy/check/licenses.ts` | the three Node projects |
| Scripts | shellcheck 0.11 | `deploy/check/*.sh`, `deploy/docker/run.sh` |

`task deploy:platforms:run` ([`deploy/check/compose-run.sh`](../deploy/check/compose-run.sh), host
port 18480 by default) brings up `compose.yml` with the fixtures as a local source, then
`compose.split.yml` with two image replicas, one video replica and nginx, and checks `/healthz`,
`/readyz`, a signed image (200), an unsigned one (403), that 9090 is not published, that `/video/…`
(a signed WebM transcode, 200), `/q/video` and `/_stream/…` reach the video fleet and images the
image fleet, and that each fleet refuses the other's requests directly. Then, only if the host is
not already a swarm node, it creates a single-node swarm, deploys `stack.yml`, checks 2/2 replicas
and the update/rollback settings, removes the stack and leaves the swarm. Everything it creates is
removed (`down -v`).

No GitHub workflow runs these yet (Actions minutes are exhausted); the tasks are the entry point.

## Licences

The Node tooling in `deploy/pulumi`, `deploy/sst` and `deploy/cdk` is development-only: it is run to
describe or deploy infrastructure and never ships in an image or a client. Each directory has its own
`package.json` and `bun.lock` outside the root Bun workspace. `deploy/check/licenses.ts` applies the
ADR 0006 allowlist (MIT, BSD, Apache-2.0, ISC) and additionally accepts BlueOak-1.0.0, CC0-1.0 and
Python-2.0 (permissive; isaacs' glob/minimatch/tar family, SPDX data, argparse), with two named
exceptions: `spdx-exceptions` (CC-BY-3.0 data) and `@logdna/tail-file` (MIT licence file, no SPDX
field). Pulumi SDKs, SST and the AWS CDK are Apache-2.0.

Terraform providers, OpenTofu, Terraform, tflint, Nomad, kubeconform and check-jsonschema are
external tools that are only run (ADR 0006): the HashiCorp providers are MPL-2.0, OpenTofu is
MPL-2.0, Terraform and Nomad are BUSL-1.1, tflint is MPL-2.0 (its rulesets MPL-2.0), kubeconform
Apache-2.0, check-jsonschema Apache-2.0.
