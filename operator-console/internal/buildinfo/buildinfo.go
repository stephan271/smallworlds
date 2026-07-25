// Package buildinfo holds immutable release metadata set by the release build.
// Keeping it separate from the command makes the exact same value available to
// diagnostics and future versioned API compatibility checks without relying on
// an ambient Git checkout at runtime.
package buildinfo

// Version is replaced with a release tag by the packaging command. Development
// and ordinary test builds deliberately remain identifiable as "devel".
var Version = "devel"
