package controller

import (
	"fmt"
	"sync"

	"github.com/Jeremiahtaylor2017/orchestration_engine/pkg/jobs"
)

// Store keeps pending jobs and completed results in-memory
// Controller call this HTTP handler to enqueue work, engines poll it for next job, users poll status/results
type Store struct {
	mu      sync.Mutex
	queue   []string              // FIFO of job IDs waiting pickup
	records map[string]*jobRecord //full job definitions + status/results
}

type jobRecord struct {
	job    jobs.JobDefinition
	status jobs.Status
	result *jobs.Result
}

// NewStore returns a ready-to-use in-memory queue
func NewStore() *Store {
	return &Store{
		queue:   make([]string, 0, 32),
		records: make(map[string]*jobRecord),
	}
}

// Enqueue validates and queues a job for execution
func (s *Store) Enqueue(job jobs.JobDefinition) error {
	if err := job.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.records[job.ID]; exists {
		return fmt.Errorf("job %s already exists", job.ID)
	}

	s.records[job.ID] = &jobRecord{
		job:    job,
		status: jobs.StatusPending,
	}
	s.queue = append(s.queue, job.ID)

	return nil
}

// Next pops the next pending job for engine
// Return (nil, false) when nothing is queued
func (s *Store) Next() (*jobs.JobDefinition, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.queue) == 0 {
		return nil, false
	}

	jobID := s.queue[0]
	s.queue = s.queue[1:]

	rec := s.records[jobID]
	rec.status = jobs.StatusRunning

	jobCopy := rec.job //return by value so callers cannot mutate store internals

	return &jobCopy, true
}

// Complete records the final result returned by an engine
func (s *Store) Complete(result jobs.Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.records[result.JobID]
	if !ok {
		return fmt.Errorf("job %s not found", result.JobID)
	}

	rec.status = result.Status
	// Store copy to not be able to mutate original pointer
	resCopy := result
	rec.result = &resCopy

	return nil
}

// Lookup exposes status/result for a given job ID
func (s *Store) Lookup(jobID string) (jobs.Status, *jobs.Result, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.records[jobID]
	if !ok {
		return "", nil, false
	}
	if rec.result == nil {
		return rec.status, nil, true
	}

	resultCopy := *rec.result

	return rec.status, &resultCopy, true
}
