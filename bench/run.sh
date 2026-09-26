#!/bin/sh
# Benchmarks xtandard-image against imgproxy OSS (docs/BENCHMARKS.md).
#
# Starts both servers from bench/compose.yml next to a static origin; only one
# is under load at a time. For every case: warm both up, then at each fixed
# arrival rate and once closed-loop (throughput ceiling) measure each server
# back to back, BENCH_REPEAT times, sampling the loaded container's CPU and
# memory with `docker stats`. Interleaving keeps slow drift in background load
# on the host from favouring one server. Writes
#   bench/out/<server>-<case>-<mode>-<point>-r<n>.json  k6 summary per run
#   bench/out/results.jsonl       every run (k6 + docker stats + host load)
#   bench/out/report.md           metadata and median tables for RESULTS.md
#
# Usage: bench/run.sh
#   XI_IMAGE        xtandard-image image (default xtandard-image:dev; build it
#                   first: task release:build)
#   IMGPROXY_IMAGE  default darthsim/imgproxy:latest
#   BENCH_WORKERS   workers for both servers (default: CPUs Docker sees)
#   BENCH_SERVERS   default "xtandard imgproxy"
#   BENCH_CASES     subset of the case names below (default: all)
#   BENCH_DURATION  per measured run (default 15s)
#   BENCH_REPEAT    runs per server and point; tables show the median (default 3)
#   BENCH_WARMUP    per case and server warm-up (default 5s)
#   BENCH_MAX_VUS   closed-loop concurrency (default 2 x workers)
#   BENCH_MODES     "rate max" (default) or a subset
#   XI_BASE_IMAGE   builder image for fixture generation (compose.dev.yml)
# Needs docker (compose), jq and awk on the host.
set -eu
cd "$(dirname "$0")"

export XI_IMAGE="${XI_IMAGE:-xtandard-image:dev}"
export IMGPROXY_IMAGE="${IMGPROXY_IMAGE:-darthsim/imgproxy:latest}"
export BENCH_WORKERS="${BENCH_WORKERS:-$(docker info -f '{{.NCPU}}')}"
servers="${BENCH_SERVERS:-xtandard imgproxy}"
duration="${BENCH_DURATION:-15s}"
repeat="${BENCH_REPEAT:-3}"
warmup="${BENCH_WARMUP:-5s}"
max_vus="${BENCH_MAX_VUS:-$((BENCH_WORKERS * 2))}"
modes="${BENCH_MODES:-rate max}"

# case name, then the fixed arrival rates (req/s). The rates are the same for
# both servers: roughly 25 %, 50 % and 90 % of the lower of the two
# throughput ceilings measured on the reference machine (bench/RESULTS.md);
# scale them for other hardware. info runs against xtandard-image only
# (/info is an imgproxy Pro feature; OSS answers 404).
case_table='
jpeg-400-jpeg 50 125 250
jpeg-400-webp 50 100 190
jpeg-400-avif 50 100 180
png-alpha-400-webp 15 30 60
gif-anim-240-webp 5 10 18
jpeg-1200-fill-jpeg 15 35 65
info 500 1000 2500
'
cases="${BENCH_CASES:-$(echo "$case_table" | awk 'NF { print $1 }')}"

compose() { docker compose -f compose.yml "$@"; }

# ---- fixtures --------------------------------------------------------------
if [ ! -s fixtures/photo.jpg ] || [ ! -s fixtures/alpha.png ] || [ ! -s fixtures/anim.gif ]; then
  echo "bench: generating fixtures"
  (cd .. && docker compose -f compose.dev.yml run --rm -T dev sh bench/fixtures.sh)
fi

mkdir -p out
rm -f out/*.json out/results.jsonl out/stats-*.txt out/idle-memory.txt

# ---- metadata --------------------------------------------------------------
docker image inspect "$XI_IMAGE" >/dev/null
docker image inspect "$IMGPROXY_IMAGE" >/dev/null 2>&1 || docker pull -q "$IMGPROXY_IMAGE" >/dev/null
compose --profile tools pull -q k6 fixtures >/dev/null 2>&1 || true

digest() { docker image inspect -f '{{if .RepoDigests}}{{index .RepoDigests 0}}{{else}}{{.Id}}{{end}}' "$1"; }
created() { docker image inspect -f '{{.Created}}' "$1"; }
if [ "$(uname -s)" = Darwin ]; then
  cpu="$(sysctl -n machdep.cpu.brand_string), $(sysctl -n hw.physicalcpu) cores ($(sysctl -n hw.perflevel0.physicalcpu 2>/dev/null || echo ?)P + $(sysctl -n hw.perflevel1.physicalcpu 2>/dev/null || echo ?)E), $(($(sysctl -n hw.memsize) / 1073741824)) GiB"
  os="macOS $(sw_vers -productVersion)"
else
  cpu="$(awk -F': ' '/model name/ { print $2; exit }' /proc/cpuinfo), $(nproc) CPUs"
  os="$(uname -sr)"
fi
docker_info="$(docker info -f '{{.OperatingSystem}} {{.ServerVersion}}, {{.NCPU}} CPUs, {{.MemTotal}} bytes, kernel {{.KernelVersion}}, {{.Architecture}}')"
xi_version="$(docker run --rm "$XI_IMAGE" version | head -1)"
ip_version="$(docker run --rm --entrypoint imgproxy "$IMGPROXY_IMAGE" version 2>/dev/null | head -1 || echo unknown)"
k6_version="$(compose --profile tools run --rm -T k6 version 2>/dev/null | head -1)"
started="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# ---- helpers -----------------------------------------------------------------
# sampler <cid> <file>: one line per sample, "<cpu%> <mem bytes>", while
# <file>.run exists. docker stats --no-stream takes about a second per call,
# which is the sampling interval.
sampler() {
  while [ -f "$2.run" ]; do
    docker stats --no-stream --format '{{.CPUPerc}} {{.MemUsage}}' "$1" 2>/dev/null |
      awk '{
        cpu = $1; sub(/%/, "", cpu)
        mem = $2; unit = mem; sub(/[A-Za-z]+$/, "", mem); sub(/^[0-9.]+/, "", unit)
        f = 1
        if (unit == "KiB" || unit == "kB") f = 1024
        if (unit == "MiB" || unit == "MB") f = 1048576
        if (unit == "GiB" || unit == "GB") f = 1073741824
        printf "%s %d\n", cpu, mem * f
      }' >>"$2" || true
  done
}

# summarize <file>: CPU average/max and memory max over the samples after the
# first (which straddles the start of the run).
summarize() {
  awk 'NR > 1 { n++; c += $1; if ($1 > cm) cm = $1; if ($2 > mm) mm = $2 }
    END { if (n == 0) { print "{\"cpu_avg_pct\":null,\"cpu_max_pct\":null,\"mem_max_mib\":null,\"samples\":0}"; exit }
      printf "{\"cpu_avg_pct\":%.0f,\"cpu_max_pct\":%.0f,\"mem_max_mib\":%.0f,\"samples\":%d}\n", c / n, cm, mm / 1048576, n }' "$1"
}

# load1: the host's 1-minute load average (noise indicator in results.jsonl).
load1() {
  if [ "$(uname -s)" = Darwin ]; then
    sysctl -n vm.loadavg | awk '{ print $2 }'
  else
    awk '{ print $1 }' /proc/loadavg
  fi
}

# k6 <server> <case> <mode> <rate|vus> <duration> <out name>
k6() {
  compose --profile tools run --rm -T \
    -e TARGET="http://$1:8080" -e CASE="$2" -e MODE="$3" -e RATE="$4" -e VUS="$4" \
    -e DURATION="$5" -e OUT="out/$6.json" \
    k6 run -q --no-thresholds k6/scenario.js >/dev/null 2>&1 || true
}

# measure <server> <case> <mode> <rate|vus> <repetition>
measure() {
  name="$1-$2-$3-$4-r$5"
  stats="out/stats-$name.txt"
  cid="$(compose ps -q "$1")"
  : >"$stats"
  : >"$stats.run"
  sampler "$cid" "$stats" &
  spid=$!
  load_before="$(load1)"
  k6 "$1" "$2" "$3" "$4" "$duration" "$name"
  load_after="$(load1)"
  rm -f "$stats.run"
  wait "$spid" 2>/dev/null || true
  if [ ! -s "out/$name.json" ]; then
    echo "bench: k6 produced no summary for $name" >&2
    return 1
  fi
  jq -c --arg server "$1" --argjson rep "$5" --argjson stats "$(summarize "$stats")" \
    --argjson lb "$load_before" --argjson la "$load_after" \
    '. + {server: $server, rep: $rep, host_load1_before: $lb, host_load1_after: $la} + $stats' \
    "out/$name.json" | tee -a out/results.jsonl |
    jq -r '"  \(.server) \(.mode) \(.rate // .vus) #\(.rep): \(.rps | floor) req/s, p50 \(.p50_ms * 10 | floor / 10) ms, p95 \(.p95_ms * 10 | floor / 10) ms, p99 \(.p99_ms * 10 | floor / 10) ms, failed \(.failed_rate), dropped \(.dropped), cpu \(.cpu_avg_pct)%, mem \(.mem_max_mib) MiB, load \(.host_load1_before)"'
}

skip() {
  [ "$1" = imgproxy ] && [ "$2" = info ]
}

cleanup() {
  rm -f out/*.run
  compose --profile xtandard --profile imgproxy --profile tools down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# ---- run -----------------------------------------------------------------------
echo "bench: starting $servers (workers $BENCH_WORKERS, $repeat x $duration per point)"
profiles=""
for server in $servers; do
  profiles="$profiles --profile $server"
done
# shellcheck disable=SC2086 # $profiles is a list of flags
compose $profiles up -d --wait --wait-timeout 60 >/dev/null 2>&1
for server in $servers; do
  idle="$(docker stats --no-stream --format '{{.MemUsage}}' "$(compose ps -q "$server")" | awk '{ print $1 }')"
  echo "  $server idle memory $idle"
  echo "$server $idle" >>out/idle-memory.txt
done

for c in $cases; do
  echo "bench: $c"
  for server in $servers; do
    skip "$server" "$c" || k6 "$server" "$c" max "$max_vus" "$warmup" "warmup-$server-$c"
  done
  for mode in $modes; do
    if [ "$mode" = rate ]; then
      points="$(echo "$case_table" | awk -v c="$c" '$1 == c { for (i = 2; i <= NF; i++) print $i }')"
    else
      points="$max_vus"
    fi
    for point in $points; do
      rep=1
      while [ "$rep" -le "$repeat" ]; do
        for server in $servers; do
          skip "$server" "$c" || measure "$server" "$c" "$mode" "$point" "$rep" || true
        done
        rep=$((rep + 1))
      done
    done
  done
done

# ---- report ------------------------------------------------------------------
median='def median: sort | if length == 0 then null elif length % 2 == 1 then .[length / 2 | floor] else (.[length / 2 - 1] + .[length / 2]) / 2 end;
  def r1: if . == null then "–" else . * 10 | round / 10 end;
  def failed: "\(.failed * 1000 | round / 10)%" + (if .s429 + .s503 + .sother > 0 then " (" + ([if .s429 > 0 then "429: \(.s429)" else empty end, if .s503 > 0 then "503: \(.s503)" else empty end, if .sother > 0 then "other: \(.sother)" else empty end] | join(", ")) + ")" else "" end);
  group_by([.case, .mode, (.rate // .vus), .server]) | map({
    case: .[0].case, mode: .[0].mode, point: (.[0].rate // .[0].vus), server: .[0].server, runs: length,
    rps: (map(.rps) | median), p50: (map(.p50_ms) | median), p95: (map(.p95_ms) | median),
    p99: (map(.p99_ms) | median), p99_min: (map(.p99_ms) | min), p99_max: (map(.p99_ms) | max),
    failed: (map(.failed_rate) | median), dropped: (map(.dropped) | add),
    s429: (map(.status_429 // 0) | add), s503: (map(.status_503 // 0) | add), sother: (map(.status_other // 0) | add),
    cpu: (map(.cpu_avg_pct) | map(select(. != null)) | median), mem: (map(.mem_max_mib) | map(select(. != null)) | max),
    load: (map(.host_load1_before) | median)
  })'
{
  echo "<!-- generated by bench/run.sh; copy into bench/RESULTS.md -->"
  echo
  echo "- Date: $started to $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "- Host: $cpu, $os"
  echo "- Docker: $docker_info"
  echo "- xtandard-image: \`$XI_IMAGE\` ($xi_version), \`$(digest "$XI_IMAGE")\`, created $(created "$XI_IMAGE")"
  echo "- imgproxy: \`$IMGPROXY_IMAGE\` ($ip_version), \`$(digest "$IMGPROXY_IMAGE")\`, created $(created "$IMGPROXY_IMAGE")"
  echo "- k6: $k6_version, \`$(digest "${K6_IMAGE:-grafana/k6:latest}")\`"
  echo "- Workers: $BENCH_WORKERS on both; closed-loop concurrency $max_vus; $repeat interleaved runs of $duration per server and point (medians below), $warmup warm-up per case"
  echo "- Idle memory after start: $(tr '\n' ',' <out/idle-memory.txt | sed 's/,$//; s/,/, /g')"
  echo "- Host 1-minute load average before each run: median $(jq -s 'map(.host_load1_before) | sort | .[length / 2 | floor]' out/results.jsonl), range $(jq -s 'map(.host_load1_before) | min' out/results.jsonl) to $(jq -s 'map(.host_load1_before) | max' out/results.jsonl)"
  echo
  echo "### Fixed arrival rate"
  echo
  echo "| Case | Rate (req/s) | Server | Achieved (req/s) | p50 (ms) | p95 (ms) | p99 (ms) | p99 range (ms) | Failed | Dropped | CPU avg (%) | Mem max (MiB) |"
  echo "|---|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|"
  jq -rs "$median"' | map(select(.mode == "rate")) | sort_by(.case, .point, .server) | .[] |
    "| \(.case) | \(.point) | \(.server) | \(.rps | r1) | \(.p50 | r1) | \(.p95 | r1) | \(.p99 | r1) | \(.p99_min | r1)–\(.p99_max | r1) | \(failed) | \(.dropped) | \(.cpu | if . == null then "–" else round end) | \(.mem | if . == null then "–" else round end) |"' out/results.jsonl
  echo
  echo "### Throughput ceiling (closed loop, $max_vus concurrent requests)"
  echo
  echo "| Case | Server | Throughput (req/s) | p50 (ms) | p95 (ms) | p99 (ms) | Failed | CPU avg (%) | Mem max (MiB) |"
  echo "|---|---|---:|---:|---:|---:|---:|---:|---:|"
  jq -rs "$median"' | map(select(.mode == "max")) | sort_by(.case, .server) | .[] |
    "| \(.case) | \(.server) | \(.rps | r1) | \(.p50 | r1) | \(.p95 | r1) | \(.p99 | r1) | \(failed) | \(.cpu | if . == null then "–" else round end) | \(.mem | if . == null then "–" else round end) |"' out/results.jsonl
} >out/report.md
echo "bench: wrote bench/out/report.md"
