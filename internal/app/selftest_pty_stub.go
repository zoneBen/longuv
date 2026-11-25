//go:build !linux

package app

// testPTY is a no-op on platforms without /dev/ptmx support. RunSelftest
// prints a SKIP line based on pty.IsSupported.
func testPTY() error { return nil }
