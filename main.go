package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/DanielLavrushin/url-recon/jobs"
	"github.com/DanielLavrushin/url-recon/scanner"
)

const (
	// MaxRequestBodySize limits POST body to 1KB (URLs shouldn't be larger)
	MaxRequestBodySize = 1024
)

//go:embed http/ui/dist/*
var uiFS embed.FS

var jobManager *jobs.Manager

var port int

func main() {
	flag.IntVar(&port, "port", 8080, "HTTP server port")
	flag.Parse()

	// Load ASN database at startup
	if err := scanner.LoadIPRanges(); err != nil {
		log.Fatalf("Failed to load IP-to-ASN database: %v", err)
	}
	log.Printf("Loaded %d CDN IP ranges", scanner.GetLoadedRangeCount())

	// Initialize job manager with 1-hour TTL and 5-minute cleanup interval
	jobManager = jobs.NewManager(1*time.Hour, 5*time.Minute)

	// API endpoints with CSRF protection
	http.HandleFunc("/api/jobs", csrfProtection(handleJobs))     // POST to create
	http.HandleFunc("/api/jobs/", handleJobByID)                 // GET /:id and GET /:id/stream
	http.HandleFunc("/api/queue/stats", handleQueueStats)        // GET queue stats

	// Serve the embedded frontend
	distFS, err := fs.Sub(uiFS, "http/ui/dist")
	if err != nil {
		log.Fatal(err)
	}

	// Wrap everything with security headers
	handler := securityHeaders(http.DefaultServeMux)
	http.Handle("/", http.FileServer(http.FS(distFS)))

	log.Println("Security: TRUST_PROXY =", trustProxy)

	// Initialize browser pool
	_ = scanner.GetPool()
	log.Println("Browser pool initialized")

	// Create server for graceful shutdown
	addr := fmt.Sprintf(":%d", port)
	server := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	// Handle shutdown signals
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		log.Println("Shutting down server...")

		// Shutdown HTTP server with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(ctx)

		// Shutdown browser pool
		scanner.ShutdownPool()
		log.Println("Cleanup complete")
	}()

	log.Printf("Server starting on http://localhost:%d", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// trustProxy controls whether X-Forwarded-For and X-Real-IP headers are trusted.
// Set TRUST_PROXY=true environment variable only if running behind a trusted reverse proxy.
var trustProxy = os.Getenv("TRUST_PROXY") == "true"

// getVisitorIP extracts the visitor's IP address.
// Only trusts proxy headers if TRUST_PROXY=true environment variable is set.
func getVisitorIP(r *http.Request) string {
	// Only trust proxy headers if explicitly configured
	if trustProxy {
		// Check X-Forwarded-For header (for proxies/load balancers)
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Take the first IP (original client)
			parts := strings.Split(xff, ",")
			ip := strings.TrimSpace(parts[0])
			// Validate it's actually an IP address
			if parsedIP := net.ParseIP(ip); parsedIP != nil {
				return ip
			}
		}

		// Check X-Real-IP header
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			if parsedIP := net.ParseIP(xri); parsedIP != nil {
				return xri
			}
		}
	}

	// Use RemoteAddr (most reliable when not behind proxy)
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr might not have port in some cases
		return r.RemoteAddr
	}
	return host
}

// securityHeaders adds security headers to responses
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent MIME type sniffing
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Prevent clickjacking
		w.Header().Set("X-Frame-Options", "DENY")
		// XSS protection (legacy but still useful)
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		// Referrer policy
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Content Security Policy
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'")

		next.ServeHTTP(w, r)
	})
}

// csrfProtection validates Origin/Referer headers for state-changing requests
func csrfProtection(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Only check POST, PUT, DELETE, PATCH
		if r.Method == http.MethodPost || r.Method == http.MethodPut ||
			r.Method == http.MethodDelete || r.Method == http.MethodPatch {

			origin := r.Header.Get("Origin")
			referer := r.Header.Get("Referer")

			// At least one must be present for browser requests
			if origin == "" && referer == "" {
				// Could be a non-browser client (curl, etc.) - allow but log
				log.Printf("CSRF warning: request without Origin/Referer from %s", getVisitorIP(r))
			} else {
				// Validate Origin if present
				if origin != "" {
					// For same-origin requests, Origin should match our host
					// For cross-origin, this will block unless we add CORS
					host := r.Host
					if !strings.HasSuffix(origin, "://"+host) &&
						!strings.HasSuffix(origin, fmt.Sprintf("://localhost:%d", port)) &&
						origin != "null" { // null origin for file:// or data:// - block these
						writeJSON(w, http.StatusForbidden, jobs.ErrorResponse{
							Error: "Cross-origin requests not allowed",
							Code:  "CSRF_BLOCKED",
						})
						return
					}
				}
			}
		}
		next(w, r)
	}
}

// handleJobs handles POST /api/jobs to create a new job
func handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body size to prevent DoS
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBodySize)

	var req jobs.CreateJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err.Error() == "http: request body too large" {
			writeJSON(w, http.StatusRequestEntityTooLarge, jobs.ErrorResponse{Error: "Request body too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, jobs.ErrorResponse{Error: "Invalid request body"})
		return
	}

	// Validate URL using security module (includes SSRF protection)
	url, err := scanner.ValidateURL(req.URL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, jobs.ErrorResponse{
			Error: "Invalid URL: " + err.Error(),
			Code:  "INVALID_URL",
		})
		return
	}

	visitorIP := getVisitorIP(r)

	job, err := jobManager.CreateJob(visitorIP, url)
	if err != nil {
		if activeErr, ok := err.(*jobs.ActiveJobError); ok {
			writeJSON(w, http.StatusConflict, jobs.ErrorResponse{
				Error:       "You already have an active scan job",
				Code:        "ACTIVE_JOB_EXISTS",
				ActiveJobID: activeErr.JobID,
			})
			return
		}
		log.Printf("Error creating job: %v", err)
		writeJSON(w, http.StatusInternalServerError, jobs.ErrorResponse{Error: "Failed to create job"})
		return
	}

	// Start the job in the background
	if err := jobManager.StartJob(job.ID); err != nil {
		log.Printf("Error starting job: %v", err)
		writeJSON(w, http.StatusInternalServerError, jobs.ErrorResponse{Error: "Failed to start job"})
		return
	}

	message := "Job created successfully"
	if job.QueuePosition > 0 {
		message = fmt.Sprintf("Job queued at position %d", job.QueuePosition)
	}

	writeJSON(w, http.StatusCreated, jobs.CreateJobResponse{
		JobID:         job.ID,
		Status:        job.Status,
		QueuePosition: job.QueuePosition,
		Message:       message,
	})
}

// handleQueueStats returns current queue statistics
func handleQueueStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	running, queued, maxConcurrent := jobManager.GetQueueStats()
	writeJSON(w, http.StatusOK, jobs.QueueStatsResponse{
		Running:       running,
		Queued:        queued,
		MaxConcurrent: maxConcurrent,
	})
}

// handleJobByID handles GET /api/jobs/:id and GET /api/jobs/:id/stream
func handleJobByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse job ID from path: /api/jobs/{id} or /api/jobs/{id}/stream
	path := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	parts := strings.Split(path, "/")
	jobID := parts[0]
	isStream := len(parts) > 1 && parts[1] == "stream"

	if jobID == "" {
		writeJSON(w, http.StatusBadRequest, jobs.ErrorResponse{Error: "Job ID is required"})
		return
	}

	if isStream {
		handleJobStream(w, r, jobID)
		return
	}

	job, exists := jobManager.GetJob(jobID)
	if !exists {
		writeJSON(w, http.StatusNotFound, jobs.ErrorResponse{Error: "Job not found"})
		return
	}

	writeJSON(w, http.StatusOK, job)
}

// handleJobStream handles SSE streaming for job progress
func handleJobStream(w http.ResponseWriter, r *http.Request, jobID string) {
	job, exists := jobManager.GetJob(jobID)
	if !exists {
		writeJSON(w, http.StatusNotFound, jobs.ErrorResponse{Error: "Job not found"})
		return
	}

	// If job is already completed or failed, return status immediately
	if job.Status == jobs.JobStatusCompleted || job.Status == jobs.JobStatusFailed {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "SSE not supported", http.StatusInternalServerError)
			return
		}

		sendSSE(w, flusher, "complete", map[string]interface{}{
			"status": job.Status,
			"error":  job.Error,
		})
		return
	}

	// Set up SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Subscribe to progress updates
	progressCh, unsubscribe := jobManager.Subscribe(jobID)
	defer unsubscribe()

	// Send current progress immediately if available
	if job.Progress != nil {
		sendSSE(w, flusher, "progress", job.Progress)
	}

	// Listen for updates
	for {
		select {
		case progress, ok := <-progressCh:
			if !ok {
				// Channel closed, job finished
				updatedJob, _ := jobManager.GetJob(jobID)
				if updatedJob != nil {
					sendSSE(w, flusher, "complete", map[string]interface{}{
						"status": updatedJob.Status,
						"error":  updatedJob.Error,
					})
				}
				return
			}
			if progress != nil {
				sendSSE(w, flusher, "progress", progress)
			}
		case <-r.Context().Done():
			// Client disconnected
			return
		}
	}
}

func sendSSE(w http.ResponseWriter, flusher http.Flusher, event string, data interface{}) {
	jsonData, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, jsonData)
	flusher.Flush()
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
