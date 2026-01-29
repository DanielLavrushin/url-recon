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
	MaxRequestBodySize = 1024
)

//go:embed http/ui/dist
var uiFS embed.FS

var jobManager *jobs.Manager

var port int

func main() {
	flag.IntVar(&port, "port", 8080, "HTTP server port")
	flag.Parse()

	if err := scanner.LoadIPRanges(); err != nil {
		log.Fatalf("Failed to load IP-to-ASN database: %v", err)
	}
	log.Printf("Loaded %d CDN IP ranges", scanner.GetLoadedRangeCount())

	jobManager = jobs.NewManager(1*time.Hour, 5*time.Minute)

	http.HandleFunc("/api/jobs", csrfProtection(handleJobs))
	http.HandleFunc("/api/jobs/", handleJobByID)
	http.HandleFunc("/api/queue/stats", handleQueueStats)

	distFS, err := fs.Sub(uiFS, "http/ui/dist")
	if err != nil {
		log.Fatal(err)
	}

	handler := securityHeaders(http.DefaultServeMux)
	http.Handle("/", http.FileServer(http.FS(distFS)))

	log.Println("Security: TRUST_PROXY =", trustProxy)

	_ = scanner.GetPool()
	log.Println("Browser pool initialized")

	addr := fmt.Sprintf(":%d", port)
	server := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		log.Println("Shutting down server...")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(ctx)

		scanner.ShutdownPool()
		log.Println("Cleanup complete")
	}()

	log.Printf("Server starting on http://localhost:%d", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

var trustProxy = os.Getenv("TRUST_PROXY") == "true"

func getVisitorIP(r *http.Request) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			ip := strings.TrimSpace(parts[0])
			if parsedIP := net.ParseIP(ip); parsedIP != nil {
				return ip
			}
		}

		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			if parsedIP := net.ParseIP(xri); parsedIP != nil {
				return xri
			}
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'")

		next.ServeHTTP(w, r)
	})
}

func csrfProtection(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodPut ||
			r.Method == http.MethodDelete || r.Method == http.MethodPatch {

			origin := r.Header.Get("Origin")
			referer := r.Header.Get("Referer")

			if origin == "" && referer == "" {
				log.Printf("CSRF warning: request without Origin/Referer from %s", getVisitorIP(r))
			} else {
				if origin != "" {
					host := r.Host
					if !strings.HasSuffix(origin, "://"+host) &&
						!strings.HasSuffix(origin, fmt.Sprintf("://localhost:%d", port)) &&
						origin != "null" {
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

func handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

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

func handleJobByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

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

func handleJobStream(w http.ResponseWriter, r *http.Request, jobID string) {
	job, exists := jobManager.GetJob(jobID)
	if !exists {
		writeJSON(w, http.StatusNotFound, jobs.ErrorResponse{Error: "Job not found"})
		return
	}

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

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	progressCh, unsubscribe := jobManager.Subscribe(jobID)
	defer unsubscribe()

	if job.Progress != nil {
		sendSSE(w, flusher, "progress", job.Progress)
	}

	for {
		select {
		case progress, ok := <-progressCh:
			if !ok {
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
