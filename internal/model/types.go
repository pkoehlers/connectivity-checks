package model

import "time"

// MissingRange represents a contiguous range of missing sequence numbers.
type MissingRange struct {
	From           uint64    `json:"from"`
	To             uint64    `json:"to"`
	GapDurationSec float64   `json:"gap_duration_sec"`
	DetectedAt     time.Time `json:"detected_at"`
}

// MinuteCount represents the number of requests received in a given minute.
type MinuteCount struct {
	Minute string `json:"minute"`
	Count  uint64 `json:"count"`
}

// RTTStats holds round-trip time / processing time statistics.
type RTTStats struct {
	MinMs        float64 `json:"min_ms"`
	MaxMs        float64 `json:"max_ms"`
	AvgMs        float64 `json:"avg_ms"`
	P95Ms        float64 `json:"p95_ms,omitempty"`
	P99Ms        float64 `json:"p99_ms,omitempty"`
	StddevMs     float64 `json:"stddev_ms,omitempty"`
	TotalSamples uint64  `json:"total_samples"`
	SlowCount    uint64  `json:"slow_count,omitempty"`
}

// ClientStatsJSON is the JSON representation of a single client's statistics.
type ClientStatsJSON struct {
	FirstSeen           time.Time      `json:"first_seen"`
	LastSeen            time.Time      `json:"last_seen"`
	LastSeq             uint64         `json:"last_seq"`
	TotalReceived       uint64         `json:"total_received"`
	TotalExpected       uint64         `json:"total_expected"`
	TotalMissing        uint64         `json:"total_missing"`
	MissingSequences    []MissingRange `json:"missing_sequences"`
	RequestsPerMinute   []MinuteCount  `json:"requests_per_minute"`
	ProcessingTimeStats *RTTStats      `json:"processing_time_stats,omitempty"`
	RTTStatsData        *RTTStats      `json:"rtt_stats,omitempty"`
}

// StatsResponse is the top-level JSON response for GET /stats.
type StatsResponse struct {
	Clients    map[string]*ClientStatsJSON `json:"clients"`
	ServerTime time.Time                   `json:"server_time"`
}

// PingResponse is the JSON response for GET /ping.
type PingResponse struct {
	Client       string    `json:"client"`
	SeqReceived  uint64    `json:"seq_received,omitempty"`
	ServerTime   time.Time `json:"server_time"`
	ProcessingMs float64   `json:"processing_ms,omitempty"`
	Status       string    `json:"status"`
}
