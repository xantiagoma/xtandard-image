# Getting help

xtandard-image is maintained by one person in their spare time. There is no paid support and no SLA. These are the places to go, in order:

1. **Docs first.** Start with [Getting started](docs/GETTING-STARTED.md). The rest of the documentation:
   - [CONFIG](docs/CONFIG.md): every setting;
   - [SOURCES](docs/SOURCES.md): where images come from;
   - [URL-DIALECTS](docs/URL-DIALECTS.md): how to write URLs in each dialect;
   - [FORMATS](docs/FORMATS.md): what each flavour reads and writes;
   - [DEPLOY](docs/DEPLOY.md) and [OPERATIONS](docs/OPERATIONS.md): running it in production.

   `xtandard-image config validate` explains most startup errors. The server's 4xx error bodies name the failed check, for example the source allowlist, a limit or the unsigned policy.
2. **Search the [issues](https://github.com/xantiagoma/xtandard-image/issues)**, open and closed.
3. **Open an issue.** Use the [bug report](https://github.com/xantiagoma/xtandard-image/issues/new?template=bug_report.yml) template for something broken. Use the [feature request](https://github.com/xantiagoma/xtandard-image/issues/new?template=feature_request.yml) template for a missing option, format, dialect or platform. Questions are welcome as issues too. Include your flavour, version and the URL or configuration involved, with secrets removed.

A **security vulnerability** never goes in an issue. Report it privately, as [SECURITY.md](SECURITY.md) explains.

The official clients in [`clients/`](clients/README.md), `@xtandard/image` included, are supported here too. Open issues for them in this repository.
