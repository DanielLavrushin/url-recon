package jobs

import (
	"time"

	"github.com/DanielLavrushin/url-recon/scanner"
)

// JobStatus represents the current state of a job
type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
)

// Job represents a scan job with its full lifecycle state
type Job struct {
	ID        string          `json:"id"`
	URL       string          `json:"url"`
	VisitorIP string          `json:"-"` // Don't expose in API responses
	Status    JobStatus       `json:"status"`
	Progress  *Progress       `json:"progress,omitempty"`
	Result    *scanner.Result `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	StartedAt *time.Time      `json:"started_at,omitempty"`
	EndedAt   *time.Time      `json:"ended_at,omitempty"`
}

// Progress represents scan progress state
type Progress struct {
	Stage   string `json:"stage"`
	Current int    `json:"current"`
	Total   int    `json:"total"`
}

// CreateJobRequest is the request body for creating a new job
type CreateJobRequest struct {
	URL string `json:"url"`
}

// CreateJobResponse is returned when a job is successfully created
type CreateJobResponse struct {
	JobID   string    `json:"job_id"`
	Status  JobStatus `json:"status"`
	Message string    `json:"message"`
}

// ErrorResponse represents an API error
type ErrorResponse struct {
	Error       string `json:"error"`
	Code        string `json:"code,omitempty"`
	ActiveJobID string `json:"active_job_id,omitempty"`
}
