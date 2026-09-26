// Package acceptance is the M1 acceptance suite (ROADMAP WP-39): a
// black-box, end-to-end run of the published app image started by
// compose.acceptance.yml and configured through environment variables only.
//
// The tests live in files guarded by the "acceptance" build tag and talk to
// the containers over HTTP; nothing here imports the server's packages, so
// signatures, encrypted sources and image checks are independent
// re-implementations of the documented contracts (docs/URL-DIALECTS.md §3,
// docs/ARCHITECTURE.md §9 and §12). Run it with scripts/acceptance.sh
// (`task acceptance`), which starts the stack, generates the keys and exports
// the endpoints:
//
//	XI_ACC_APP, XI_ACC_ADMIN            main app, public and admin listeners
//	XI_ACC_TINY, XI_ACC_TINY_ADMIN      one-worker app for 429 and SIGTERM drain
//	XI_ACC_TINY_CONTAINER               its container ID (docker kill/wait)
//	XI_ACC_SIGNING_KEY, _SALT           hex imgproxy key/salt of the main app
//	XI_ACC_ENCRYPTION_KEY               hex AES key for enc/ sources
//	XI_ACC_IMGIX_TOKEN                  imgix secure URL token of the main app
//	XI_ACC_FIXTURES                     path of testdata/fixtures on the host
//
// Every test skips when its endpoint is unset (go-standards §8).
package acceptance
