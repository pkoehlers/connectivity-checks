package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/pkoehlers/connectivity-checks/internal/logging"
	"github.com/pkoehlers/connectivity-checks/internal/model"
	"github.com/pkoehlers/connectivity-checks/internal/stats"
)

func main() {
	port := flag.Int("port", 8080, "Listen port")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	flag.Parse()

	level := logging.ParseLevel(*logLevel)
	handler := logging.NewHumanHandler(os.Stdout, level)
	logger := slog.New(handler)
	slog.SetDefault(logger)

	store := stats.NewStore(0)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", pingHandler(store, logger))
	mux.HandleFunc("GET /stats", statsHandler(store))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok\n"))
	})

	addr := fmt.Sprintf(":%d", *port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("server starting", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
}

func pingHandler(store *stats.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		clientName := r.URL.Query().Get("client")
		seqStr := r.URL.Query().Get("seq")

		if clientName == "" || seqStr == "" {
			http.Error(w, `{"error":"client and seq parameters required"}`, http.StatusBadRequest)
			return
		}

		seq, err := strconv.ParseUint(seqStr, 10, 64)
		if err != nil {
			http.Error(w, `{"error":"seq must be a positive integer"}`, http.StatusBadRequest)
			return
		}

		now := time.Now().UTC()

		// Reset
		if seq == 0 {
			store.ResetClient(clientName)
			logger.Info("reset", "client", clientName)

			resp := model.PingResponse{
				Client:     clientName,
				Status:     "reset",
				ServerTime: now,
			}
			writeJSON(w, http.StatusOK, resp)
			return
		}

		tracker := store.Get(clientName)
		gaps, duplicate := tracker.RecordSeq(seq, now)

		if duplicate {
			logger.Debug("ping", "client", clientName, "seq", seq, "status", "duplicate")
		} else {
			for _, g := range gaps {
				logger.Warn("gap",
					"client", clientName,
					"missing", fmt.Sprintf("%d..%d", g.From, g.To),
					"gap_sec", g.To-g.From+1,
				)
			}
			logger.Info("ping", "client", clientName, "seq", seq, "status", "ok")
		}

		processingMs := float64(time.Since(start).Microseconds()) / 1000.0
		tracker.RecordTiming(processingMs)

		resp := model.PingResponse{
			Client:       clientName,
			SeqReceived:  seq,
			ServerTime:   now,
			ProcessingMs: processingMs,
			Status:       "ok",
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func statsHandler(store *stats.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clientName := r.URL.Query().Get("client")
		clients := store.Snapshot(clientName)

		resp := model.StatsResponse{
			Clients:    clients,
			ServerTime: time.Now().UTC(),
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}
