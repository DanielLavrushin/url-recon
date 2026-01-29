package jobs

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/DanielLavrushin/url-recon/scanner"
	"github.com/google/uuid"
)

type ProgressSubscriber chan *Progress

const (
	DefaultMaxConcurrentJobs = 4
)

type Manager struct {
	mu           sync.RWMutex
	jobs         map[string]*Job
	visitorJobs  map[string]string
	subscribers  map[string][]ProgressSubscriber
	subscriberMu sync.RWMutex

	queue         []string
	queueMu       sync.Mutex
	runningCount  int
	maxConcurrent int
	queueCond     *sync.Cond

	jobTTL          time.Duration
	cleanupInterval time.Duration
}

func NewManager(jobTTL, cleanupInterval time.Duration) *Manager {
	return NewManagerWithConcurrency(jobTTL, cleanupInterval, DefaultMaxConcurrentJobs)
}

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

func (m *Manager) CreateJob(visitorIP, url string) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if activeJobID, exists := m.visitorJobs[visitorIP]; exists {
		activeJob := m.jobs[activeJobID]
		if activeJob != nil && (activeJob.Status == JobStatusQueued ||
			activeJob.Status == JobStatusPending ||
			activeJob.Status == JobStatusRunning) {
			return nil, &ActiveJobError{JobID: activeJobID}
		}
	}

	job := &Job{
		ID:        uuid.New().String(),
		URL:       url,
		VisitorIP: visitorIP,
		Status:    JobStatusQueued,
		CreatedAt: time.Now(),
	}

	m.jobs[job.ID] = job
	m.visitorJobs[visitorIP] = job.ID

	m.enqueueJob(job.ID)

	return job, nil
}

func (m *Manager) enqueueJob(jobID string) {
	m.queueMu.Lock()
	defer m.queueMu.Unlock()

	m.queue = append(m.queue, jobID)
	m.updateQueuePositions()
	m.queueCond.Signal()
}

func (m *Manager) updateQueuePositions() {
	for i, jobID := range m.queue {
		if job, exists := m.jobs[jobID]; exists {
			job.QueuePosition = i + 1
		}
	}
}

func (m *Manager) queueProcessor() {
	for {
		m.queueMu.Lock()

		for len(m.queue) == 0 || m.runningCount >= m.maxConcurrent {
			m.queueCond.Wait()
		}

		jobID := m.queue[0]
		m.queue = m.queue[1:]
		m.runningCount++

		m.updateQueuePositions()

		m.queueMu.Unlock()

		m.startJobExecution(jobID)
	}
}

func (m *Manager) startJobExecution(jobID string) {
	m.mu.Lock()
	job, exists := m.jobs[jobID]
	if !exists {
		m.mu.Unlock()
		m.jobFinished()
		return
	}

	now := time.Now()
	job.Status = JobStatusRunning
	job.QueuePosition = 0
	job.StartedAt = &now
	m.mu.Unlock()

	m.notifySubscribers(jobID, &Progress{Stage: "Starting", Current: 0, Total: 0})

	go m.executeJob(context.Background(), job)
}

func (m *Manager) jobFinished() {
	m.queueMu.Lock()
	m.runningCount--
	m.queueCond.Signal()
	m.queueMu.Unlock()
}

func (m *Manager) GetJob(jobID string) (*Job, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job, exists := m.jobs[jobID]
	return job, exists
}

func (m *Manager) StartJob(jobID string) error {
	m.mu.RLock()
	job, exists := m.jobs[jobID]
	m.mu.RUnlock()

	if !exists {
		return &JobNotFoundError{JobID: jobID}
	}

	if job.Status == JobStatusQueued {
		log.Printf("Job %s queued at position %d", jobID, job.QueuePosition)
		return nil
	}

	return nil
}

func (m *Manager) executeJob(ctx context.Context, job *Job) {

	defer m.jobFinished()

	onProgress := func(stage string, current, total int) {
		m.mu.Lock()
		job.Progress = &Progress{
			Stage:   stage,
			Current: current,
			Total:   total,
		}
		progress := *job.Progress
		m.mu.Unlock()

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

	m.closeSubscribers(job.ID)
}

func (m *Manager) Subscribe(jobID string) (ProgressSubscriber, func()) {
	m.subscriberMu.Lock()
	defer m.subscriberMu.Unlock()

	ch := make(chan *Progress, 10)
	m.subscribers[jobID] = append(m.subscribers[jobID], ch)

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

func (m *Manager) notifySubscribers(jobID string, progress *Progress) {
	m.subscriberMu.RLock()
	defer m.subscriberMu.RUnlock()

	for _, sub := range m.subscribers[jobID] {
		select {
		case sub <- progress:
		default:

		}
	}
}

func (m *Manager) closeSubscribers(jobID string) {
	m.subscriberMu.Lock()
	defer m.subscriberMu.Unlock()

	for _, sub := range m.subscribers[jobID] {
		close(sub)
	}
	delete(m.subscribers, jobID)
}

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

		if (job.Status == JobStatusCompleted || job.Status == JobStatusFailed) &&
			job.EndedAt != nil && job.EndedAt.Before(cutoff) {
			delete(m.jobs, id)

			if m.visitorJobs[job.VisitorIP] == id {
				delete(m.visitorJobs, job.VisitorIP)
			}
		}
	}
}

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

func (m *Manager) GetQueueStats() (running, queued, maxConcurrent int) {
	m.queueMu.Lock()
	defer m.queueMu.Unlock()
	return m.runningCount, len(m.queue), m.maxConcurrent
}
