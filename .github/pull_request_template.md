## What and why

<!-- What this pull request changes, and the problem it solves. Link the issue: "Fixes #123". -->

## How it was verified

<!--
Paste the summary of the checks you ran (CONTRIBUTING.md, "Checks a pull request must pass").
Go changes: task docker:lint and task docker:test (inside the base image, not only on the host).
Plus, when relevant: task ts:check, task clients:check, task quickstart, task acceptance.
-->

## Checklist

- [ ] Commits follow Conventional Commits (`type(scope): subject`), and a breaking change is marked with `!`.
- [ ] The user docs are updated for any change in behaviour, configuration or URL options: CONFIG, URL-DIALECTS, FORMATS, SOURCES, or the README.
- [ ] The `Unreleased` section of `CHANGELOG.md` has an entry, if users will notice the change.
- [ ] If this adds a dependency, the ADR has a line for it, and its licence fits [ADR 0006](https://github.com/xantiagoma/xtandard-image/blob/main/docs/adr/0006-license-policy.md).
- [ ] Generated files are regenerated, not edited by hand (`task clients:spec clients:generate`, `bun run generate`).
- [ ] For a performance claim, `benchstat` output from `-count=10` runs is included.

## Deviations

<!-- Anything that departs from docs/go-standards.md, an ADR or the docs, and why. Write "None" if there is nothing. -->
