// Package monitor collects lightweight runtime metrics: request and byte
// counters, process CPU and memory, and a 24 h per-minute history.
package monitor

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Monitor struct {
	Started time.Time

	Requests   atomic.Int64
	Errors     atomic.Int64 // 5xx
	ClientErrs atomic.Int64 // 4xx
	BytesOut   atomic.Int64
	BytesIn    atomic.Int64
	Conns      atomic.Int64
	ShareHits  atomic.Int64
	DavReqs    atomic.Int64

	mu      sync.Mutex
	samples []Sample // ring, newest last
	last    Sample
	lastCPU float64 // seconds
	lastAt  time.Time
}

type Sample struct {
	T          int64   `json:"t"`
	Requests   int64   `json:"requests"` // per minute
	Errors     int64   `json:"errors"`
	BytesOut   int64   `json:"bytesOut"`
	BytesIn    int64   `json:"bytesIn"`
	CPU        float64 `json:"cpu"` // percent of one core
	MemBytes   uint64  `json:"mem"`
	Goroutines int     `json:"goroutines"`
	Conns      int64   `json:"conns"`
	ShareHits  int64   `json:"shareHits"`
	Load1      float64 `json:"load1"`
}

const keep = 24 * 60

func New() *Monitor {
	m := &Monitor{Started: time.Now(), lastAt: time.Now(), lastCPU: cpuSeconds()}
	go m.loop()
	return m
}

func (m *Monitor) loop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		m.sample()
	}
}

func (m *Monitor) totals() Sample {
	return Sample{
		Requests: m.Requests.Load(), Errors: m.Errors.Load(), BytesOut: m.BytesOut.Load(),
		BytesIn: m.BytesIn.Load(), ShareHits: m.ShareHits.Load(),
	}
}

func (m *Monitor) sample() {
	now := time.Now()
	cur := m.totals()
	cpu := cpuSeconds()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	m.mu.Lock()
	defer m.mu.Unlock()
	elapsed := now.Sub(m.lastAt).Seconds()
	s := Sample{
		T: now.Unix(), Requests: cur.Requests - m.last.Requests, Errors: cur.Errors - m.last.Errors,
		BytesOut: cur.BytesOut - m.last.BytesOut, BytesIn: cur.BytesIn - m.last.BytesIn,
		ShareHits: cur.ShareHits - m.last.ShareHits, MemBytes: ms.Sys, Goroutines: runtime.NumGoroutine(),
		Conns: m.Conns.Load(), Load1: load1(),
	}
	if elapsed > 0 {
		s.CPU = (cpu - m.lastCPU) / elapsed * 100
	}
	m.last, m.lastCPU, m.lastAt = cur, cpu, now
	m.samples = append(m.samples, s)
	if len(m.samples) > keep {
		m.samples = m.samples[len(m.samples)-keep:]
	}
}

// Snapshot is the current state plus history.
type Snapshot struct {
	Uptime     int64      `json:"uptime"`
	GoVersion  string     `json:"goVersion"`
	CPUs       int        `json:"cpus"`
	MemBytes   uint64     `json:"mem"`
	HeapBytes  uint64     `json:"heap"`
	Goroutines int        `json:"goroutines"`
	Conns      int64      `json:"conns"`
	Load       [3]float64 `json:"load"`
	Totals     Sample     `json:"totals"`
	History    []Sample   `json:"history"`
}

func (m *Monitor) Snapshot() Snapshot {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	m.mu.Lock()
	hist := append([]Sample(nil), m.samples...)
	m.mu.Unlock()
	return Snapshot{
		Uptime: int64(time.Since(m.Started).Seconds()), GoVersion: runtime.Version(), CPUs: runtime.NumCPU(),
		MemBytes: ms.Sys, HeapBytes: ms.HeapAlloc, Goroutines: runtime.NumGoroutine(), Conns: m.Conns.Load(),
		Load: loadAvg(), Totals: m.totals(), History: hist,
	}
}

// WritePrometheus writes metrics in the Prometheus text format. extra lets
// the caller add gauges (name -> value).
func (m *Monitor) WritePrometheus(w io.Writer, extra map[string]float64) {
	s := m.Snapshot()
	line := func(name, typ, help string, v float64) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n%s %g\n", name, help, name, typ, name, v)
	}
	line("coffer_uptime_seconds", "gauge", "Seconds since start.", float64(s.Uptime))
	line("coffer_http_requests_total", "counter", "HTTP requests served.", float64(s.Totals.Requests))
	line("coffer_http_errors_total", "counter", "HTTP 5xx responses.", float64(s.Totals.Errors))
	line("coffer_http_client_errors_total", "counter", "HTTP 4xx responses.", float64(m.ClientErrs.Load()))
	line("coffer_bytes_sent_total", "counter", "Response bytes sent.", float64(s.Totals.BytesOut))
	line("coffer_bytes_received_total", "counter", "Upload bytes received.", float64(s.Totals.BytesIn))
	line("coffer_share_requests_total", "counter", "Public share requests.", float64(s.Totals.ShareHits))
	line("coffer_webdav_requests_total", "counter", "WebDAV requests.", float64(m.DavReqs.Load()))
	line("coffer_open_connections", "gauge", "Open client connections.", float64(s.Conns))
	line("coffer_memory_bytes", "gauge", "Memory obtained from the OS.", float64(s.MemBytes))
	line("coffer_goroutines", "gauge", "Running goroutines.", float64(s.Goroutines))
	for k, v := range extra {
		line(k, "gauge", k, v)
	}
}

func cpuSeconds() float64 {
	b, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	// Fields after the ")" of the command name; utime and stime are 14 and 15.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 13 {
		return 0
	}
	ut, _ := strconv.ParseFloat(f[11], 64)
	st, _ := strconv.ParseFloat(f[12], 64)
	return (ut + st) / 100 // USER_HZ is 100 on Linux
}

func loadAvg() [3]float64 {
	var out [3]float64
	f, err := os.Open("/proc/loadavg")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if sc.Scan() {
		parts := strings.Fields(sc.Text())
		for i := 0; i < 3 && i < len(parts); i++ {
			out[i], _ = strconv.ParseFloat(parts[i], 64)
		}
	}
	return out
}

func load1() float64 { return loadAvg()[0] }
