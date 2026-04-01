package stats

import (
	"math"
	"slices"
	"sync"
	"time"

	"github.com/pkoehlers/connectivity-checks/internal/model"
)

const (
	maxMissingRanges  = 10_000
	maxMinuteCounters = 1_440
	maxTimingSamples  = 86_400
	minuteKeyFormat   = "2006-01-02T15:04Z"
)

// Tracker tracks per-client sequence gaps, per-minute counters, and timing samples.
type Tracker struct {
	mu             sync.RWMutex
	name           string
	firstSeen      time.Time
	lastSeen       time.Time
	lastSeq        uint64
	totalReceived  uint64
	receivedSeqs   map[uint64]struct{}
	minuteCounters map[string]uint64
	missingRanges  []model.MissingRange
	timingSamples  []float64
	timingIdx      int
	timingFull     bool
	slowThreshold  float64
	slowCount      uint64
}

// NewTracker creates a new Tracker for a given client name.
// slowThresholdMs sets the threshold for counting a sample as "slow" (0 = disabled).
func NewTracker(name string, slowThresholdMs float64) *Tracker {
	return &Tracker{
		name:           name,
		receivedSeqs:   make(map[uint64]struct{}),
		minuteCounters: make(map[string]uint64),
		timingSamples:  make([]float64, 0, 1024),
		slowThreshold:  slowThresholdMs,
	}
}

// GapInfo is returned by RecordSeq when a gap is detected.
type GapInfo struct {
	From uint64
	To   uint64
}

// RecordSeq records a received sequence number. Returns detected gaps (if any)
// and whether this was a duplicate. The caller should log accordingly.
func (t *Tracker) RecordSeq(seq uint64, now time.Time) (gaps []GapInfo, duplicate bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.firstSeen.IsZero() {
		t.firstSeen = now
	}
	t.lastSeen = now

	// Duplicate check
	if _, exists := t.receivedSeqs[seq]; exists {
		return nil, true
	}

	t.receivedSeqs[seq] = struct{}{}
	t.totalReceived++

	// Gap detection
	if t.lastSeq > 0 && seq > t.lastSeq+1 {
		from := t.lastSeq + 1
		to := seq - 1
		gapDuration := float64(to - from + 1)
		mr := model.MissingRange{
			From:           from,
			To:             to,
			GapDurationSec: gapDuration,
			DetectedAt:     now,
		}
		t.missingRanges = append(t.missingRanges, mr)
		if len(t.missingRanges) > maxMissingRanges {
			t.missingRanges = t.missingRanges[len(t.missingRanges)-maxMissingRanges:]
		}
		gaps = append(gaps, GapInfo{From: from, To: to})
	}

	// Late arrival: seq <= lastSeq but wasn't in receivedSeqs - remove from missing ranges
	if seq <= t.lastSeq {
		t.removeLateArrival(seq)
	}

	if seq > t.lastSeq {
		t.lastSeq = seq
	}

	// Per-minute counter
	minuteKey := now.UTC().Format(minuteKeyFormat)
	t.minuteCounters[minuteKey]++
	t.pruneMinuteCounters(now)

	return gaps, false
}

// RecordFailure records a failed sequence number (client-side: request failed).
func (t *Tracker) RecordFailure(seq uint64, now time.Time) (gaps []GapInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.firstSeen.IsZero() {
		t.firstSeen = now
	}
	t.lastSeen = now

	// For client-side tracking, a failure means this seq was NOT received successfully.
	// We record the gap relative to lastSeq.
	if t.lastSeq > 0 && seq > t.lastSeq+1 {
		from := t.lastSeq + 1
		to := seq - 1
		gapDuration := float64(to - from + 1)
		mr := model.MissingRange{
			From:           from,
			To:             to,
			GapDurationSec: gapDuration,
			DetectedAt:     now,
		}
		t.missingRanges = append(t.missingRanges, mr)
		if len(t.missingRanges) > maxMissingRanges {
			t.missingRanges = t.missingRanges[len(t.missingRanges)-maxMissingRanges:]
		}
		gaps = append(gaps, GapInfo{From: from, To: to})
	}

	// The failed seq itself is also missing - extend last range or create new one
	if len(t.missingRanges) > 0 {
		last := &t.missingRanges[len(t.missingRanges)-1]
		if seq == last.To+1 {
			last.To = seq
			last.GapDurationSec = float64(last.To - last.From + 1)
		} else if seq > last.To {
			t.missingRanges = append(t.missingRanges, model.MissingRange{
				From:           seq,
				To:             seq,
				GapDurationSec: 1,
				DetectedAt:     now,
			})
		}
	} else {
		t.missingRanges = append(t.missingRanges, model.MissingRange{
			From:           seq,
			To:             seq,
			GapDurationSec: 1,
			DetectedAt:     now,
		})
	}
	if len(t.missingRanges) > maxMissingRanges {
		t.missingRanges = t.missingRanges[len(t.missingRanges)-maxMissingRanges:]
	}

	if seq > t.lastSeq {
		t.lastSeq = seq
	}

	return gaps
}

// RecordTiming adds a timing sample (ms).
func (t *Tracker) RecordTiming(ms float64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if ms > t.slowThreshold && t.slowThreshold > 0 {
		t.slowCount++
	}

	if len(t.timingSamples) < maxTimingSamples {
		t.timingSamples = append(t.timingSamples, ms)
	} else {
		t.timingSamples[t.timingIdx] = ms
		t.timingFull = true
	}
	t.timingIdx = (t.timingIdx + 1) % maxTimingSamples
}

// Reset clears all statistics for this tracker.
func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.firstSeen = time.Time{}
	t.lastSeen = time.Time{}
	t.lastSeq = 0
	t.totalReceived = 0
	t.receivedSeqs = make(map[uint64]struct{})
	t.minuteCounters = make(map[string]uint64)
	t.missingRanges = nil
	t.timingSamples = t.timingSamples[:0]
	t.timingIdx = 0
	t.timingFull = false
	t.slowCount = 0
}

// Snapshot returns a JSON-serializable snapshot of the current stats.
func (t *Tracker) Snapshot() *model.ClientStatsJSON {
	t.mu.RLock()
	defer t.mu.RUnlock()

	totalExpected := t.lastSeq
	totalMissing := uint64(0)
	if totalExpected > t.totalReceived {
		totalMissing = totalExpected - t.totalReceived
	}

	// Copy missing ranges
	missing := make([]model.MissingRange, len(t.missingRanges))
	copy(missing, t.missingRanges)

	// Build sorted minute counters
	minutes := make([]model.MinuteCount, 0, len(t.minuteCounters))
	for k, v := range t.minuteCounters {
		minutes = append(minutes, model.MinuteCount{Minute: k, Count: v})
	}
	slices.SortFunc(minutes, func(a, b model.MinuteCount) int {
		if a.Minute < b.Minute {
			return -1
		}
		if a.Minute > b.Minute {
			return 1
		}
		return 0
	})

	snap := &model.ClientStatsJSON{
		FirstSeen:         t.firstSeen,
		LastSeen:          t.lastSeen,
		LastSeq:           t.lastSeq,
		TotalReceived:     t.totalReceived,
		TotalExpected:     totalExpected,
		TotalMissing:      totalMissing,
		MissingSequences:  missing,
		RequestsPerMinute: minutes,
	}

	// Timing stats
	if ts := t.computeTimingStats(); ts != nil {
		snap.ProcessingTimeStats = ts
	}

	return snap
}

// SnapshotWithRTT returns a snapshot that labels timing data as rtt_stats (for client use).
func (t *Tracker) SnapshotWithRTT() *model.ClientStatsJSON {
	snap := t.Snapshot()
	if snap.ProcessingTimeStats != nil {
		snap.RTTStatsData = snap.ProcessingTimeStats
		snap.ProcessingTimeStats = nil
	}
	return snap
}

// TotalMissing returns the current count of missing sequences.
func (t *Tracker) TotalMissing() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.lastSeq > t.totalReceived {
		return t.lastSeq - t.totalReceived
	}
	return 0
}

// LastSeq returns the highest sequence number seen.
func (t *Tracker) LastSeq() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastSeq
}

func (t *Tracker) removeLateArrival(seq uint64) {
	for i := range t.missingRanges {
		r := &t.missingRanges[i]
		if seq < r.From || seq > r.To {
			continue
		}
		if r.From == r.To {
			t.missingRanges = slices.Delete(t.missingRanges, i, i+1)
			return
		}
		if seq == r.From {
			r.From++
			r.GapDurationSec = float64(r.To - r.From + 1)
			return
		}
		if seq == r.To {
			r.To--
			r.GapDurationSec = float64(r.To - r.From + 1)
			return
		}
		// Split the range
		newRange := model.MissingRange{
			From:           seq + 1,
			To:             r.To,
			GapDurationSec: float64(r.To - seq),
			DetectedAt:     r.DetectedAt,
		}
		r.To = seq - 1
		r.GapDurationSec = float64(r.To - r.From + 1)
		t.missingRanges = slices.Insert(t.missingRanges, i+1, newRange)
		return
	}
}

func (t *Tracker) pruneMinuteCounters(now time.Time) {
	if len(t.minuteCounters) <= maxMinuteCounters {
		return
	}
	cutoff := now.Add(-24 * time.Hour).UTC().Format(minuteKeyFormat)
	for k := range t.minuteCounters {
		if k < cutoff {
			delete(t.minuteCounters, k)
		}
	}
}

func (t *Tracker) computeTimingStats() *model.RTTStats {
	n := len(t.timingSamples)
	if n == 0 {
		return nil
	}

	sorted := make([]float64, n)
	copy(sorted, t.timingSamples)
	slices.Sort(sorted)

	var sum float64
	for _, v := range sorted {
		sum += v
	}
	avg := sum / float64(n)

	var variance float64
	for _, v := range sorted {
		d := v - avg
		variance += d * d
	}
	stddev := math.Sqrt(variance / float64(n))

	p95idx := int(math.Ceil(float64(n)*0.95)) - 1
	p99idx := int(math.Ceil(float64(n)*0.99)) - 1
	if p95idx < 0 {
		p95idx = 0
	}
	if p99idx < 0 {
		p99idx = 0
	}
	if p95idx >= n {
		p95idx = n - 1
	}
	if p99idx >= n {
		p99idx = n - 1
	}

	return &model.RTTStats{
		MinMs:        sorted[0],
		MaxMs:        sorted[n-1],
		AvgMs:        math.Round(avg*100) / 100,
		P95Ms:        sorted[p95idx],
		P99Ms:        sorted[p99idx],
		StddevMs:     math.Round(stddev*100) / 100,
		TotalSamples: uint64(n),
		SlowCount:    t.slowCount,
	}
}

// Store manages multiple client trackers.
type Store struct {
	mu      sync.RWMutex
	clients map[string]*Tracker
	slowMs  float64
}

// NewStore creates a new Store.
func NewStore(slowThresholdMs float64) *Store {
	return &Store{
		clients: make(map[string]*Tracker),
		slowMs:  slowThresholdMs,
	}
}

// Get returns the tracker for a client, creating one if it doesn't exist.
func (s *Store) Get(name string) *Tracker {
	s.mu.RLock()
	t, ok := s.clients[name]
	s.mu.RUnlock()
	if ok {
		return t
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok = s.clients[name]; ok {
		return t
	}
	t = NewTracker(name, s.slowMs)
	s.clients[name] = t
	return t
}

// ResetClient clears stats for a client.
func (s *Store) ResetClient(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, name)
}

// Snapshot returns a map of all client stats, or a single client if name is non-empty.
func (s *Store) Snapshot(name string) map[string]*model.ClientStatsJSON {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]*model.ClientStatsJSON)
	if name != "" {
		if t, ok := s.clients[name]; ok {
			result[name] = t.Snapshot()
		}
		return result
	}
	for k, t := range s.clients {
		result[k] = t.Snapshot()
	}
	return result
}
