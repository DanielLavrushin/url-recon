package jobs

import "fmt"

type ActiveJobError struct {
	JobID string
}

func (e *ActiveJobError) Error() string {
	return fmt.Sprintf("visitor already has an active job: %s", e.JobID)
}

type JobNotFoundError struct {
	JobID string
}

func (e *JobNotFoundError) Error() string {
	return fmt.Sprintf("job not found: %s", e.JobID)
}
