package jobs

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/DanielLavrushin/url-recon/scanner"
	"github.com/google/uuid"
)

// ProgressSubscriber is a channel that receives progress updates
type ProgressSubscriber chan *Progress

// Default concurrency settings
const (
	DefaultMaxConcurrentJobs = 4
)

// Manager handles job lifecycle, storage, and visitor tracking
type Manager struct {
	mu           sync.RWMutex
	jobs         map[string]*Job   // jobID -> Job
	visitorJobs  map[string]string // visitorIP -> active jobID
	subscribers  map[string][]ProgressSubscriber
	subscriberMu sync.RWMutex

	// Queue management
	queue        []string // Queue of job IDs waiting to run
	queueMu      sync.Mutex
	runningCount int
	maxConcurrent int
	queueCond    *sync.Cond // Condition variable for queue processing

	jobTTL          time.Duration
	cleanupInterval time.Duration
}

// NewManager creates a new job manager with cleanup routine
func NewManager(jobTTL, cleanupInterval time.Duration) *Manager {
	return NewManagerWithConcurrency(jobTTL, cleanupInterval, DefaultMaxConcurrentJobs)
}

// NewManagerWithConcurrency creates a job manager with custom concurrency limit
func NewManagerWithConcurrency(jobTTL, cleanupInterval time.Duration, maxConcurrent int) *Manager {
	m := &Manager{
		jobs:            make(map[string]*Job),
		visitorJobs:     make(map[string]string),
		subscribers:     make(map[string][]ProgressSubscriber),
		queue:           make([]string, 0),
		maxConcurrent:   maxConcurrent,
		jobTTL:          jobTTL,
		cleanupInterval: cleanupInterval,
	}
	m.queueCond = sync.NewCond(&m.queueMu)

	go m.cleanupLoop()
	go m.queueProcessor()

	log.Printf("Job manager initialized: max %d concurrent jobs", maxConcurrent)
	return m
}

// CreateJob creates a new job for a visitor and adds it to the queue
// Returns error if visitor already has an active job
func (m *Manager) CreateJob(visitorIP, url string) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if visitor has an active job (queued, pending, or running)
	if activeJobID, exists := m.visitorJobs[visitorIP]; exists {
		activeJob := m.jobs[activeJobID]
		if activeJob != nil && (activeJob.Status == JobStatusQueued ||
			activeJob.Status == JobStatusPending ||
			activeJob.Status == JobStatusRunning) {
			return nil, &ActiveJobError{JobID: activeJobID}
		}
	}

	// Create new job in queued state
	job := &Job{
		ID:        uuid.New().String(),
		URL:       url,
		VisitorIP: visitorIP,
		Status:    JobStatusQueued,
		CreatedAt: time.Now(),
	}

	m.jobs[job.ID] = job
	m.visitorJobs[visitorIP] = job.ID

	// Add to queue
	m.enqueueJob(job.ID)

	return job, nil
}

// enqueueJob adds a job to the queue and signals the processor
func (m *Manager) enqueueJob(jobID string) {
	m.queueMu.Lock()
	defer m.queueMu.Unlock()

	m.queue = append(m.queue, jobID)
	m.updateQueuePositions()
	m.queueCond.Signal() // Wake up queue processor
}

// updateQueuePositions updates the QueuePosition field for all queued jobs
func (m *Manager) updateQueuePositions() {
	for i, jobID := range m.queue {
		if job, exists := m.jobs[jobID]; exists {
			job.QueuePosition = i + 1 // 1-indexed position
		}
	}
}

// queueProcessor runs in the background and starts jobs when slots are available
func (m *Manager) queueProcessor() {
	for {
		m.queueMu.Lock()

		// Wait until there's a job in queue AND a free slot
		for len(m.queue) == 0 || m.runningCount >= m.maxConcurrent {
			m.queueCond.Wait()
		}

		// Get next job from queue
		jobID := m.queue[0]
		m.queue = m.queue[1:]
		m.runningCount++

		// Update queue positions for remaining jobs
		m.updateQueuePositions()

		m.queueMu.Unlock()

		// Start the job
		m.startJobExecution(jobID)
	}
}

// startJobExecution begins executing a job (called by queue processor)
func (m *Manager) startJobExecution(jobID string) {
	m.mu.Lock()
	job, exists := m.jobs[jobID]
	if !exists {
		m.mu.Unlock()
		m.jobFinished() // Release the slot
		return
	}

	now := time.Now()
	job.Status = JobStatusRunning
	job.QueuePosition = 0 // No longer in queue
	job.StartedAt = &now
	m.mu.Unlock()

	// Notify subscribers of status change
	m.notifySubscribers(jobID, &Progress{Stage: "Starting", Current: 0, Total: 0})

	// Run scanner in goroutine
	go m.executeJob(context.Background(), job)
}

// jobFinished is called when a job completes to release the slot
func (m *Manager) jobFinished() {
	m.queueMu.Lock()
	m.runningCount--
	m.queueCond.Signal() // Wake up queue processor for next job
	m.queueMu.Unlock()
}

// GetJob retrieves a job by ID
func (m *Manager) GetJob(jobID string) (*Job, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job, exists := m.jobs[jobID]
	return job, exists
}

// StartJob is kept for API compatibility - jobs are auto-started by the queue
// This now just verifies the job exists and is queued
func (m *Manager) StartJob(jobID string) error {
	m.mu.RLock()
	job, exists := m.jobs[jobID]
	m.mu.RUnlock()

	if !exists {
		return &JobNotFoundError{JobID: jobID}
	}

	// Job is already in queue and will be processed automatically
	if job.Status == JobStatusQueued {
		log.Printf("Job %s queued at position %d", jobID, job.QueuePosition)
		return nil
	}

	return nil
}

// executeJob runs the scan and updates job state
func (m *Manager) executeJob(ctx context.Context, job *Job) {
	// Ensure we release the slot when done
	defer m.jobFinished()

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

	log.Printf("Job %s finished with status: %s", job.ID, job.Status)

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
	if job == nil || (job.Status != JobStatusQueued &&
		job.Status != JobStatusPending &&
		job.Status != JobStatusRunning) {
		return "", false
	}

	return jobID, true
}

// GetQueueStats returns current queue statistics
func (m *Manager) GetQueueStats() (running, queued, maxConcurrent int) {
	m.queueMu.Lock()
	defer m.queueMu.Unlock()
	return m.runningCount, len(m.queue), m.maxConcurrent
}
