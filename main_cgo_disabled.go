//go:build !cgo

// Package main placeholder so the module can be compiled (e.g. go test/vet on a
// machine without a C toolchain). The real plugin entrypoint is main.go, which
// requires cgo because the C-ABI plugin is built as a c-shared library.
package main

func main() {}
