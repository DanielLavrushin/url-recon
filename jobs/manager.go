package jobs

import (
	"context"
	"sync"
	"time"

	"github.com/DanielLavrushin/url-recon/scanner"
	"github.com/google/uuid"
)

// ProgressSubscriber is a channel that receives progress updates
type ProgressSubscriber chan *Progress

// Manager handles job lifecycle, storage, and visitor tracking
type Manager struct {
	mu           sync.RWMutex
	jobs         map[string]*Job    // jobID -> Job
	visitorJobs  map[string]string  // visitorIP -> active jobID
	subscribers  map[string][]ProgressSubscriber
	subscriberMu sync.RWMutex

	jobTTL          time.Duration
	cleanupInterval time.Duration
}

// NewManager creates a new job manager with cleanup routine
func NewManager(jobTTL, cleanupInterval time.Duration) *Manager {
	m := &Manager{
		jobs:            make(map[string]*Job),
		visitorJobs:     make(map[string]string),
		subscribers:     make(map[string][]ProgressSubscriber),
		jobTTL:          jobTTL,
		cleanupInterval: cleanupInterval,
	}
	go m.cleanupLoop()
	return m
}

// CreateJob creates a new pending job for a visitor
// Returns error if visitor already has an active job
func (m *Manager) CreateJob(visitorIP, url string) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if visitor has an active job
	if activeJobID, exists := m.visitorJobs[visitorIP]; exists {
		activeJob := m.jobs[activeJobID]
		if activeJob != nil && (activeJob.Status == JobStatusPending || activeJob.Status == JobStatusRunning) {
			return nil, &ActiveJobError{JobID: activeJobID}
		}
	}

	// Create new job
	job := &Job{
		ID:        uuid.New().String(),
		URL:       url,
		VisitorIP: visitorIP,
		Status:    JobStatusPending,
		CreatedAt: time.Now(),
	}

	m.jobs[job.ID] = job
	m.visitorJobs[visitorIP] = job.ID

	return job, nil
}

// GetJob retrieves a job by ID
func (m *Manager) GetJob(jobID string) (*Job, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job, exists := m.jobs[jobID]
	return job, exists
}

// StartJob starts executing a job in the background
func (m *Manager) StartJob(jobID string) error {
	m.mu.Lock()
	job, exists := m.jobs[jobID]
	if !exists {
		m.mu.Unlock()
		return &JobNotFoundError{JobID: jobID}
	}

	now := time.Now()
	job.Status = JobStatusRunning
	job.StartedAt = &now
	m.mu.Unlock()

	// Run scanner in goroutine with background context
	go m.executeJob(context.Background(), job)
	return nil
}

// executeJob runs the scan and updates job state
func (m *Manager) executeJob(ctx context.Context, job *Job) {
	onProgress := func(stage string, current, total int) {
		m.mu.Lock()
		job.Progress = &Progress{
			Stage:   stage,
			Current: current,
			Total:   total,
		}
		progress := *job.Progress // Copy for notification
		m.mu.Unlock()

		// Notify subscribers
		m.notifySubscribers(job.ID, &progress)
	}

	result, err := scanner.Scan(ctx, job.URL, onProgress)

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	job.EndedAt = &now
	job.Progress = nil

	if err != nil {
		job.Status = JobStatusFailed
		job.Error = err.Error()
	} else {
		job.Status = JobStatusCompleted
		job.Result = result
	}

	// Notify subscribers of completion and close channels
	m.closeSubscribers(job.ID)
}

// Subscribe adds a subscriber for job progress updates
func (m *Manager) Subscribe(jobID string) (ProgressSubscriber, func()) {
	m.subscriberMu.Lock()
	defer m.subscriberMu.Unlock()

	ch := make(chan *Progress, 10)
	m.subscribers[jobID] = append(m.subscribers[jobID], ch)

	// Return unsubscribe function
	unsubscribe := func() {
		m.subscriberMu.Lock()
		defer m.subscriberMu.Unlock()
		subs := m.subscribers[jobID]
		for i, sub := range subs {
			if sub == ch {
				m.subscribers[jobID] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
	}

	return ch, unsubscribe
}

// notifySubscribers sends progress to all subscribers
func (m *Manager) notifySubscribers(jobID string, progress *Progress) {
	m.subscriberMu.RLock()
	defer m.subscriberMu.RUnlock()

	for _, sub := range m.subscribers[jobID] {
		select {
		case sub <- progress:
		default:
			// Channel full, skip
		}
	}
}

// closeSubscribers closes all subscriber channels for a job
func (m *Manager) closeSubscribers(jobID string) {
	m.subscriberMu.Lock()
	defer m.subscriberMu.Unlock()

	for _, sub := range m.subscribers[jobID] {
		close(sub)
	}
	delete(m.subscribers, jobID)
}

// cleanupLoop periodically removes old completed jobs
func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(m.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		m.cleanup()
	}
}

func (m *Manager) cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := time.Now().Add(-m.jobTTL)
	for id, job := range m.jobs {
		// Only clean up completed/failed jobs older than TTL
		if (job.Status == JobStatusCompleted || job.Status == JobStatusFailed) &&
			job.EndedAt != nil && job.EndedAt.Before(cutoff) {
			delete(m.jobs, id)
			// Also remove from visitor tracking if this was their last job
			if m.visitorJobs[job.VisitorIP] == id {
				delete(m.visitorJobs, job.VisitorIP)
			}
		}
	}
}

// GetActiveJobForVisitor returns the active job ID for a visitor, if any
func (m *Manager) GetActiveJobForVisitor(visitorIP string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	jobID, exists := m.visitorJobs[visitorIP]
	if !exists {
		return "", false
	}

	job := m.jobs[jobID]
	if job == nil || (job.Status != JobStatusPending && job.Status != JobStatusRunning) {
		return "", false
	}

	return jobID, true
}
