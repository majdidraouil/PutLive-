package main

import (
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

type HealthResponse struct {
	Status    string                 `json:"status"`
	Version   string                 `json:"version"`
	Uptime    int64                  `json:"uptime_seconds"`
	Timestamp time.Time              `json:"timestamp"`
	Components map[string]interface{} `json:"components,omitempty"`
}

var startTime = time.Now()

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	uptime := int64(time.Since(startTime).Seconds())

	respondJSON(w, http.StatusOK, HealthResponse{
		Status:    "ok",
		Version:   "3.0",
		Uptime:    uptime,
		Timestamp: time.Now(),
	})
}

func handleDetailedHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	components := make(map[string]interface{})
	overallStatus := "ok"

	// Check SRS
	srsStatus := checkSRS()
	components["srs"] = srsStatus
	if srsStatus["status"] != "ok" {
		overallStatus = "unhealthy"
	}

	// Check database
	dbStatus := checkDatabase()
	components["database"] = dbStatus
	if dbStatus["status"] != "ok" {
		overallStatus = "unhealthy"
	}

	// Check disk space
	diskStatus := checkDisk()
	components["disk"] = diskStatus
	if diskStatus["status"] != "ok" {
		overallStatus = "warning"
	}

	// Check memory
	memStatus := checkMemory()
	components["memory"] = memStatus

	statusCode := http.StatusOK
	if overallStatus == "unhealthy" {
		statusCode = http.StatusServiceUnavailable
	}

	respondJSON(w, statusCode, HealthResponse{
		Status:     overallStatus,
		Version:    "3.0",
		Uptime:     int64(time.Since(startTime).Seconds()),
		Timestamp:  time.Now(),
		Components: components,
	})
}

func checkSRS() map[string]interface{} {
	// Check if SRS process is running
	cmd := exec.Command("pgrep", "-x", "srs")
	if err := cmd.Run(); err != nil {
		return map[string]interface{}{
			"status": "down",
			"error":  "Process not running",
		}
	}

	// Check SRS HTTP API
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:1985/api/v1/versions")
	if err != nil {
		return map[string]interface{}{
			"status": "down",
			"error":  "API not responding",
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return map[string]interface{}{
			"status": "degraded",
			"error":  "API returned non-200 status",
		}
	}

	return map[string]interface{}{
		"status": "ok",
	}
}

func checkDatabase() map[string]interface{} {
	if err := app.DB.Ping(); err != nil {
		return map[string]interface{}{
			"status": "down",
			"error":  err.Error(),
		}
	}

	// Get database size
	var dbSize int64
	if stat, err := os.Stat(app.Config.Database.Path); err == nil {
		dbSize = stat.Size()
	}

	return map[string]interface{}{
		"status":   "ok",
		"size_mb":  float64(dbSize) / (1024 * 1024),
	}
}

func checkDisk() map[string]interface{} {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return map[string]interface{}{
			"status": "unknown",
			"error":  err.Error(),
		}
	}

	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bfree * uint64(stat.Bsize)
	used := total - free
	usagePercent := float64(used) / float64(total) * 100

	status := "ok"
	if usagePercent > 90 {
		status = "critical"
	} else if usagePercent > 80 {
		status = "warning"
	}

	return map[string]interface{}{
		"status":         status,
		"usage_percent":  usagePercent,
		"available_gb":   float64(free) / (1024 * 1024 * 1024),
		"total_gb":       float64(total) / (1024 * 1024 * 1024),
	}
}

func checkMemory() map[string]interface{} {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return map[string]interface{}{
		"alloc_mb":       float64(m.Alloc) / (1024 * 1024),
		"total_alloc_mb": float64(m.TotalAlloc) / (1024 * 1024),
		"sys_mb":         float64(m.Sys) / (1024 * 1024),
		"num_gc":         m.NumGC,
	}
}
