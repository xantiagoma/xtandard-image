// Mixed traffic for the local observability stack (compose.yml, profile
// "load") and the autoscaling check (deploy/scripts/kind-autoscale.sh):
// image resizes across formats and sizes (cache misses and hits), /info,
// some 404s and, with VIDEO=1, short video transformations.
//
//   TARGET    base URL (http://xtandard-image:8080)
//   RATE      requests per second (20)
//   DURATION  how long (2m)
//   VIDEO     1 to add video transformations (needs the full or gpl image)
//   SOURCE    source prefix (xi://local/, an fs source over testdata/fixtures)
//   IMAGES    comma list of source keys; FORMATS comma list of output formats
import http from "k6/http";

const target = __ENV.TARGET || "http://xtandard-image:8080";
const rate = Number(__ENV.RATE || 20);
const source = __ENV.SOURCE || "xi://local/";
const video = __ENV.VIDEO === "1";

const list = (v, d) => (v ? v.split(",") : d);
// IMAGES and FORMATS (comma lists) override the mix.
const images = list(__ENV.IMAGES, ["jpeg/baseline.jpg", "jpeg/progressive.jpg", "png/rgba8.png", "png/rgb16.png"]);
const formats = list(__ENV.FORMATS, ["webp", "avif", "jpg", "png"]);
const widths = [120, 240, 320, 480, 640, 800, 1024];

export const options = {
  discardResponseBodies: true,
  scenarios: {
    traffic: {
      executor: "constant-arrival-rate",
      rate,
      timeUnit: "1s",
      duration: __ENV.DURATION || "2m",
      preAllocatedVUs: Math.max(8, rate),
      maxVUs: Math.max(64, rate * 4),
    },
  },
};

function pick(a) {
  return a[Math.floor(Math.random() * a.length)];
}

export default function () {
  const r = Math.random();
  let path;
  if (r < 0.03) {
    path = `/insecure/rs:fit:200:200/plain/${source}missing/nope.jpg@webp`; // 404
  } else if (r < 0.08) {
    path = `/info/insecure/plain/${source}${pick(images)}`;
  } else if (video && r < 0.1) {
    path = `/video/insecure/rs:fit:${pick([160, 240])}:0/plain/${source}video/clip-av1.mp4@webm`;
  } else {
    path = `/insecure/rs:fit:${pick(widths)}:${pick(widths)}/q:${pick([70, 80])}/plain/${source}${pick(images)}@${pick(formats)}`;
  }
  http.get(target + path, { tags: { name: "xi" } });
}
