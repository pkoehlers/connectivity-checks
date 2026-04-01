# Connectivity Checker

Detects, quantifies, and proves intermittent HTTP connectivity drops with exact timestamps and gap analysis. Designed for scenarios where ICMP ping is blocked (e.g., Zscaler tunnels) or insufficient (no sequence tracking, no per-minute throughput).

A **client** sends numbered HTTP probes at a fixed interval. A **server** records every probe and detects gaps in the sequence. Both sides independently track missing sequences, so either side's logs are sufficient evidence.

## What it proves

- Exact start/end time and duration of every connectivity gap
- Number of missing probes per gap and total
- Per-minute request throughput (spot partial degradation)
- Round-trip time statistics with percentiles (p95/p99) and slow-probe detection
- Server-side processing time per request

Compare gap windows across multiple servers to isolate whether the problem is client-side, ISP, or server-side.

## Quick Start

### Build

```bash
go build -o bin/connectivity-server ./cmd/server
go build -o bin/connectivity-client ./cmd/client
```

### Local smoke test

**Terminal 1 — start the server:**

```bash
./bin/connectivity-server --port 8080
```

**Terminal 2 — run the client:**

```bash
./bin/connectivity-client --server http://localhost:8080 --name my-laptop --interval 1s --reset
```

Let it run for a few seconds, then press `Ctrl+C`. The client prints a summary on shutdown:

```
--- Client Statistics ---
{
  "first_seen": "...",
  "last_seq": 5,
  "total_received": 5,
  "total_missing": 0,
  "missing_sequences": [],
  "rtt_stats": { "min_ms": 0.5, "max_ms": 1.2, "avg_ms": 0.8, ... }
}
--- End Statistics ---
```

**Terminal 2 — check server-side stats:**

```bash
curl -s http://localhost:8080/stats | python3 -m json.tool
```

### Simulate a gap

```bash
# Send specific sequence numbers with a gap (seq 3 and 4 missing)
curl -s 'http://localhost:8080/ping?client=test&seq=1'
curl -s 'http://localhost:8080/ping?client=test&seq=2'
curl -s 'http://localhost:8080/ping?client=test&seq=5'

# Check stats — shows missing_sequences: [{from: 3, to: 4}]
curl -s 'http://localhost:8080/stats?client=test' | python3 -m json.tool
```

### Reset a client

```bash
curl -s 'http://localhost:8080/ping?client=test&seq=0'
```

## Server

| Flag | Default | Description |
|---|---|---|
| `--port` | `8080` | Listen port |
| `--log-level` | `info` | `debug`, `info`, `warn`, `error` |

**Endpoints:**

| Endpoint | Description |
|---|---|
| `GET /ping?client=NAME&seq=N` | Record probe. `seq=0` resets the client. |
| `GET /stats?client=NAME` | JSON stats (all clients if `client` omitted) |
| `GET /healthz` | Returns `200 ok` (Kubernetes readiness) |

## Client

| Flag | Default | Description |
|---|---|---|
| `--server` | (required) | Server URL |
| `--name` | (required) | Client identifier |
| `--interval` | `1s` | Probe interval |
| `--timeout` | `3s` | HTTP request timeout |
| `--reset` | `false` | Send reset on startup |
| `--rtt-warn` | `500ms` | Log WARN if RTT exceeds this |
| `--stats-port` | `0` | Local HTTP port for stats (useful on Windows) |
| `--log-level` | `info` | `debug`, `info`, `warn`, `error` |

On Linux/macOS, send `SIGUSR1` to print interim stats without stopping.

## Docker

```bash
docker build -f Dockerfile.server -t connectivity-server .
docker build -f Dockerfile.client -t connectivity-client .

docker run -p 8080:8080 connectivity-server
docker run connectivity-client --server http://host.docker.internal:8080 --name docker-test
```

## Kubernetes

```bash
kubectl apply -f deploy/kubernetes/server.yaml
kubectl apply -f deploy/kubernetes/httproute.yaml   # adjust gateway name and hostname
kubectl apply -f deploy/kubernetes/client.yaml       # probes server in-cluster
```

## Cross-platform

Builds for Linux, macOS, and Windows:

```bash
GOOS=darwin  GOARCH=arm64 go build -o bin/connectivity-client-darwin-arm64  ./cmd/client
GOOS=windows GOARCH=amd64 go build -o bin/connectivity-client-windows.exe  ./cmd/client
```
