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
	Status           string     `json:"status"`
	Version          string     `json:"version"`
	Uptime           string     `json:"uptime"`
	CacheStatus      string     `json:"cacheStatus"`
	BackendStatus    string     `json:"backendStatus"`
	SystemInfo       SystemInfo `json:"systemInfo"`
	LatestRequests   int        `json:"latestRequests"`
	CacheStats       any        `json:"cacheStats,omitempty"`
	BackendStats     any        `json:"backendStats,omitempty"`
	HitRate          float64    `json:"hitRate"`
	LastErrorMessage string     `json:"lastErrorMessage,omitempty"`
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

// HandleHealth is the exported version of handleHealth
func (s *Server) HandleHealth(w http.ResponseWriter, r *http.Request) {
	s.handleHealth(w, r)
}

// HandleReady is the exported version of handleReady
func (s *Server) HandleReady(w http.ResponseWriter, r *http.Request) {
	s.handleReady(w, r)
}

// HandleMetrics is the exported version of handleMetrics
func (s *Server) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	s.handleMetrics(w, r)
}

// handleHealth serves the health check endpoint
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Log.Debug.TraceHTTPRequests {
		log.Printf("[HTTP TRACE] Health check requested from %s", r.RemoteAddr)
	}

	// Detailed parameter determines whether to include detailed stats
	detailed := r.URL.Query().Get("detailed") == "true"

	// Get cache stats with mutex protection
	s.mutex.Lock()
	cacheStats := s.cache.GetStats() // No error to handle with the new interface
	cacheStatus := "ok"

	// Get basic metrics
	metrics := s.metrics.GetStatistics()
	s.mutex.Unlock()

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
		Version:        s.version,
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
		status.BackendStats = map[string]any{
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
	if s.cfg.Log.Debug.TraceHTTPRequests {
		log.Printf("[HTTP TRACE] Metrics requested from %s", r.RemoteAddr)
	}

	// Use mutex when accessing the metrics handler
	s.mutex.Lock()
	handler := promhttp.Handler()
	s.mutex.Unlock()

	handler.ServeHTTP(w, r)
}

// handleReady serves the readiness check endpoint
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Log.Debug.TraceHTTPRequests {
		log.Printf("[HTTP TRACE] Readiness check requested from %s", r.RemoteAddr)
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte(`{"status":"ready"}`)); err != nil {
		log.Printf("Error writing ready response: %v", err)
	}
}
