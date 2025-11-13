package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Jeremiahtaylor2017/orchestration_engine/pkg/jobs"
)

// HTTPTransport polls the controller for pending jobs and reports results back.
type HTTPTransport struct {
	BaseURL      string
	Client       *http.Client
	PollInterval time.Duration
}

// NextJob continuously polls /v1/queue/next until a job arrives or the caller cancels via stop.
// The returned receipt string is the job ID so the engine can reference it when posting results.
func (t *HTTPTransport) NextJob(stop <-chan struct{}) (*jobs.JobDefinition, string, error) {
	if strings.TrimSpace(t.BaseURL) == "" {
		return nil, "", errors.New("controller base URL not configured")
	}

	client := t.httpClient()
	for {
		select {
		case <-stop:
			return nil, "", errors.New("polling stopped")
		default:
			req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/queue/next", t.BaseURL), nil)
			if err != nil {
				return nil, "", err
			}

			resp, err := client.Do(req)
			if err != nil {
				time.Sleep(t.sleepInterval())
				continue
			}

			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				return nil, "", readErr
			}

			switch resp.StatusCode {
			case http.StatusNoContent:
				time.Sleep(t.sleepInterval())
				continue
			case http.StatusOK:
				var job jobs.JobDefinition
				if err := json.Unmarshal(body, &job); err != nil {
					return nil, "", err
				}
				if err := job.Validate(); err != nil {
					return nil, "", err
				}

				return &job, job.ID, nil
			default:
				return nil, "", fmt.Errorf("controller returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			}
		}
	}
}

// WriteResult sends the execution result back to /v1/jobs/{id}/results
func (t *HTTPTransport) WriteResult(jobID string, result jobs.Result) error {
	if strings.TrimSpace(jobID) == "" {
		return errors.New("job id required for result submission")
	}

	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("%s/v1/jobs/%s/results", t.BaseURL, jobID),
		bytes.NewReader(payload),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient().Do(req)
	if err != nil {
		return err
	}

	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("controller rejected result (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return nil
}

func (t *HTTPTransport) sleepInterval() time.Duration {
	if t.PollInterval <= 0 {
		return 2 * time.Second
	}

	return t.PollInterval
}

func (t *HTTPTransport) httpClient() *http.Client {
	if t.Client != nil {
		return t.Client
	}

	return &http.Client{Timeout: 30 * time.Second}
}
