// Developer tools, kept out of the application's module graph (go-standards §1).
// Run with: go tool -modfile=tools/go.mod <tool> ...
module github.com/xantiagoma/xtandard-image/tools

go 1.27.0

toolchain go1.27.1

tool (
	golang.org/x/perf/cmd/benchstat
	golang.org/x/vuln/cmd/govulncheck
)

require (
	github.com/aclements/go-moremath v0.0.0-20210112150236-f10218a38794 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/perf v0.0.0-20260908200009-22c9c6c9d4da // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/telemetry v0.0.0-20260908163034-4bcc4b2ee518 // indirect
	golang.org/x/tools v0.50.0 // indirect
	golang.org/x/vuln v1.8.0 // indirect
)
