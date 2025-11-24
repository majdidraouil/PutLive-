package main

import (
	"net/http"
	"runtime"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "putlive_http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "endpoint", "status"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "putlive_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "endpoint"},
	)

	streamActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "putlive_stream_active",
			Help: "Whether a stream is currently active (1) or not (0)",
		},
	)

	streamViewers = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "putlive_stream_viewers_total",
			Help: "Current number of stream viewers",
		},
	)

	ffmpegProcesses = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "putlive_ffmpeg_processes_running",
			Help: "Number of FFmpeg processes currently running",
		},
	)

	apiUptime = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "putlive_api_uptime_seconds",
			Help: "API uptime in seconds",
		},
	)

	activeConnections int64
)

func initMetrics() {
	// Start background metric collectors
	go collectSystemMetrics()
}

func collectSystemMetrics() {
	for {
		// Update API uptime
		apiUptime.Inc()

		// Collect Go runtime metrics
		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		// Update metrics would go here
		// (Prometheus automatically exposes Go runtime metrics)

		time.Sleep(15 * time.Second)
	}
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"timestamp": time.Now(),
		"cpu": map[string]interface{}{
			"goroutines": runtime.NumGoroutine(),
			"cores":      runtime.NumCPU(),
		},
		"memory": map[string]interface{}{
			"alloc_mb":       float64(m.Alloc) / (1024 * 1024),
			"total_alloc_mb": float64(m.TotalAlloc) / (1024 * 1024),
			"sys_mb":         float64(m.Sys) / (1024 * 1024),
		},
		"connections": map[string]interface{}{
			"active": atomic.LoadInt64(&activeConnections),
		},
	})
}
