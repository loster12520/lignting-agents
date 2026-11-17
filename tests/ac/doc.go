// Package ac holds end-to-end acceptance tests for the 15 ACs defined in
// doc/0.0.1 最初版/requirements.md §14.
//
// Run with:
//
//	go test -tags=acceptance ./tests/ac -v
//
// Without the acceptance build tag this directory builds as an empty package,
// so plain `go test ./...` stays fast and does not need a DeepSeek key.
package ac
