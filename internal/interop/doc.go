// Package interop tests this module against the official TypeScript SDK
// of ACP: its example agent with a Go client, and its example client with
// the Go example agent (examples/echo). The SDK (a pinned version, from
// testdata/package-lock.json) and its examples (a pinned commit, checked
// against testdata/SHA256SUMS) go into a temporary folder; nothing is
// installed elsewhere. It needs Node.js (24 or later, which runs
// TypeScript as it is) and the network, so it runs only with the interop
// build tag: make interop.
package interop
