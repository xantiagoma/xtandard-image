# Operations

This guide is for running xtandard-image in production. It covers how the server uses CPU, memory and disk, how big a replica should be, how to autoscale a fleet, how to put a CDN in front, which result cache tiers to use, and how to observe the whole thing. Platform recipes (Docker Compose, Swarm, Fly.io, Cloud Run, ECS, Azure Container Apps, Modal, Terraform, Pulumi and others) are in [DEPLOY.md](DEPLOY.md). Every variable is listed in [CONFIG.md §2](CONFIG.md#2-environment-variable-reference).

The deployable files live in [`deploy/`](../deploy/):

| Path | What it is |
|---|---|
| [`deploy/helm/xtandard-image/`](../deploy/helm/xtandard-image/) | Helm chart. Install it once per fleet (image, video). Values: [`values.yaml`](../deploy/helm/xtandard-image/values.yaml), checked by `values.schema.json`. Example fleets: [`deploy/helm/values-image.yaml`](../deploy/helm/values-image.yaml), [`deploy/helm/values-video.yaml`](../deploy/helm/values-video.yaml) |
| [`deploy/kubernetes/`](../deploy/kubernetes/) | Kustomize base (hardened Deployment, Service, ConfigMap, PDB, NetworkPolicy, ServiceAccount, HPA on CPU), components (`role-image`, `role-video`, `keda`, `hpa-queue-metric`, `monitoring`) and overlays `image`, `video`, `keda`, `monitoring` |
| [`deploy/observability/`](../deploy/observability/) | Prometheus alert and SLO rules with unit tests, Grafana dashboard, OpenTelemetry Collector config, and a local Compose stack (server, Prometheus, Grafana, Collector, Tempo, k6) |
| [`deploy/scripts/`](../deploy/scripts/) | `check.sh` (static checks), `gen.sh` (copies the rules and dashboard into the chart and the Kustomize component), `kind-autoscale.sh` (end-to-end autoscaling test in a local kind cluster) |

[`docs/examples/k8s/`](examples/k8s/) stays the reference for a ConfigMap config file with several sources and Secrets mounted as files. A test loads it.

## 1. How the server uses resources

### 1.1 CPU and workers

Processing (decode, transform, encode) runs on a fixed number of **workers**, `XI_PROCESSING_WORKERS`. It defaults to `GOMAXPROCS`, and since Go 1.25 `GOMAXPROCS` follows the container's CPU limit (the cgroup quota, rounded up). So **the CPU limit is the worker count**: a pod with `limits.cpu: 2` runs two images at a time. Each job uses one locked OS thread and libvips runs with concurrency 1 (`XI_VIPS_CONCURRENCY`), so one worker is about one core. Set whole-number CPU limits. A fractional limit is rounded up, with a minimum of 2: a container limited to 0.5 CPU reports `go_sched_gomaxprocs_threads 2` (measured with `docker run --cpus 0.5`), so two workers share half a core.

Everything else runs outside the workers: TLS and HTTP, result cache hits, and source fetches (bounded separately by `XI_FETCH_MAX_CONCURRENT`, default 256). A cache-hit-heavy pod can be CPU-busy with an empty queue. `/info` requests are cheap header reads but do take a queue permit, so a flood of them can fill the queue ([bench/RESULTS.md](../bench/RESULTS.md)).

### 1.2 Admission: queue, 429 and 503

Each processing request passes admission before it decodes anything (ARCHITECTURE §11):

1. **Queue permit.** Capacity is workers + `XI_PROCESSING_QUEUE_SIZE` (default workers × 16). When it is full, the request fails fast with **429** and `Retry-After` (`xi_overload_total{reason="queue_full"}`).
2. **Worker slot.** The request waits at most `XI_PROCESSING_QUEUE_TIMEOUT` (5 s), then gets **503** and `Retry-After` (`reason="wait_timeout"`).
3. **Memory budget** (optional). With `XI_PROCESSING_MEMORY_BUDGET` set, the job also reserves its planned peak decoded bytes. If the budget stays full past the queue timeout, the request gets 503 (`reason="memory_budget"`). A single job larger than the whole budget gets 422.

`xi_queue_depth` counts jobs that hold a queue permit (running plus waiting). `xi_workers_busy` counts the running ones. `xi_semaphore_wait_seconds` is the wait for a worker. These are the autoscaling signals (§3).

The bounded queue is deliberate. Near saturation the server answers the excess quickly instead of letting latency grow without limit. In the benchmark, imgproxy reached 1.35 s p99 at the same load where xtandard-image stayed at 87 ms and shed 429s (bench/RESULTS.md). Behind a CDN that retries, and with an autoscaler watching the queue, a 429 is usually the right answer. Clients see it only while the fleet is scaling up.

Video transformations have their own pool with cost-based admission (`XI_VIDEO_TRANSFORM_*`, ARCHITECTURE §11.1, ADR 0012 §4):

- Up to `XI_VIDEO_TRANSFORM_MAX_JOBS` transcodes run at once, within `XI_VIDEO_TRANSFORM_CAPACITY` cost units. One unit is about a millisecond of one core's work.
- `XI_VIDEO_TRANSFORM_QUEUE` jobs and `XI_VIDEO_TRANSFORM_QUEUE_COST` units may wait. Beyond that the request gets 429.
- A job that waits longer than `XI_VIDEO_TRANSFORM_QUEUE_TIMEOUT` gets 503.

Image and video work never share a semaphore.

### 1.3 Memory

A replica's memory has three parts:

- **libvips and codecs (C memory).** Bounded by the input limits (`XI_LIMITS_*`) times the worker count, and optionally by `XI_PROCESSING_MEMORY_BUDGET`. `xi_vips_mem_bytes` and `xi_vips_mem_highwater_bytes` report what libvips tracks.
- **jemalloc.** The image preloads jemalloc with `MALLOC_CONF=narenas:2`. With its default of 4 arenas per CPU, freed pages stayed resident and peak RSS more than doubled under load (WP-87). Keep the image's `MALLOC_CONF`.
- **The Go heap.** It is small: buffers are C-allocated and owned by vips.

At startup the server sets the Go memory limit (`GOMEMLIMIT`) to `XI_PROCESSING_GO_MEMORY_FRACTION` (30 %) of the cgroup memory limit: cgroup v2 `memory.max`, or v1 `memory.limit_in_bytes`, the smallest from the process's own cgroup up to the root (so a pod-level limit counts too). It logs the result once:

```json
{"level":"INFO","msg":"go memory limit set","gomemlimit_bytes":322122547,"cgroup_memory_limit_bytes":1073741824,"memory_fraction":0.3,"cgroup":"v2"}
```

`go_gc_gomemlimit_bytes` shows the same value. Without a memory limit (`max`), the runtime default (no limit) stays and the log says `go memory limit unchanged: no cgroup memory limit`. A `GOMEMLIMIT` you set yourself wins (`go memory limit from GOMEMLIMIT`); set it only to override the fraction. When `XI_PROCESSING_MEMORY_BUDGET` is set and the Go share plus the budget exceed the limit, the server warns at startup (`go memory limit and memory budget exceed the cgroup memory limit`): lower one of them.

Never set it to the whole limit: most of the memory is C memory that the Go GC cannot see.

`XI_PROCESSING_MEMORY_BUDGET` makes memory admission explicit. Set it to about 60–70 % of the memory limit to keep a burst of large images from triggering the OOM killer; the excess waits or gets 503 instead. Its weights are planner estimates, so watch RSS (`process_resident_memory_bytes`) against the limit and adjust. The alert `XtandardImageMemoryNearLimit` fires at 90 %.

### 1.4 Disk

The root file system is read-only. `/tmp` is a small RAM-backed `emptyDir` (64 MiB) for libraries that need a temporary directory. Two features need real disk:

- **Video spool** (`XI_VIDEO_TRANSFORM_SPOOL_DIR`). Sources and outputs above `XI_VIDEO_TRANSFORM_SPOOL_THRESHOLD` (32 MiB) go to unlinked spool files instead of memory.
  - Mount a disk-backed volume there: an `emptyDir` without `medium: Memory`, with a `sizeLimit`.
  - Set `XI_VIDEO_TRANSFORM_SPOOL_MAX_BYTES` below that limit. The server then answers 503 (`xi_video_spool_rejections_total`) before the kubelet evicts the pod for exceeding its ephemeral storage.
  - The chart and the `role-video` component mount `/spool`: 20 Gi volume, 16 GiB server bound.
- **Disk result cache** (`XI_CACHE_RESULT_DISK_PATH`, §5). An `emptyDir` gives a per-pod cache that is lost on reschedule. A PersistentVolume needs a StatefulSet, because each directory must belong to exactly one process.

### 1.5 Roles: splitting image and video fleets

`XI_ROLE` decides what an instance serves. The same binary and image serve every role.

| Role | Serves | Answers 404 to |
|---|---|---|
| `all` (default) | everything | — |
| `image` | images, `/info`, raw | video transformations (no video pool is created) |
| `video` | video transformations and their HLS/DASH parts under `/_stream/` | everything else |

`/healthz`, `/readyz` and the admin listener work on every role. The `video` role needs `XI_VIDEO_TRANSFORM_ENABLED=true` and the `full` or `gpl` image.

Split the fleets when video is a real share of the load:

- A transcode runs for seconds to minutes and has a different cost profile.
- A native crash in a demuxer then takes down only a video pod.
- Each fleet scales on its own signal.

At the load balancer, route `/video/*`, `/q/video`, the video endpoints of the other dialects you use (Cloudinary `/video/upload/`, ImageKit video URLs, Cloudflare `/cdn-cgi/media/`) and `/_stream/*` to the video fleet. Everything else goes to the image fleet. With ingress-nginx, two Ingress objects on the same host merge, and the longest prefix wins; `deploy/helm/values-video.yaml` shows the paths. Give both fleets the same Secret: `XI_VIDEO_TRANSFORM_STREAM_KEYS` (or the first `XI_SIGNING_KEYS` key it is derived from) must be identical on every video pod, or manifest child URLs work only on the pod that wrote them.

## 2. Sizing

### 2.1 Starting points (images)

The numbers below come from [bench/RESULTS.md](../bench/RESULTS.md). **They are from an Apple M1 Pro laptop (8 cores, arm64, Docker in OrbStack) that was not idle.** The host load average had a median of 14 to 35 during the runs, and single-run spread is large. Treat them as orders of magnitude, measure on your own hardware (§2.3), and expect amd64 cloud vCPUs (often one hyperthread) to be slower per "core". All cases ran with the origin cache and result cache off, so these are cache-miss costs.

| Workload (8 workers, closed loop) | Throughput | Per worker (≈ per core) | Peak RSS (16 concurrent) |
|---|---:|---:|---:|
| 400 px fit, JPEG → JPEG | 286–359 req/s | 35–45 req/s | 173–309 MiB |
| 400 px fit, JPEG → WebP | 224 req/s | ~28 req/s | 298 MiB |
| 400 px fit, JPEG → AVIF (effort 1) | 237 req/s | ~30 req/s | 482 MiB |
| 1200×1200 fill, JPEG → JPEG | 86–87 req/s | ~11 req/s | 359–692 MiB |
| 400 px fit, PNG with alpha → WebP | 61–96 req/s | 8–12 req/s | 260–581 MiB |
| 240 px animated GIF (12 frames) → WebP | 24–35 req/s | 3–4 req/s | 331–749 MiB |

The lower memory figure in each range is from the WP-87 rerun (`MALLOC_CONF=narenas:2`, now the image default); the higher one is from the first run. The bench pins AVIF to effort 1 to match imgproxy. The server default is effort 4 (`XI_FORMATS_AVIF_EFFORT`), which is several times slower per image.

What this suggests per replica:

- **CPU: 2 to 4 cores** (`requests.cpu = limits.cpu`, a whole number). Two is the smallest size that keeps one slow image from blocking the pod. Beyond 4 to 8, use more replicas rather than bigger ones: smaller pods scale in finer steps and lose less on a crash.
- **Memory: about 1 GiB per worker for mixed traffic**, with a floor of 1 GiB. Photo thumbnails need far less, around 40–60 MiB per worker plus about 100 MiB base at the observed peaks. Large outputs, alpha PNGs and animations need more. The chart default (2 CPU, 1 Gi request, 2 Gi limit) is sized for mixed traffic with headroom.
- **Throughput:** expect roughly `workers × per-worker rate × your cache-miss ratio⁻¹` before the queue fills. A 2-core pod doing 400 px WebP thumbnails handles about 50 misses per second on this hardware.

### 2.2 Video

There are no published video benchmarks yet. Video cost depends on the output resolution, the codec and the duration far more than on the request rate. The server's own estimate is visible:

- `xi_video_transform_job_cost` is a histogram of estimated cost units, about a millisecond of one core each.
- `xi_video_transform_duration_seconds` is the measured time per job.

Guidance:

- **A dedicated video fleet with 4+ cores per pod.** Set `XI_VIDEO_TRANSFORM_WORKERS` to the CPU count; the default `GOMAXPROCS/4` assumes the pod also serves images. Keep `XI_VIDEO_TRANSFORM_THREADS=1` and scale with jobs, unless single-job latency matters more than throughput.
- **Memory.** A transcode holds at most twice the spool threshold (2 × 32 MiB) of source and output in memory, plus the codecs' working memory, which grows with resolution and with AV1 or HEVC. Start at **1 GiB per running job** (`MAX_JOBS` defaults to 2 × workers). Lower it after watching RSS under your real mix.
- **Disk.** Size the spool for the largest sources × concurrent jobs: sources up to `XI_VIDEO_TRANSFORM_MAX_INPUT_BYTES` (512 MiB), outputs up to `_MAX_OUTPUT_BYTES` (256 MiB) each. `deploy/helm/values-video.yaml` uses 4 CPU, 4 Gi, a 20 Gi spool and a disk result cache.
- **Drain.** A transcode may run for `XI_VIDEO_TRANSFORM_TIMEOUT` (2 min). Give the video fleet `XI_SERVER_SHUTDOWN_TIMEOUT=150s` and a matching termination grace period; the chart does this for you.

### 2.3 Measuring your own numbers

1. Run [`bench/run.sh`](../bench/run.sh) (`task bench`; method in [BENCHMARKS.md](BENCHMARKS.md)) on the instance type you will deploy to, with `BENCH_WORKERS` set to your planned CPU limit. Replace the cases in `bench/k6/scenario.js` with URLs from your real traffic: sizes, formats and sources.
2. Or load one replica in place. Use the local stack (§6.6) or a staging pod, and drive [`deploy/observability/load/traffic.js`](../deploy/observability/load/traffic.js) with your URL mix (`IMAGES`, `FORMATS`, `RATE`). Raise `RATE` until `xi_overload_total` starts to grow.
3. Watch, on the dashboard:
   - `xi_workers_busy / go_sched_gomaxprocs_threads`. Saturation is 1.
   - `xi_queue_depth`. The KEDA target is 0.75 jobs per worker.
   - p95/p99 of `xi_request_duration_seconds` and `xi_semaphore_wait_seconds`.
   - `process_resident_memory_bytes` and `xi_vips_mem_highwater_bytes` against the memory limit.
   - `rate(xi_overload_total[1m])`.
4. The sustainable rate per replica is the highest rate with p99 inside your SLO and no shedding. Set the autoscaler target below it (§3.2), and the memory limit at about 1.5 × the peak RSS you saw.

## 3. Autoscaling

### 3.1 Which signal

| Signal | Metric | Why |
|---|---|---|
| **Jobs per worker** (primary) | `xi_queue_depth / go_sched_gomaxprocs_threads`, per pod | Says how far over capacity a pod is: 0.5 is half busy, 3 means three jobs per worker are running or waiting. The autoscaler can jump straight to the right size. |
| **Shed rate** | `rate(xi_overload_total[1m])` | Any sustained 429/503 means the fleet is too small right now, even when a one-minute average of the queue looks fine. |
| **CPU** | container CPU utilisation | Works everywhere and catches work the queue does not see (cache hits, TLS, fetches), but it is a poor primary signal (below). |
| **In-flight requests** | `xi_requests_in_flight` | Includes cache hits and requests waiting for the origin, so it overstates processing load. Use it only where the platform scales on concurrency anyway (Cloud Run, Fly). |
| **Video utilisation** (video fleet) | `(xi_video_transform_capacity_used + xi_video_transform_queued_cost) / xi_video_transform_capacity` | Cost units held plus waiting over capacity. Transcode count alone is not a signal: one 4K job can cost a hundred audio jobs. |

**Why CPU alone lags.** CPU utilisation is capped at 100 % of the limit, so it cannot say how much more work is waiting. The HPA formula `desired = current × utilisation / target` grows the fleet by at most 100/65 ≈ 1.5× per step at a 65 % target, however deep the queue is. On top of that come the metrics pipeline (kubelet → metrics-server, refreshed every 15–60 s) and the HPA sync period (15 s), so a burst gets several rounds of 429s before CPU-based scaling catches up. The queue ratio sees the burst at the next scrape and asks for the full size at once.

Use the queue ratio and the shed rate as primary triggers and keep CPU as a floor. The chart's KEDA mode (`autoscaling.type: keda`) does exactly that.

### 3.2 Targets

- **Jobs per worker: 0.75** (`autoscaling.keda.queueUtilisationTarget`). This keeps a quarter of the workers free for bursts while the fleet scales. Lower it (0.5) for spiky traffic or a tight latency SLO, raise it (0.9) for steady batch-like traffic. Above 1, jobs wait by design.
- **Shed rate: 1 per second per pod** (`overloadTarget`).
- **CPU: 65–70 %** when CPU is the only signal (the HPA default here).
- **Video: 0.8** of cost capacity (`videoUtilisationTarget`).

### 3.3 Minimum and maximum replicas

**Minimum.**

- At least 2 for availability, at least one per zone you spread over, and enough to carry your off-peak traffic at the target.
- The PDB (`maxUnavailable: 1`) only protects anything with 2 or more.
- Scale-to-zero (`minReplicas: 0` with KEDA, Cloud Run, Fly auto-stop) is fine for a video fleet or a staging environment. The first request then waits for a cold start (§3.6), and the in-memory caches start empty.

**Maximum.** It is a budget and blast-radius decision, bounded by whichever of these comes first:

- **Origin bandwidth.** Every cache miss downloads the source. A replica doing 50 misses per second on 2 MB sources pulls 100 MB/s (800 Mbit/s). `rate(xi_source_bytes_total[5m])` per replica, times max replicas, must fit what your origin or bucket and the network between can serve. Origin rate limits and egress costs apply too. Shared origin stores (`XI_CACHE_ORIGIN_STORES`, §5) cut this for a large fleet.
- **Budget.** Max replicas × CPU and memory per replica at your cloud's price is the worst-case hourly cost. Set it where a traffic spike (or an abusive client: keep signing on) cannot run up the bill.
- **Cluster capacity.** Nodes, memory, IP addresses, plus the cluster autoscaler's own scale-up time.
- **Shared services.** Redis connection limits (go-redis pool per pod), S3 request rates and the OTel Collector all have limits of their own.

Start with `max = 2–3 × the replicas your measured peak needs`. Then use the `XtandardImageOverloaded` alert (shedding above 5 % for 10 minutes) as the sign that max is too low or scaling too slow.

### 3.4 Up fast, down slow

Scale-up should be immediate: a missing replica means 429s. Scale-down should be slow:

- A removed pod loses its in-memory result and origin caches.
- It must drain.
- Traffic often oscillates, and flapping wastes both.

The chart and the Kustomize base use this behaviour (autoscaling/v2 `behavior`, also passed to KEDA's HPA):

```yaml
scaleUp:   { stabilizationWindowSeconds: 0,   policies: [{ type: Percent, value: 100, periodSeconds: 15 }, { type: Pods, value: 4, periodSeconds: 15 }], selectPolicy: Max }
scaleDown: { stabilizationWindowSeconds: 300, policies: [{ type: Percent, value: 20, periodSeconds: 60 }], selectPolicy: Min }
```

So the fleet can double (or add 4 pods) every 15 s. It shrinks only after 5 minutes of lower recommendations, and then by at most 20 % per minute. For the video fleet, use a 10-minute window and 1 pod per 2 minutes (`deploy/helm/values-video.yaml`): a pod removed mid-transcode wastes the work and the client starts over.

KEDA's `cooldownPeriod` and `pollingInterval` only apply to scaling between 0 and 1 (KEDA 2.20 warns that the polling interval is "not relevant" otherwise). Between 1 and N, the HPA that KEDA creates reads the metric every HPA sync period (15 s by default) and its `behavior` above decides.

### 3.5 Readiness and drain

On SIGTERM the server:

1. turns `/readyz` to 503, so the pod leaves the Service endpoints;
2. keeps serving for `XI_SERVER_SHUTDOWN_DELAY` (5 s), while load balancers notice;
3. stops accepting new connections and drains in-flight requests for up to `XI_SERVER_SHUTDOWN_TIMEOUT` (30 s);
4. shuts libvips down.

`terminationGracePeriodSeconds` must exceed delay + timeout. The base uses 45 s, and the chart computes delay + timeout + 10 s (165 s for video). Raise the delay to 10–15 s behind cloud load balancers that deregister slowly, such as AWS ALB target groups. No `preStop` sleep is needed: the server's own delay does that job.

`XI_SERVER_READY_QUEUE_THRESHOLD` (off by default) makes `/readyz` answer 503 `busy` while the image processing queue is at least N % full: running plus waiting jobs over the `XI_PROCESSING_WORKERS` + `XI_PROCESSING_QUEUE_SIZE` places, the ones whose exhaustion means 429. It takes a saturated pod out of rotation until the queue drains below N %.

- It is evaluated on every probe, with no hysteresis: under a load that hovers around the threshold the pod flaps exactly as often as the probe sees it cross. Damp it with the probe's `failureThreshold` and `successThreshold`, or a high N (90–100: only a pod that is about to shed load leaves).
- It removes capacity exactly when the fleet is busiest, and pods that leave and rejoin the endpoints confuse some load balancers. Autoscaling on the queue (§3.1) is the better remedy; use the threshold to stop a load balancer from piling onto one hot pod.
- Video transformations have their own pool (§1.2) and do not count; a `XI_ROLE=video` pod never turns busy. Scale video on `xi_video_transform_queue_depth`.
- The admin listener's `/readyz` answers the same.

`XI_SERVER_MAX_CONNECTIONS` (default 4096) caps the open connections on the public listener; the admin listener is not counted, so probes and scrapes there still work. At the cap the server stops accepting and new clients wait in the kernel's accept backlog; they are not refused. Idle keep-alive connections hold their slot until `XI_SERVER_IDLE_TIMEOUT` (90 s), so behind a load balancer that pools connections the cap is about its pool size, not about requests.

### 3.6 Cold start

- The server becomes ready within a second or two of starting. It initialises libvips and probes FFmpeg encoders; there is no warm-up.
- The cost of a new replica is everything around that: scheduling, maybe a new node (minutes with a cluster autoscaler), and the image pull. The slim image is about 110 MB compressed; `full` and `gpl` are larger.
- Pre-pull the image on the nodes or keep spare node capacity if bursts must be absorbed within seconds.
- A new pod's in-memory caches are empty. Shared tiers (Redis, S3, origin stores) and the CDN keep the miss rate from jumping when the fleet grows.

### 3.7 How each platform expresses it

The concepts above map onto each platform's scaler. [DEPLOY.md](DEPLOY.md) has the concrete files.

| Platform | Signal available | Min / max | Notes |
|---|---|---|---|
| Kubernetes HPA | CPU (metrics-server). The queue through prometheus-adapter (`deploy/kubernetes/components/hpa-queue-metric`, chart `autoscaling.hpa.extraMetrics`) | `minReplicas` / `maxReplicas` | `behavior` as in §3.4 |
| Kubernetes KEDA | Prometheus queries: jobs per worker, shed rate, video utilisation, plus CPU (chart `autoscaling.type: keda`, `deploy/kubernetes/overlays/keda`) | `minReplicaCount` / `maxReplicaCount`; 0 allowed | `fallback` keeps a fixed size when Prometheus is unreachable |
| Google Cloud Run | Request concurrency and CPU | min / max instances | Set container concurrency to about 2–4 × workers, so Cloud Run adds instances well before our queue (workers × 16) is full. Keep CPU allocated outside requests, because result cache writes finish after the response. Use min instances ≥ 1 to avoid cold starts |
| Fly.io | Proxy concurrency (`soft_limit` / `hard_limit`, type `requests`); auto start and stop of existing Machines | `min_machines_running`; max = Machines you created | The proxy starts stopped Machines but does not create new ones. Set `soft_limit` near the per-Machine sustainable rate, or the metrics-based autoscaler for more |
| AWS ECS / Fargate | Application Auto Scaling target tracking on CPU, ALB requests per target, or a custom CloudWatch metric (queue ratio scraped by the CloudWatch agent or ADOT) | min / max capacity | Scale-in cooldown plays the role of the down window |
| Azure Container Apps | KEDA scale rules: HTTP concurrent requests, CPU, or custom KEDA scalers such as Prometheus | min / max replicas; 0 allowed | Same rules as KEDA above |
| Modal | Requests in flight per container (`@modal.concurrent` `target_inputs`, burst to `max_inputs`) | `min_containers` / `max_containers`; 0 allowed | `buffer_containers` keeps idle spares while active; `scaledown_window` is the down window. No `/readyz` probe and no metrics scrape: keep `max_inputs` below workers + queue |
| Docker Swarm, plain Compose, Dokploy, Coolify | none | fixed replicas | No autoscaler: size for peak by hand, or drive `docker service scale` from an external tool |

### 3.8 Verified: autoscaling in a local cluster

`deploy/scripts/kind-autoscale.sh` (`task deploy:autoscale`) does an end-to-end run:

1. creates a kind cluster;
2. installs KEDA, a minimal Prometheus and the chart with `deploy/scripts/kind/values-autoscale.yaml` (0.5 CPU pods, min 1, max 5, KEDA on jobs per worker and shed rate, 60 s down window);
3. drives AVIF resizes with k6, then idles;
4. prints the replica count, the trigger value and the shed rate every 10 s.

WP-148 run, 2026-10-23: kind 0.30.0 on the M1 Pro laptop, KEDA 2.20.2, image `xtandard-image:main-gpl`, pods limited to 0.5 CPU (`GOMAXPROCS` 2), 60 req/s of AVIF resizes (default effort 4, up to 1024 px) for 4 minutes, then idle:

| Time after load start | Replicas (total/ready) | Jobs per worker | Requests/s | Shed/s |
|---|---|---|---|---|
| 0 s | 1/1 | 0 | — | — |
| 11 s | 1/1 | 8.5 | 7.2 | 6.7 |
| 21 s | 5/1 (KEDA asked for the maximum in one step) | 11.3 | 17.9 | 16.5 |
| 32 s | 5/5 | 2.6 | 28.4 | 25.9 |
| 1 m 7 s to 4 m | 5/5 (at max) | 3.4 | 60 | 54.5 |
| load stopped, +68 s | 5/5 | 0 | 0 | 0 |
| +120 s | 2/2 | 0 | 0 | 0 |
| +151 s | 1/1 (min) | 0 | 0 | 0 |

So the fleet went from min to max within about 20 s of the burst and was all ready at 32 s. Once the queue had drained, it went back to min within about 1.5 minutes, as the 60 s window and 50 %/30 s policy of the test values allow (production values: 5 minutes, 20 %/min). The load was deliberately far above what five half-core pods can encode: AVIF at effort 4 cost about 0.7 s of CPU per image here. So at max replicas 94 % of the requests were shed (429, some 503 after the 5 s queue timeout; k6: 14,400 requests, p95 5.05 s). That is the `XtandardImageOverloaded` situation of §6.3. In production the remedy is a higher `maxReplicas` or a cheaper format setting (`XI_FORMATS_AVIF_EFFORT`).

## 4. A CDN in front

The server is built to sit behind a CDN. It sends long-lived, validator-backed cacheable responses and stays stateless. This section uses what the server really sends (ARCHITECTURE §9.1, `internal/server`). The vendor behaviour was checked against each vendor's documentation in October 2026; the links are inline. Vendors change their products, so re-check before relying on a detail.

### 4.1 What the origin sends

| Response | Headers |
|---|---|
| 200 image | `Cache-Control: public, max-age=<XI_CACHE_HEADERS_TTL>, stale-while-revalidate=<TTL/10>` (default `max-age=31536000, stale-while-revalidate=3153600`), plus `immutable` when the URL is signed and carries a cache buster or `hashsum`. Strong `ETag` (`"xi1-…"`), `Last-Modified` when the source has one. `Vary: Accept` **only when the output format was negotiated from `Accept`** (the URL did not fix it). Client-hint names in `Vary` and `Content-DPR` only when a client hint changed the result (`XI_CLIENT_HINTS_ENABLED`, default off, or a URL opt-in). `Cache-Tag` (`XI_CACHE_TAGS_HEADER`) when the URL has `cache_tags` |
| 304 | Same validators and caching headers, no body |
| 404 / 422 / 502 / 504 from the source or the image | `Cache-Control: public, max-age=<XI_CACHE_HEADERS_ERROR_TTL>` (10 s; `0` → `no-store`) |
| 400 / 403 / 406 / 429 / 503 / 500 | `no-store`. 429 and 503 carry `Retry-After` |
| HLS/DASH manifest (`.m3u8`, `.mpd`) at the dialect URL and under `/_stream/<ticket>/` | `public, max-age=` the smaller of `XI_CACHE_HEADERS_TTL` and a quarter of `XI_VIDEO_TRANSFORM_STREAM_TTL` (3 h by default), strong `ETag`, `Accept-Ranges: bytes` |
| HLS/DASH segments (`/_stream/<ticket>/v0/3.m4s`, `init.mp4`, …) | `public, max-age=` until the ticket expires (up to the stream TTL, 12 h), strong `ETag`, Range support |
| Video files (`/video/…@mp4`) | Image rules above, plus `Accept-Ranges` and `206` for `Range` requests |

What follows for any CDN:

- **The cache key must be path plus the full query string, in order.** Signatures live in the path (imgproxy, thumbor) or the query (`xi_sig`, imgix `s`, ImageKit `ik-s`). Never enable "ignore query string". Avoid query-string sorting, which can reorder a signed URL.
- **Pass signed URLs through unchanged.** The CDN never needs the signing key. A CDN URL rewrite that touches the path or query breaks the signature (403).
- **`Accept` is in the key only when the format is negotiated.** The best option is to fix the format in the URL (`@webp`, `f:avif`, `fm=avif`, `format=auto` resolved at your HTML layer). Then there is no `Vary` and one cache entry per URL. If you negotiate, normalise `Accept` at the edge to one of `image/avif`, `image/webp` or nothing. The server picks AVIF if accepted, else WebP, else the source-like format (`XI_FORMATS_AUTO_AVIF`/`_WEBP`; add `image/jxl` if you enable `XI_FORMATS_AUTO_JXL`). That caps a URL at three variants instead of one per browser `Accept` string.
- **Client hints.** If you enable them, every hint the server lists in `Vary` must be in the CDN cache key, or the CDN serves the wrong size. The `Accept-CH` response header must reach the browser. Leave them off unless you need them.
- **Error TTLs.** Short, cacheable 404/5xx responses (10 s) protect the origin from a stampede on a broken image. Never let the CDN cache 429 or 503: they are `no-store` on purpose, and a cached 429 would keep failing after the fleet has scaled.
- **Purging.** Responses are immutable per URL for a year. When a source changes under the same URL:
  - our result cache keeps serving the old rendition for `XI_CACHE_RESULT_TTL` (24 h);
  - the CDN keeps it for its TTL.
  Prefer versioned source paths or a `cachebuster`. To invalidate globally, change `XI_CACHE_ETAG_BUSTER` (which changes every result key and ETag) and purge the CDN, by tag (`cache_tags` → `Cache-Tag`) or by prefix.
- **HTTP `QUERY`** (RFC 10008, `/q/` endpoints) is not cached by any CDN: they cache GET and HEAD only. Some, CloudFront among them, do not accept the method at all; that comes from vendor documentation for CloudFront and an informal test for the others, not verified further. Use the equivalent GET URL (the `Location` header of a QUERY response) for anything cacheable.
- **Origin shield / tiered cache.** Turn it on. It collapses misses from many PoPs into one origin request, which is what our result and origin caches would otherwise absorb.
- **Range and video.** Serve video and segments through the CDN with Range support. The origin answers `206` from the cached file. Large video files may exceed a plan's per-object cache limit.

### 4.2 Cloudflare

- **Default caching is by file extension**, not content type. AVIF, WebP, JPEG, PNG, GIF, SVG, MP4 and WebM are cached by default. **Extensionless URLs and `.m3u8`, `.mpd`, `.m4s` are not**, so `/_stream/…` and most dialect URLs (`/insecure/rs:fit:300:300/plain/…` ends with `@webp` at best) need a Cache Rule with **Eligible for cache** ([default cache behavior](https://developers.cloudflare.com/cache/concepts/default-cache-behavior/)).
- Cloudflare **does not cache `no-store`/`private`** responses unless a rule overrides it. Its default TTLs apply only when the origin sends no `Cache-Control`, and 429/503 are not cached by default. A Cache Rule's **status-code TTL overrides the origin, including `no-store`**, so never give it a range that covers 429 or 503 ([status code TTL](https://developers.cloudflare.com/cache/how-to/configure-cache-status-code/)).
- **Vary: Accept.** There are two features:
  - **Vary in Cache Rules**, on every plan including Free since July 2026, can normalise `Accept` to a list of media types ([Vary](https://developers.cloudflare.com/cache/concepts/vary/), [changelog](https://developers.cloudflare.com/changelog/post/2026-07-02-vary-for-cache-rules/)). Use it.
  - The older **Vary for Images** (Pro and above) works only for URLs ending in an image extension and needs each variant listed per extension ([Vary for Images](https://developers.cloudflare.com/cache/advanced-configuration/vary-for-images/)).
  - What Cloudflare does with `Vary: Accept` and no rule is not documented. Always configure a Vary rule, or fix the format in the URL.
- **ETags.** Turn on **Respect strong ETags**, or Cloudflare weakens our strong ETag.
- **stale-while-revalidate.** Honoured asynchronously since February 2026. `s-maxage`, `must-revalidate` or `no-cache` disable it, and Always Online disables both it and stale-if-error ([revalidation](https://developers.cloudflare.com/cache/concepts/revalidation/)).
- **Range.** Served from cached files (GET only); **Origin Range Requests** fetches large files in 1 MiB chunks ([range requests](https://developers.cloudflare.com/cache/reference/range-requests/)). The per-file cache limit is 512 MB, or 5 GB on Enterprise.
- **Tiered Cache** (Smart topology) is on every plan ([tiered cache](https://developers.cloudflare.com/cache/how-to/tiered-cache/)). Cache Reserve is a paid add-on.
- **Purge** by URL, tag (`Cache-Tag`, which is our default header name), prefix and hostname on every plan since April 2025 ([purge for all](https://developers.cloudflare.com/changelog/post/2025-04-01-purge-for-all/)).
- **Custom cache keys** with headers are Enterprise only. The default key already includes the full query string.

Terraform (provider v5) cache rules for an image host, with `Accept` normalised. Syntax from the [Terraform example](https://developers.cloudflare.com/cache/how-to/cache-rules/terraform-example/) and the [Cache Rules settings](https://developers.cloudflare.com/cache/how-to/cache-rules/settings/):

```hcl
resource "cloudflare_ruleset" "xtandard_image_cache" {
  zone_id = var.zone_id
  name    = "xtandard-image cache"
  kind    = "zone"
  phase   = "http_request_cache_settings"
  rules = [
    {
      ref         = "images"
      description = "Images: cache extensionless URLs, respect origin TTLs, normalise Accept"
      expression  = "(http.host eq \"img.example.com\" and not starts_with(http.request.uri.path, \"/_stream/\"))"
      action      = "set_cache_settings"
      action_parameters = {
        cache                      = true
        edge_ttl                   = { mode = "respect_origin" }
        browser_ttl                = { mode = "respect_origin" }
        respect_strong_etags       = true
        origin_error_page_passthru = true
        vary = {
          # Responses without Vary are cached normally; a Vary on any other
          # header (client hints) bypasses: list those as "passthrough" if you use them.
          default = { action = "bypass" }
          headers = {
            "accept" = { action = "normalize", media_types = ["image/avif", "image/webp"] }
          }
        }
      }
    },
    {
      ref         = "streams"
      description = "HLS/DASH parts: the origin sets short manifest and long segment TTLs"
      expression  = "(http.host eq \"img.example.com\" and starts_with(http.request.uri.path, \"/_stream/\"))"
      action      = "set_cache_settings"
      action_parameters = {
        cache                 = true
        edge_ttl              = { mode = "respect_origin" }
        respect_strong_etags  = true
        origin_range_requests = { mode = "on" }
      }
    }
  ]
}
```

In the dashboard this is **Caching → Cache Rules**: *Eligible for cache*, Edge TTL *Use cache-control header if present*, Browser TTL *Respect origin*, *Respect strong ETags* on, and **Vary** with `accept` set to *Normalize* (`image/avif`, `image/webp`).

### 4.3 Amazon CloudFront

- **CloudFront ignores the origin's `Vary` when it builds the cache key.** The key is only what the cache policy lists. Without a policy that forwards it, CloudFront also removes `Accept` from the request it sends to the origin ([request and response behavior](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/RequestAndResponseBehaviorCustomOrigin.html)). So with format negotiation, `Accept` (normalised) **must be in the cache policy**, and so must any client-hint header you enable.
- **None of the managed cache policies fits.** `CachingOptimized` drops the query string and `Accept`. A `MinTTL` above 0 overrides `no-store`, `no-cache` and `private` ([managed cache policies](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/using-managed-cache-policies.html), [expiration](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/Expiration.html)). Use a custom policy with `MinTTL: 0`.
- **stale-while-revalidate and stale-if-error are honoured** (same expiration page).
- **Error caching.** CloudFront caches 404 and 500/502/503/504 for `ErrorCachingMinTTL`, default 10 s. 429 is not cached ([status codes](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/HTTPStatusCodes.html)). The docs do not say clearly whether our `no-store` on a 503 avoids that 10 s floor. Add custom error responses for 500 and 503 with `ErrorCachingMinTTL: 0`.
- **Range requests** are forwarded and cached ([range GETs](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/RangeGETs.html)).
- **Methods.** Allowed methods are fixed sets, and only GET and HEAD (optionally OPTIONS) are cached, so HTTP `QUERY` is not available through CloudFront.
- **Purging.** Invalidations: the first 1,000 paths per month are free, and a `*` wildcard counts as one ([pricing](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/PayingForInvalidation.html)). Tag invalidation reads a response header you name (`CacheTagConfig.HeaderName`, so set it to `Cache-Tag`) and invalidates with `#tag` ([invalidation by tags](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/invalidation-by-tags.html)).
- **Origin Shield**: enable it in the origin settings, in the region closest to the fleet ([Origin Shield](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/origin-shield.html)).

Cache policy (`aws cloudfront create-cache-policy --cache-policy-config file://xi-cache-policy.json`, fields per [CachePolicyConfig](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_CachePolicyConfig.html)):

```json
{
  "Name": "xtandard-image",
  "Comment": "Origin TTLs; key = path + all query strings + normalised Accept",
  "MinTTL": 0,
  "DefaultTTL": 86400,
  "MaxTTL": 31536000,
  "ParametersInCacheKeyAndForwardedToOrigin": {
    "EnableAcceptEncodingGzip": false,
    "EnableAcceptEncodingBrotli": false,
    "HeadersConfig": { "HeaderBehavior": "whitelist", "Headers": { "Quantity": 1, "Items": ["Accept"] } },
    "CookiesConfig": { "CookieBehavior": "none" },
    "QueryStringsConfig": { "QueryStringBehavior": "all" }
  }
}
```

Headers and query strings in the cache policy are forwarded to the origin as well. An origin request policy is needed only for headers the server reads but does not vary on, and **anything the server varies on must be in the cache policy, not only the origin request policy**. If you use one, keep it minimal. The `HeaderBehavior` values other than `whitelist` were not re-checked for this guide.

```json
{
  "Name": "xtandard-image-origin",
  "HeadersConfig": { "HeaderBehavior": "none" },
  "CookiesConfig": { "CookieBehavior": "none" },
  "QueryStringsConfig": { "QueryStringBehavior": "all" }
}
```

`Accept` normalisation as a CloudFront Function (`cloudfront-js-2.0`, viewer request, attached to the image behaviour only, not `/_stream/*`). It follows the idea of AWS's own [image-optimization sample](https://github.com/aws-samples/image-optimization):

```js
function handler(event) {
  var req = event.request;
  var a = req.headers.accept ? req.headers.accept.value : "";
  if (a.indexOf("image/avif") !== -1) req.headers.accept = { value: "image/avif" };
  else if (a.indexOf("image/webp") !== -1) req.headers.accept = { value: "image/webp" };
  else delete req.headers.accept;
  return req;
}
```

For the `/_stream/*` behaviour, use a second cache policy without `Accept`, with gzip and Brotli enabled (manifests compress well).

### 4.4 Fastly

- Fastly **honours `Vary`** as the spec says and does not normalise `Accept` itself. Its docs warn that `Vary: Accept` fragments the cache and recommend normalising in VCL ([Vary](https://www.fastly.com/documentation/reference/http/http-headers/Vary/), [Accept](https://www.fastly.com/documentation/reference/http/http-headers/Accept/), [best practices](https://www.fastly.com/blog/best-practices-for-using-the-vary-header)).
- TTL precedence is `Surrogate-Control` > `s-maxage` > `max-age` > `Expires`.
- 200, 203, 300, 301, 302, 404 and 410 are cacheable by default; 429 and 5xx are not.
- `stale-while-revalidate` and `stale-if-error` are supported ([cache freshness](https://www.fastly.com/documentation/guides/concepts/edge-state/cache/cache-freshness/)).
- **Purge by surrogate key** uses the `Surrogate-Key` response header, **space-separated**, so translate our comma-separated `Cache-Tag`. Soft purge (`Fastly-Soft-Purge: 1`) marks objects stale instead of removing them ([purging](https://www.fastly.com/documentation/reference/api/purging/), [surrogate keys](https://www.fastly.com/documentation/guides/full-site-delivery/purging/working-with-surrogate-keys/)).
- Enable shielding on the origin (not re-checked for this guide).

VCL snippets:

```vcl
# vcl_recv: normalise Accept for images (not for HLS/DASH parts)
if (req.url.path !~ "^/_stream/") {
  if (req.http.Accept ~ "image/avif") {
    set req.http.Accept = "image/avif";
  } elsif (req.http.Accept ~ "image/webp") {
    set req.http.Accept = "image/webp";
  } else {
    unset req.http.Accept;
  }
}
```

```vcl
# vcl_fetch: Cache-Tag "a,b" -> Surrogate-Key "a b"
if (beresp.http.Cache-Tag) {
  set beresp.http.Surrogate-Key = regsuball(beresp.http.Cache-Tag, ",\s*", " ");
}
```

A regex is used instead of `accept.media_lookup` because the server's order of preference (AVIF, then WebP) should win over the client's q-values.

### 4.5 Bunny CDN

- **Bunny does not follow the origin's `Vary`.** It varies only on what the pull zone's **Vary Cache** settings list: WebP support, AVIF support, URL query string, country, hostname, mobile/desktop, cookie and request headers ([Vary Cache](https://bunny.net/docs/cdn/vary-cache)).
- **The default cache key ignores query strings.** That breaks query-signed URLs and query dialects, so turn on **Vary Cache → URL Query String** (API `IgnoreQueryStrings: false`).
- For format negotiation, enable **WebP** and **AVIF** vary (`EnableWebpVary`, `EnableAvifVary`). How Bunny detects support was not verified; test that its variants line up with what the server picks. Fixing the format in the URL avoids the question.
- **Cache expiration time: Respect origin.** Enable *Use stale while updating* and *Use stale while offline* (`UseStaleWhileUpdating`, `UseStaleWhileOffline`) and **Origin Shield** (`EnableOriginShield`) ([pull zone API](https://bunny.net/docs/api-reference/core/pull-zone/update-pull-zone)).
- With **Smart Cache**, Bunny caches only known extensions. Extensionless image URLs and HLS/DASH content types may need an Edge Rule with *Override Cache Time* ([Smart Cache](https://bunny.net/docs/cdn/smart-cache)). Whether it caches `application/vnd.apple.mpegurl` and `application/dash+xml` by itself was not verified.
- **Tag purge** reads the `CDN-Tag` response header: set `XI_CACHE_TAGS_HEADER=CDN-Tag` ([purge](https://bunny.net/docs/cdn/purge-cache)). A `*` in a purge URL purges by prefix.
- For video, *Optimize for video* (`EnableCacheSlice`) caches large files in slices.

### 4.6 Akamai and others

- **Akamai** by default **does not cache responses that carry `Vary`**, other than `Vary: Accept-Encoding` on compressed responses ([caching](https://techdocs.akamai.com/property-mgr/docs/caching-2), [Remove Vary Header](https://techdocs.akamai.com/property-mgr/docs/rm-vary-header)).
  - Fix the output format in the URL, or remove `Vary` at the edge and put `Accept` in the cache key with **Cache ID Modification** (include the request header).
  - Only original client headers can be part of the key, so a normalised `Accept` needs EdgeWorkers or variables (not detailed here) ([Cache ID Modification](https://techdocs.akamai.com/property-mgr/docs/cache-id-modification)).
  - Tags for Fast Purge use the `Edge-Cache-Tag` header: set `XI_CACHE_TAGS_HEADER=Edge-Cache-Tag` ([cache tags](https://techdocs.akamai.com/purge-cache/docs/assign-cache-tags)).
- **Any other CDN** (Google Cloud CDN, Azure Front Door, KeyCDN, Gcore, nginx or Varnish of your own). Check five things:
  1. whether it keys on the full query string;
  2. whether it honours `Vary: Accept`, and if so whether it can normalise it;
  3. that it does not cache `no-store` (our 429/503);
  4. that it respects `max-age` and the strong `ETag`;
  5. which header its tag purge reads (point `XI_CACHE_TAGS_HEADER` at it).

  When in doubt, fix the format in the URL.

## 5. Result cache tiers

The result cache stores finished renditions (`XI_CACHE_RESULT_STORES`, ordered fastest first; ARCHITECTURE §9.2). A hit skips the fetch, admission and processing. The origin cache keeps source bytes (`XI_CACHE_ORIGIN_*`); it is per process by default and can be backed by shared stores with `XI_CACHE_ORIGIN_STORES`.

| Tier | Scope | Good for | Limits |
|---|---|---|---|
| `memory` | one pod | the hottest renditions, sub-millisecond hits | `XI_CACHE_RESULT_MEMORY_BYTES` (256 MiB) comes out of the pod's memory limit; lost on restart and scale-down; skips spooled video |
| `disk` | one pod (one directory per process) | a larger per-pod tier | lost with an `emptyDir`; a PersistentVolume needs a StatefulSet; size `XI_CACHE_RESULT_DISK_BYTES` |
| `redis` | the fleet | sharing hits across replicas and new pods | values over `XI_CACHE_RESULT_REDIS_MAX_VALUE_BYTES` (1 MiB) are skipped (`xi_cache_skipped_total{reason="too_large"}`); memory-priced |
| `s3` | the fleet | the long tail, video results, surviving everything | slower (tens of ms); per-request S3 pricing; add a lifecycle rule on the prefix to reclaim space |

Which tiers to use:

- **Behind a CDN** the CDN absorbs repeats. Our cache serves the CDN's misses: new PoPs, shield misses, objects evicted from the edge, and re-requests after a purge. For one replica, `memory` alone is often enough. For a fleet, use `memory,s3` or `memory,redis`, so a miss on one pod is a hit for all and a new pod is useful immediately. Hit ratios behind a CDN are naturally low (`job:xi_cache_hit:ratio_rate5m`); judge the cache by the origin bytes and processing it saves.
- **Without a CDN** the result cache is the cache: `memory,redis` for small images, `memory,disk` for one or a few pods, and `memory,redis,s3` for a fleet with a long tail.
- **Video**: `disk` or `s3`, never only `memory`. A video file evicts thousands of images.
- **Originals**: with many replicas and a slow or metered origin, set `XI_CACHE_ORIGIN_STORES=redis` or `s3`, so each source is downloaded once per fleet rather than once per pod.

Writes are asynchronous (`XI_CACHE_RESULT_WRITE_QUEUE`). When a slow tier cannot keep up, writes are dropped and counted in `xi_cache_writes_dropped_total`; the alert `XtandardImageCacheWritesDropped` warns.

## 6. Observability

### 6.1 Metrics

`/metrics` is served on the **admin listener** only. It defaults to `127.0.0.1:9090`; set `XI_ADMIN_ADDR=:9090` so Prometheus can reach it (the chart and the base do). Scrape port `admin`, never the public port:

- **prometheus-operator**: the chart's `serviceMonitor.enabled`, or `deploy/kubernetes/components/monitoring/servicemonitor.yaml`.
- **Plain Prometheus**: `deploy/observability/prometheus/prometheus.yml` (static) or Kubernetes endpoint discovery keeping the `admin` port (`deploy/scripts/kind/prometheus.yaml`).

The metrics that matter most (the full list is in ARCHITECTURE §13):

| Metric | Meaning |
|---|---|
| `xi_requests_total{dialect,status,format}`, `xi_request_duration_seconds` | Processing requests (images, video, `/info`; not health checks) and their end-to-end latency |
| `xi_requests_in_flight` | Requests being served, cache hits included |
| `xi_queue_depth`, `xi_workers_busy`, `xi_semaphore_wait_seconds` | Admission: jobs holding a queue permit, running jobs, wait for a worker |
| `xi_overload_total{reason}` | Shed requests: `queue_full` (429), `wait_timeout` and `memory_budget` (503) |
| `xi_stage_duration_seconds{stage}` | Where the time goes, per lifecycle stage |
| `xi_source_fetch_duration_seconds{source,type}`, `xi_source_bytes_total{type}`, `xi_fetch_coalesced_total` | Origin latency, origin bytes, deduplicated fetches |
| `xi_cache_requests_total{store,result}`, `xi_cache_bytes`, `xi_cache_entries`, `xi_cache_evictions_total`, `xi_cache_writes_dropped_total`, `xi_cache_skipped_total` | Result tiers (`memory`, `disk`, `redis`, `s3`), the origin cache (`origin`) and the stream cache (`stream`) |
| `xi_vips_mem_bytes`, `xi_vips_mem_highwater_bytes`, `xi_vips_allocs` | libvips memory |
| `xi_video_transform_*` | Video pool: duration by result, bytes, queue depth, workers busy, cost capacity, used and queued cost, rejections by reason |
| `xi_video_spool_bytes`, `xi_video_spool_files`, `xi_video_spool_spills_total`, `xi_video_spool_rejections_total` | Spool usage |
| `xi_panics_total`, `xi_build_info{version,commit,goversion}` | Recovered panics; build |
| `go_sched_gomaxprocs_threads`, `go_gc_gomemlimit_bytes`, `process_resident_memory_bytes`, `process_cpu_seconds_total`, `process_start_time_seconds` | Go runtime and process: worker count (default), Go memory limit, RSS, CPU, restarts |

### 6.2 Dashboard

[`deploy/observability/grafana/dashboards/xtandard-image.json`](../deploy/observability/grafana/dashboards/xtandard-image.json) (uid `xtandard-image`). It has variables for datasource, job and instance, and these rows:

- **Overview**: requests/s, SLO error ratio, p99, instances, result cache hit ratio, shed/s.
- **Traffic**: by status, dialect and format; in-flight.
- **Latency**: percentiles, p95 by dialect and by stage, worker wait.
- **Capacity and admission**: queue depth against workers, workers busy ratio, shed by reason, CPU.
- **Caches**: hit ratio, size, evictions, dropped writes, coalesced fetches.
- **Sources**: fetch latency, origin bytes/s.
- **Memory**: RSS against the limit, libvips, Go heap, goroutines.
- **Video transformations**: transforms by result, duration, pool, cost units, rejections, bytes, spool.

Import it in Grafana, or let the Grafana sidecar load it: chart `grafanaDashboard.enabled`, or the `monitoring` component, label `grafana_dashboard: "1"`.

### 6.3 Alerts and SLOs

[`deploy/observability/prometheus/rules.yml`](../deploy/observability/prometheus/rules.yml) is the single source of the recording rules, the SLOs and the alerts. `deploy/scripts/gen.sh` copies it into the chart (`prometheusRule.enabled`) and into the Kustomize `monitoring` component, and `check.sh` fails if a copy drifts. Its unit tests are [`rules.test.yml`](../deploy/observability/prometheus/rules.test.yml) (`promtool test rules`). Everything is aggregated by `(namespace, job)`, so each fleet or release gets its own SLO.

SLOs (edit the objectives in the file):

- **Availability, 99.9 %**: a processing request counts as bad when it is a 5xx other than 502, or a 429. A 502 means the origin failed, which is not our availability, though `XtandardImageUpstreamErrors` reports it. Recording rules: `job:xi_slo_errors:ratio_rate{5m,30m,1h,6h}`. Alerts use multi-window burn rates (Google SRE workbook):
  - `XtandardImageAvailabilityBurnFast` (critical): 14.4× the budget over 1 h and 5 m;
  - `XtandardImageAvailabilityBurnSlow` (warning): 6× over 6 h and 30 m.
- **Latency, 99 % of successful image requests within 1 s**, excluding video formats and HLS/DASH parts. Recording rules: `job:xi_slo_slow:ratio_rate{5m,1h}`. Alert: `XtandardImageLatencyBurn`.

Other alerts:

| Alert | Fires when |
|---|---|
| `XtandardImageTargetDown` | the admin listener cannot be scraped (job name containing `xtandard-image`) |
| `XtandardImageOverloaded` | more than 5 % of requests are shed for 10 minutes: max replicas too low or scaling too slow |
| `XtandardImageQueueSaturated` | over 1.5 jobs per worker for 10 minutes |
| `XtandardImagePanics` | a handler panic was recovered |
| `XtandardImageRestarting` | more than 2 restarts in 30 minutes (OOM kill or native crash) |
| `XtandardImageMemoryNearLimit` | RSS above 90 % of the limit, derived as `GOMEMLIMIT / 0.3` (the server sets `GOMEMLIMIT` from the limit, §1.3) |
| `XtandardImageUpstreamErrors` | more than 5 % 502/504 for 10 minutes |
| `XtandardImageCacheWritesDropped` | the result cache write queue overflows |
| `XtandardImageVideoOverloaded`, `XtandardImageVideoFailures`, `XtandardImageVideoSpoolFull` | the video pool rejects jobs, more than 10 % of transforms fail, or the spool is full |

Recording rules for dashboards and capacity planning: `job:xi_requests:rate5m`, `job:xi_request_duration_seconds:p{50,95,99}_5m`, `job:xi_queue_utilisation:ratio`, `job:xi_workers_busy:ratio`, `job:xi_overload:rate5m`, `job:xi_cache_hit:ratio_rate5m`, `job:xi_video_transform_utilisation:ratio`.

### 6.4 Tracing (OpenTelemetry)

Tracing is on when `OTEL_EXPORTER_OTLP_ENDPOINT` (or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`) is set, or when `XI_OTEL_ENABLED=true`. The exporter comes from the standard `OTEL_*` variables (`autoexport`: OTLP over HTTP/protobuf by default, `OTEL_EXPORTER_OTLP_PROTOCOL=grpc` for gRPC). `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES` set the resource. Spans cover the request and its stages (`GET`, `fetch`, `transcode`, …), with W3C trace-context propagation; propagation to sources is off.

**Sampling happens in the server's code: parent-based, sampling every root trace.** `OTEL_TRACES_SAMPLER` has no effect, because the sampler is set in code and options override the environment in the Go SDK. Sample in the Collector. The provided config keeps every error and every trace over 1 s, plus 10 % of the rest (`tail_sampling`).

Send traces to an **OpenTelemetry Collector** (chart `otel.endpoint`, e.g. `http://otel-collector.monitoring.svc:4318`), not straight to a vendor. The Collector batches, samples, retries and holds the credentials. [`deploy/observability/otel-collector/config.yaml`](../deploy/observability/otel-collector/config.yaml) exports to Tempo. Exporters for other backends, to put in that file's `exporters` and `service.pipelines.traces.exporters` (credentials from environment variables; these are the contrib Collector's standard exporters, check its docs for your version):

```yaml
exporters:
  # Tempo or Jaeger v2 (both accept OTLP)
  otlp/tempo:
    endpoint: tempo.monitoring.svc:4317
    tls: { insecure: true }
  otlp/jaeger:
    endpoint: jaeger-collector.monitoring.svc:4317
    tls: { insecure: true }
  # Honeycomb
  otlp/honeycomb:
    endpoint: api.honeycomb.io:443
    headers: { x-honeycomb-team: ${env:HONEYCOMB_API_KEY} }
  # Grafana Cloud (OTLP gateway of your stack's region; instance id and token)
  otlphttp/grafanacloud:
    endpoint: https://otlp-gateway-prod-eu-west-2.grafana.net/otlp
    auth: { authenticator: basicauth/grafanacloud }
  # Datadog (contrib datadog exporter; or send OTLP to a Datadog Agent with OTLP ingest on)
  datadog:
    api: { key: ${env:DD_API_KEY}, site: datadoghq.com }

extensions:
  basicauth/grafanacloud:
    client_auth: { username: ${env:GRAFANA_CLOUD_INSTANCE_ID}, password: ${env:GRAFANA_CLOUD_API_TOKEN} }
```

The access log carries `trace_id` and `span_id`, so logs and traces join on them.

### 6.5 Logs

The server writes JSON lines (`log/slog`) to stdout. Configure the level with `XI_OBSERVABILITY_LOG_LEVEL` (`debug`, `info`, `warn`, `error`). Every request produces one `"msg":"request served"` line with these fields:

- `method`, `path` (source query strings stripped, passwords redacted; signatures never logged);
- `status`, `bytes`;
- `duration` (nanoseconds);
- `dialect`, `source`, `cache` (`HIT`/`MISS`/`BYPASS`), `format`, `width`, `height`;
- `stages` (per-stage durations);
- `request_id` (also the `X-Request-Id` response header), `trace_id`, `span_id`.

Failures add a `"msg":"request failed"` line with `error`.

Ship them with the node's log agent:

- **Grafana Alloy** to Loki (Promtail is deprecated in favour of Alloy). Example LogQL: `{namespace="xi"} | json | status >= 500`, or `| json | cache="MISS" | line_format "{{.path}}"`.
- **Vector** or **Fluent Bit** to any backend. Parse the line as JSON, keep `request_id`, `trace_id`, `status`, `dialect`, `cache`, `duration`, and drop `/healthz` and `/readyz` lines at the agent if you want less volume.

Health-check lines are logged like any request, without the image fields.

### 6.6 Local stack

[`deploy/observability/compose.yml`](../deploy/observability/compose.yml) runs the server (fs source over `testdata/fixtures`, tracing on), Prometheus with the rules, Grafana with the dashboard provisioned, the Collector and Tempo. Host ports start with 1 so they do not collide with a server on 8080/9090:

```sh
XI_IMAGE=xtandard-image:main-gpl task deploy:stack:up    # or docker compose -f deploy/observability/compose.yml up -d
task deploy:stack:load                                   # k6, 20 req/s for 2 minutes (RATE, DURATION, VIDEO=1)
open http://localhost:13000                              # Grafana: dashboard "xtandard-image"; Explore → Tempo
open http://localhost:19190                              # Prometheus: targets, rules, alerts
task deploy:stack:down
```

It runs with development settings (unsigned URLs allowed, anonymous Grafana admin). Never expose it.

### 6.7 pprof

`XI_ADMIN_PPROF=true` serves `/debug/pprof/*` (including `goroutineleak`) on the admin listener. Profiles expose memory contents (heap dumps), source names, and CPU cost while a profile runs. Turn it on only when needed, and reach it with `kubectl port-forward` to port 9090, never through a Service that leaves the cluster. Profile a pod under real load: `go tool pprof http://localhost:9090/debug/pprof/profile?seconds=30`.

## 7. Security notes for operators

- **The admin listener is never public.** It serves metrics, pprof, `/openapi.json`, the config schema and the playground. Keep `XI_ADMIN_ADDR` on `127.0.0.1` unless something scrapes it. Never route the admin port from an Ingress or a public load balancer. The NetworkPolicy in the chart and the base allows it only from the `monitoring` namespace (`networkPolicy.metricsFrom`).
- **The playground stays off** in production (`XI_PLAYGROUND_ENABLED=false`, `XI_PLAYGROUND_PUBLIC=false`). Whoever reaches it can sign URLs with your keys ([CONFIG.md, Playground](CONFIG.md#playground)).
- **Signing keys are secrets.** Put `XI_SIGNING_KEYS`, `XI_SIGNING_SALTS`, `XI_VIDEO_TRANSFORM_STREAM_KEYS`, source credentials and `XI_CACHE_RESULT_REDIS_URL` in a Secret (`existingSecret`, `_FILE` variables, `{file: …}` references, or External Secrets). Never put them in the values file, a ConfigMap or the image. The examples in `deploy/` contain placeholders only. Rotate keys by listing the new one first (every key verifies, the first signs).
- **Keep signing on.** `XI_SECURITY_UNSIGNED_POLICY=allow` lets anyone generate any transformation: a cost and denial-of-service problem that autoscaling turns into a bill. The default `restricted` policy allows only listed widths, presets and formats.
- **Read-only root file system, non-root, no capabilities.** The chart and the base run as UID 65532, with `readOnlyRootFilesystem`, `allowPrivilegeEscalation: false`, all capabilities dropped, the `RuntimeDefault` seccomp profile and no service-account token. Writable paths are explicit `emptyDir` volumes: `/tmp` (RAM, 64 MiB), the spool, the disk cache.
- **Egress.** The server's SSRF rules refuse private and link-local addresses for http sources (`XI_FETCH_ALLOW_PRIVATE_NETWORKS=false`). The NetworkPolicy adds a second line: egress only to DNS and ports 80/443 (plus OTLP), never to the cloud metadata address `169.254.169.254`. Add rules for in-cluster S3 or Redis (`networkPolicy.egress.extraRules`).
- **Service links off.** Kubernetes injects `<SERVICE>_SERVICE_HOST`/`_PORT_…` variables for every Service in the namespace. A Service named `xi-…` (a Helm release called `xi`, for example) produces `XI_*` names, and the server refuses unknown `XI_*` variables at startup (`unknown environment variable XI_XTANDARD_IMAGE_SERVICE_HOST`). The chart and the base set `enableServiceLinks: false`; do the same in your own manifests (or `XI_ALLOW_UNKNOWN_ENV=true`, which also hides typos).
- **Origin credentials.** Prefer workload identity (IRSA, GKE Workload Identity, Azure managed identity) through the ServiceAccount annotations over static keys.

## 8. Verification of the deploy files

`task deploy:check` (`sh deploy/scripts/check.sh`) runs every static check from pinned container images (Helm 3.19.0, kubeconform 0.7.0 with the datreeio CRD catalog for KEDA and prometheus-operator, kustomize 5.7.1, promtool from Prometheus 3.7.3, jq 1.8.1):

- the generated copies of the rules and dashboard are up to date;
- the dashboard and schema are valid JSON;
- every metric name in the rules, dashboard, KEDA queries and this guide exists in `internal/obs/metrics` (or the Go and process collectors), and every `XI_*` name in `deploy/` and this guide is in CONFIG.md;
- `promtool check rules`, `promtool test rules`, `promtool check config`;
- `kustomize build` of every overlay through `kubeconform -strict`;
- `helm lint --strict` and `helm template` with six value sets (`ci/*.yaml`, the two example fleets, the kind values) through `kubeconform -strict`, plus refusal of bad values: no sources, the video role on `slim`, an unknown role, min above max, and a schema violation;
- `docker compose config` of the local stack.

`task deploy:autoscale` runs the kind check of §3.8 (results there). The local stack of §6.6 was run with `xtandard-image:main-gpl`. Prometheus scraped both targets and evaluated all 30 rules, Grafana provisioned the dashboard and returned data through its own query API, and Tempo received the server's traces (`GET`, `fetch` and `transcode` spans).

The chart refuses to render without sources (`config.XI_SOURCES`, a `configFile`, or an `existingSecret` that holds them), because the server does not start without one. Helm merges `config` with the defaults, so the defaults hold no sources: an unused `XI_SOURCES_<NAME>_*` key also stops startup.
