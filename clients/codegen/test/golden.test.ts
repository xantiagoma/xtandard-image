// One golden per language (WP-146): every emitter's files for the small
// spec of fixture.ts, under test/golden/<language>/ (a .golden suffix keeps every
// language's tooling away from them). The committed
// clients prove the full spec (generate --check); these pin the output of
// a spec small enough to read in review.
//
//   bun test clients/codegen/test/golden.test.ts
//   XI_UPDATE_GOLDEN=1 bun test clients/codegen/test/golden.test.ts   rewrite them

import { describe, expect, test } from "bun:test";
import type { Emitter } from "../spec.ts";
import { miniSpec } from "./fixture.ts";

const emitterDir = new URL("../emitters/", import.meta.url);
const goldenDir = new URL("golden/", import.meta.url);
const update = process.env["XI_UPDATE_GOLDEN"] === "1";
const spec = await miniSpec();

const names = [...new Bun.Glob("*.ts").scanSync({ cwd: emitterDir.pathname })].sort();

describe("golden", () => {
  for (const f of names) {
    test(f.replace(/\.ts$/, ""), async () => {
      const { emitter } = (await import(new URL(f, emitterDir).href)) as { emitter: Emitter };
      const out = emitter.emit(structuredClone(spec));
      for (const [path, content] of Object.entries(out)) {
        const file = Bun.file(new URL(`${emitter.language}/${path.replace(`clients/${emitter.language}/`, "")}.golden`, goldenDir));
        if (update) {
          await Bun.write(file, content);
          continue;
        }
        expect(await file.exists(), `${file.name} is missing: XI_UPDATE_GOLDEN=1`).toBe(true);
        expect(content).toBe(await file.text());
      }
    });
  }
});
