package transport

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jeremiahtaylor2017/orchestration_engine/pkg/jobs"
)

// Poller over shared directory
// Watches for (*.job.json) and emits results next to them.
type FilesystemTransport struct {
	// InboxDir si where new job files land
	InboxDir     string
	PollInterval time.Duration
}

// NextJob blocks until a valid job file is discovered or the poller times out.
// Returns the parsed job and the absolute path to the engine can drop the result beside it.
func (t *FilesystemTransport) NextJob(stop <-chan struct{}) (*jobs.JobDefinition, string, error) {
	if t.InboxDir == "" {
		return nil, "", errors.New("inbox directory not configured")
	}
	for {
		select {
		case <-stop:
			return nil, "", errors.New("polling stopped")
		default:
			jobPath, job, err := t.scanInbox()
			if err == nil && job != nil {
				return job, jobPath, nil
			}
			time.Sleep(t.PollInterval)
		}
	}
}

// WriteResult persists the result next to the original job file using .result.json
func (t *FilesystemTransport) WriteResult(jobPath string, result jobs.Result) error {
	resultPath := jobPath + ".result.json"
	payload, err := json.MarshalIndent(result, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(resultPath, payload, 0o640)
}

func (t *FilesystemTransport) scanInbox() (string, *jobs.JobDefinition, error) {
	var selected string
	err := filepath.WalkDir(t.InboxDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".json" {
			return nil
		}
		if filepath.Ext(strings.TrimSuffix(path, filepath.Ext(path))) != ".job" {
			return nil
		}
		selected = path
		return fs.SkipAll
	})
	if err != nil || selected == "" {
		return "", nil, err
	}

	fileData, err := os.ReadFile(selected)
	if err != nil {
		return "", nil, err
	}

	var job jobs.JobDefinition
	if err := json.Unmarshal(fileData, &job); err != nil {
		return "", nil, err
	}
	if err := job.Validate(); err != nil {
		return "", nil, err
	}

	return selected, &job, nil
}
