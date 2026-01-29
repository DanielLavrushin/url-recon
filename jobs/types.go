package jobs

import (
	"time"

	"github.com/DanielLavrushin/url-recon/scanner"
)

type JobStatus string

const (
	JobStatusQueued    JobStatus = "queued"
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
)

type Job struct {
	ID            string          `json:"id"`
	URL           string          `json:"url"`
	VisitorIP     string          `json:"-"`
	Status        JobStatus       `json:"status"`
	QueuePosition int             `json:"queue_position,omitempty"`
	Progress      *Progress       `json:"progress,omitempty"`
	Result        *scanner.Result `json:"result,omitempty"`
	Error         string          `json:"error,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	StartedAt     *time.Time      `json:"started_at,omitempty"`
	EndedAt       *time.Time      `json:"ended_at,omitempty"`
}

type Progress struct {
	Stage   string `json:"stage"`
	Current int    `json:"current"`
	Total   int    `json:"total"`
}

type CreateJobRequest struct {
	URL string `json:"url"`
}

type CreateJobResponse struct {
	JobID         string    `json:"job_id"`
	Status        JobStatus `json:"status"`
	QueuePosition int       `json:"queue_position,omitempty"`
	Message       string    `json:"message"`
}

type QueueStatsResponse struct {
	Running       int `json:"running"`
	Queued        int `json:"queued"`
	MaxConcurrent int `json:"max_concurrent"`
}

type ErrorResponse struct {
	Error       string `json:"error"`
	Code        string `json:"code,omitempty"`
	ActiveJobID string `json:"active_job_id,omitempty"`
}
