// k6 scenario for bench/run.sh (docs/BENCHMARKS.md). One run measures one
// case against one server, either at a fixed arrival rate (MODE=rate) or
// closed-loop at a fixed concurrency (MODE=max, throughput ceiling).
//
//   TARGET    base URL of the server (http://xtandard:8080)
//   CASE      key of CASES below
//   MODE      rate | max
//   RATE      requests per second (MODE=rate)
//   VUS       concurrent virtual users (MODE=max)
//   DURATION  measured duration (30s)
//   OUT       summary JSON path (out/<name>.json)
//
// Both servers get the same imgproxy URLs; neither is signed.
import http from "k6/http";
import { check } from "k6";
import { Counter } from "k6/metrics";

export const CASES = {
  "jpeg-400-jpeg": "/insecure/rs:fit:400:400/plain/http://fixtures/photo.jpg@jpg",
  "jpeg-400-webp": "/insecure/rs:fit:400:400/plain/http://fixtures/photo.jpg@webp",
  "jpeg-400-avif": "/insecure/rs:fit:400:400/plain/http://fixtures/photo.jpg@avif",
  "png-alpha-400-webp": "/insecure/rs:fit:400:400/plain/http://fixtures/alpha.png@webp",
  "gif-anim-240-webp": "/insecure/rs:fit:240:240/plain/http://fixtures/anim.gif@webp",
  "jpeg-1200-fill-jpeg": "/insecure/rs:fill:1200:1200/plain/http://fixtures/photo.jpg@jpg",
  "info": "/info/insecure/plain/http://fixtures/photo.jpg",
};

const target = __ENV.TARGET;
const name = __ENV.CASE;
const mode = __ENV.MODE || "rate";
const duration = __ENV.DURATION || "30s";
const path = CASES[name];
if (!target || !path) {
  throw new Error(`TARGET and a known CASE are required (got ${target}, ${name})`);
}
const url = target + path;

// Non-200 answers by status: 429/503 are load shedding (queue full / queue
// timeout), anything else is an error.
const status429 = new Counter("status_429");
const status503 = new Counter("status_503");
const statusOther = new Counter("status_other");

function scenario() {
  if (mode === "max") {
    return { executor: "constant-vus", vus: Number(__ENV.VUS || 16), duration };
  }
  const rate = Number(__ENV.RATE || 10);
  return {
    executor: "constant-arrival-rate",
    rate,
    timeUnit: "1s",
    duration,
    preAllocatedVUs: Math.max(8, rate),
    maxVUs: Math.max(64, rate * 4),
  };
}

export const options = {
  discardResponseBodies: true,
  summaryTrendStats: ["avg", "min", "med", "p(90)", "p(95)", "p(99)", "max"],
  scenarios: { load: scenario() },
  // Recorded, not enforced: run.sh reports failures and dropped iterations.
  thresholds: { http_req_failed: ["rate>=0"] },
};

export default function () {
  const res = http.get(url, { timeout: "30s", tags: { case: name } });
  check(res, { "status 200": (r) => r.status === 200 });
  if (res.status === 429) status429.add(1);
  else if (res.status === 503) status503.add(1);
  else if (res.status !== 200) statusOther.add(1);
}

export function handleSummary(data) {
  const m = data.metrics;
  const d = m.http_req_duration.values;
  const out = {
    case: name,
    mode,
    rate: mode === "rate" ? Number(__ENV.RATE || 10) : null,
    vus: mode === "max" ? Number(__ENV.VUS || 16) : null,
    duration,
    requests: m.http_reqs.values.count,
    rps: m.http_reqs.values.rate,
    failed_rate: m.http_req_failed.values.rate,
    dropped: m.dropped_iterations ? m.dropped_iterations.values.count : 0,
    status_429: m.status_429 ? m.status_429.values.count : 0,
    status_503: m.status_503 ? m.status_503.values.count : 0,
    status_other: m.status_other ? m.status_other.values.count : 0,
    p50_ms: d.med,
    p90_ms: d["p(90)"],
    p95_ms: d["p(95)"],
    p99_ms: d["p(99)"],
    max_ms: d.max,
    avg_ms: d.avg,
  };
  return { [__ENV.OUT || "out/summary.json"]: JSON.stringify(out, null, 2) + "\n" };
}
