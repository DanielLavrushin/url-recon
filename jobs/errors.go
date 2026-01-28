package jobs

import "fmt"

// ActiveJobError is returned when visitor already has an active job
type ActiveJobError struct {
	JobID string
}

func (e *ActiveJobError) Error() string {
	return fmt.Sprintf("visitor already has an active job: %s", e.JobID)
}

// JobNotFoundError is returned when a job ID doesn't exist
type JobNotFoundError struct {
	JobID string
}

func (e *JobNotFoundError) Error() string {
	return fmt.Sprintf("job not found: %s", e.JobID)
}
