//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

func setupStatsSignal(printStats func()) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGUSR1)
	go func() {
		for range sigCh {
			printStats()
		}
	}()
}
