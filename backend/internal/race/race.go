// Package race reports whether the binary was built with the race detector, so timing budget
// tests can skip themselves there (the detector slows code 5–60×).
package race

// Enabled is true in -race builds (see on.go).
const Enabled = enabled
