// The root composer.json publishes the PHP client from this repository
// (ADR 0013, no mirror repositories). Its require and autoload must match
// the development manifest in clients/php, with paths under clients/php/.
const root = await Bun.file("composer.json").json();
const php = await Bun.file("clients/php/composer.json").json();
const errors: string[] = [];
const eq = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
for (const key of ["name", "license", "require", "suggest"]) {
  if (!eq(root[key], php[key])) errors.push(`composer.json ${key} differs from clients/php/composer.json`);
}
const prefix = (p: string) => `clients/php/${p}`;
const want = {
  "psr-4": Object.fromEntries(
    Object.entries(php.autoload["psr-4"] as Record<string, string | string[]>).map(([ns, dirs]) => [
      ns,
      (Array.isArray(dirs) ? dirs : [dirs]).map(prefix),
    ]),
  ),
  files: (php.autoload.files as string[]).map(prefix),
};
if (!eq(root.autoload, want)) errors.push(`composer.json autoload should be ${JSON.stringify(want)}`);
if (errors.length) {
  for (const e of errors) console.error(e);
  process.exit(1);
}
console.log("root composer.json matches clients/php/composer.json");
