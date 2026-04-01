//go:build windows

package main

// setupStatsSignal is a no-op on Windows since SIGUSR1 is not available.
// Use --stats-port to expose stats via a local HTTP endpoint instead.
func setupStatsSignal(_ func()) {}
