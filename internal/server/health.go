package server

import (
	"encoding/json"
	"log"
	"net/http"
	"runtime"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// HealthStatus represents the health check response
type HealthStatus struct {
	Status           string      `json:"status"`
	Version          string      `json:"version"`
	Uptime           string      `json:"uptime"`
	CacheStatus      string      `json:"cacheStatus"`
	BackendStatus    string      `json:"backendStatus"`
	SystemInfo       SystemInfo  `json:"systemInfo"`
	LatestRequests   int         `json:"latestRequests"`
	CacheStats       interface{} `json:"cacheStats,omitempty"`
	BackendStats     interface{} `json:"backendStats,omitempty"`
	HitRate          float64     `json:"hitRate"`
	LastErrorMessage string      `json:"lastErrorMessage,omitempty"`
}

// SystemInfo contains system-level information
type SystemInfo struct {
	NumGoroutine    int              `json:"numGoroutine"`
	NumCPU          int              `json:"numCPU"`
	MemAllocMB      float64          `json:"memAllocMB"`
	MemTotalAllocMB float64          `json:"memTotalAllocMB"`
	MemSysMB        float64          `json:"memSysMB"`
	GCPauseMS       float64          `json:"gcPauseMS"`
	MemStats        runtime.MemStats `json:"-"` // Not exported to JSON
}

// handleHealth serves the health check endpoint
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Detailed parameter determines whether to include detailed stats
	detailed := r.URL.Query().Get("detailed") == "true"

	// Get cache stats
	cacheStats, err := s.cache.GetStats()
	cacheStatus := "ok"
	if err != nil {
		cacheStatus = "error"
	}

	// Get basic metrics
	metrics := s.metrics.GetStatistics()

	// Get system info
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	sysInfo := SystemInfo{
		NumGoroutine:    runtime.NumGoroutine(),
		NumCPU:          runtime.NumCPU(),
		MemAllocMB:      float64(memStats.Alloc) / (1024 * 1024),
		MemTotalAllocMB: float64(memStats.TotalAlloc) / (1024 * 1024),
		MemSysMB:        float64(memStats.Sys) / (1024 * 1024),
		GCPauseMS:       float64(memStats.PauseNs[(memStats.NumGC+255)%256]) / 1000000,
		MemStats:        memStats,
	}

	// Create health status response
	status := HealthStatus{
		Status:         "ok",
		Version:        "1.0.0", // Replace with actual version
		Uptime:         time.Since(s.startTime).String(),
		CacheStatus:    cacheStatus,
		BackendStatus:  "ok", // Simplified - could check backend health
		SystemInfo:     sysInfo,
		LatestRequests: metrics.TotalRequests,
		HitRate:        metrics.HitRate,
	}

	// Include detailed stats if requested
	if detailed {
		status.CacheStats = cacheStats
		status.BackendStats = map[string]interface{}{
			"recentRequests": metrics.RecentRequests,
		}
	}

	// Return as JSON
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(status); err != nil {
		log.Printf("Error encoding health status JSON: %v", err)
	}
}

// handleMetrics serves Prometheus metrics
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	handler := promhttp.Handler()
	handler.ServeHTTP(w, r)
}
