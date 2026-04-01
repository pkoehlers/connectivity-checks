package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pkoehlers/connectivity-checks/internal/logging"
	"github.com/pkoehlers/connectivity-checks/internal/model"
	"github.com/pkoehlers/connectivity-checks/internal/stats"
)

func main() {
	serverURL := flag.String("server", "", "Server URL (required)")
	name := flag.String("name", "", "Client name (required)")
	interval := flag.Duration("interval", 1*time.Second, "Probe interval")
	timeout := flag.Duration("timeout", 3*time.Second, "HTTP request timeout")
	reset := flag.Bool("reset", false, "Send reset on startup")
	rttWarn := flag.Duration("rtt-warn", 500*time.Millisecond, "RTT warning threshold")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	statsPort := flag.Int("stats-port", 0, "Local HTTP port to serve stats (useful on Windows where SIGUSR1 is unavailable)")
	flag.Parse()

	if *serverURL == "" || *name == "" {
		fmt.Fprintln(os.Stderr, "Error: --server and --name are required")
		flag.Usage()
		os.Exit(1)
	}

	// Validate server URL
	parsedURL, err := url.Parse(*serverURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		fmt.Fprintln(os.Stderr, "Error: --server must be a valid http:// or https:// URL")
		os.Exit(1)
	}
	serverHost := parsedURL.Host

	level := logging.ParseLevel(*logLevel)
	handler := logging.NewHumanHandler(os.Stdout, level)
	logger := slog.New(handler)
	slog.SetDefault(logger)

	tracker := stats.NewTracker(*name, float64(rttWarn.Milliseconds()))
	httpClient := &http.Client{Timeout: *timeout}

	// Build ping URL
	pingURL := *serverURL + "/ping"

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Print stats helper
	printStats := func() {
		snap := tracker.SnapshotWithRTT()
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		fmt.Fprintln(os.Stdout, "\n--- Client Statistics ---")
		enc.Encode(snap)
		fmt.Fprintln(os.Stdout, "--- End Statistics ---")
	}

	// Platform-specific signal for interim stats (SIGUSR1 on Unix)
	setupStatsSignal(printStats)

	// Optional local stats HTTP endpoint
	if *statsPort > 0 {
		go func() {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /stats", func(w http.ResponseWriter, _ *http.Request) {
				snap := tracker.SnapshotWithRTT()
				w.Header().Set("Content-Type", "application/json")
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				enc.Encode(snap)
			})
			addr := fmt.Sprintf("127.0.0.1:%d", *statsPort)
			logger.Info("stats endpoint", "addr", addr)
			http.ListenAndServe(addr, mux)
		}()
	}

	// Send reset if requested
	if *reset {
		resetURL := fmt.Sprintf("%s?client=%s&seq=0", pingURL, url.QueryEscape(*name))
		resp, err := httpClient.Get(resetURL)
		if err != nil {
			logger.Warn("reset failed", "error", err)
		} else {
			resp.Body.Close()
			logger.Info("reset", "server", serverHost, "status", "sent")
		}
	}

	logger.Info("client starting",
		"server", serverHost,
		"name", *name,
		"interval", interval.String(),
		"timeout", timeout.String(),
		"rtt_warn", rttWarn.String(),
	)

	var seq uint64
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("shutting down")
			printStats()
			return
		case <-ticker.C:
			seq++
			probeURL := fmt.Sprintf("%s?client=%s&seq=%d", pingURL, url.QueryEscape(*name), seq)

			start := time.Now()
			resp, err := httpClient.Get(probeURL)
			rttMs := float64(time.Since(start).Microseconds()) / 1000.0

			if err != nil {
				tracker.RecordFailure(seq, time.Now().UTC())
				logger.Warn("probe",
					"seq", seq,
					"server", serverHost,
					"status", "FAIL",
					"error", err.Error(),
					"rtt", "-",
				)
				continue
			}

			var pingResp model.PingResponse
			json.NewDecoder(resp.Body).Decode(&pingResp)
			resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				tracker.RecordFailure(seq, time.Now().UTC())
				logger.Warn("probe",
					"seq", seq,
					"server", serverHost,
					"status", fmt.Sprintf("HTTP_%d", resp.StatusCode),
					"rtt", fmt.Sprintf("%.0fms", rttMs),
				)
				continue
			}

			// Record success
			now := time.Now().UTC()
			gaps, _ := tracker.RecordSeq(seq, now)
			tracker.RecordTiming(rttMs)

			rttStr := fmt.Sprintf("%.0fms", rttMs)
			slow := rttMs > float64(rttWarn.Milliseconds()) && *rttWarn > 0

			if slow {
				logger.Warn("probe",
					"seq", seq,
					"server", serverHost,
					"status", "ok",
					"rtt", rttStr+" (slow)",
				)
			} else {
				logger.Info("probe",
					"seq", seq,
					"server", serverHost,
					"status", "ok",
					"rtt", rttStr,
				)
			}

			for _, g := range gaps {
				logger.Warn("gap",
					"server", serverHost,
					"missing", fmt.Sprintf("%d..%d", g.From, g.To),
					"gap_sec", g.To-g.From+1,
					"total_missing", tracker.TotalMissing(),
				)
			}
		}
	}
}
