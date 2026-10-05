// Type tests, checked by `bun run typecheck` (tsc --noEmit). Every
// `@ts-expect-error` must be an error, so this file fails to compile if
// the option types ever accept an unknown option or a wrong argument.

import {
  buildImgproxyInfoUrl,
  buildImgproxyUrl,
  buildImgproxyVideoUrl,
  type ImgproxyInfoOptions,
  type ImgproxyProcessingArgsOf,
  type ImgproxyProcessingOptions,
  type ImgproxyVideoArgsOf,
  type ImgproxyVideoOptions,
  imgproxy,
} from "../src/index.ts";
import { createClient, type ErrorKind, type InfoResponse, XtandardError } from "../src/client.ts";

type Equal<A, B> = (<T>() => T extends A ? 1 : 2) extends <T>() => T extends B ? 1 : 2 ? true : false;
function assertType<T extends true>(_: T): void {}

// ---- valid options -------------------------------------------------------------

const ok: ImgproxyProcessingOptions = {
  resize: ["fill", 300, 400, false],
  rs: ["fit"], // an alias, with trailing arguments omitted
  gravity: ["fp", 0.5, 0.25],
  g: "sm", // a single argument without the tuple
  quality: 80,
  q: [75],
  background: [255, 0, 128],
  bg: "ffffff",
  padding: [10, null, 20], // null is an empty argument
  enlarge: true,
  format: "webp",
  blur_detections: [2, "face", "cat"], // a rest group
  "min-width": 100,
  crop: [100, 200, "fp", 0.5, 0.5],
};
void ok;

const info: ImgproxyInfoOptions = { size: true, format: [true], dominant_colors: [true, true], a: true };
void info;

assertType<Equal<ImgproxyProcessingArgsOf<"rs">, ImgproxyProcessingArgsOf<"resize">>>(true);

const video: ImgproxyVideoOptions = {
  start: 1.5,
  vd: 5,
  resize: ["fill", 640, 360],
  gravity: ["soea", 10, 10],
  format: "webm",
  video_codec: "av1",
  vb: "2M",
  loudnorm: [true, -16, -1.5, 11],
  waveform: [true, "2266cc", null, true],
  fade: [1, 2],
};
void video;
void buildImgproxyVideoUrl({ source: "s3://b/clip.mp4", options: video, extension: "webm" });
void imgproxy().video("s3://b/clip.mp4").vs(1).rs("fit", 320).sr(44100).extension("m4a").build();
assertType<Equal<ImgproxyVideoArgsOf<"vs">, ImgproxyVideoArgsOf<"start">>>(true);

// @ts-expect-error auto is not a video resizing type
const videoAuto: ImgproxyVideoOptions = { resize: ["auto", 1, 1] };
void videoAuto;

// @ts-expect-error an image-only option is not a video option
const videoBlur: ImgproxyVideoOptions = { blur: 2 };
void videoBlur;

// @ts-expect-error not a video container
const videoGif: ImgproxyVideoOptions = { format: "gif" };
void videoGif;

// ---- unknown options ---------------------------------------------------------

// @ts-expect-error an unknown option
const unknown: ImgproxyProcessingOptions = { nope: [1] };
void unknown;

// @ts-expect-error a processing option is not an info option
const wrongEndpoint: ImgproxyInfoOptions = { resize: ["fit", 1, 1] };
void wrongEndpoint;

// ---- wrong arguments ------------------------------------------------------

// @ts-expect-error quality is a number, not a string
const wrongType: ImgproxyProcessingOptions = { quality: ["80"] };
void wrongType;

// @ts-expect-error not a resizing type
const wrongEnum: ImgproxyProcessingOptions = { resize: ["cover", 300, 400] };
void wrongEnum;

// @ts-expect-error too many arguments
const tooMany: ImgproxyProcessingOptions = { quality: [80, 1] };
void tooMany;

// @ts-expect-error fp gravity needs x and y
const tooFew: ImgproxyProcessingOptions = { gravity: ["fp", 0.5] };
void tooFew;

// @ts-expect-error enlarge is a boolean
const notBool: ImgproxyProcessingOptions = { enlarge: ["yes"] };
void notBool;

// @ts-expect-error at least one argument
const empty: ImgproxyProcessingOptions = { width: [] };
void empty;

// ---- the builders ----------------------------------------------------------------

const img = imgproxy({ baseUrl: "https://img.example.com", signing: { key: "00", salt: "00", size: 8 } });

void buildImgproxyUrl({ source: "s3://b/k.jpg", options: [{ w: 300 }, { bl: 2 }], extension: "webp" });
void buildImgproxyInfoUrl({ source: "s3://b/k.jpg", options: { size: true } });
void img.url({ source: "s3://b/k.jpg", options: { rs: ["fill", 1, 1] } });

// @ts-expect-error unknown option in the object form
void img.url({ source: "s3://b/k.jpg", options: { nope: 1 } });

const chain = img.from("s3://b/k.jpg").resize("fill", 300, 400).rs("fit").quality(80).chain().blur(2).extension("webp");
const built: Promise<string> = chain.build();
void built;

// @ts-expect-error an unknown fluent method
img.from("x").nope(1);

// @ts-expect-error a wrong argument type in the fluent form
img.from("x").quality("80");

// @ts-expect-error option() checks the name…
img.from("x").option("nope", 1);

// @ts-expect-error …and the arguments
img.from("x").option("width", "wide");

img.info("x").size(true).format(true).option("dominant_colors", true, true);

// @ts-expect-error /info URLs have no chained pipelines
img.info("x").chain();

// ---- the client ---------------------------------------------------------------------

const client = createClient({ baseUrl: "http://localhost:8080" });
const infoResponse: Promise<InfoResponse> = client.info({ source: "s3://b/k.jpg", options: { size: true } });
void infoResponse;
void client.readyz().then((r) => {
  const s: "ready" | "starting" | "draining" = r.state;
  void s;
});

const kind: ErrorKind = "invalid_url";
void kind;
// @ts-expect-error not an error kind
const badKind: ErrorKind = "teapot";
void badKind;

const e = new XtandardError(400, "u", undefined, "m");
const status: number = e.status;
void status;
