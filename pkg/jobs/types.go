package jobs

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusFailed    Status = "failed"
	StatusSucceeded Status = "succeeded"
)

type JobDefinition struct {
	ID         string `yaml:"id" json:"id"`
	TargetHost string `yaml:"target_host" json:"target_host"`
	// Defaults ingested when zero
	TargetPort int      `yaml:"target_port" json:"target_port"`
	TargetUser string   `yaml:"target_user" json:"target_user"`
	Command    string   `yaml:"command" json:"command"`
	Arguments  []string `yaml:"arguments" json:"arguments"`
	// Controls whether the engine requests a pseudo-terminal for interactive commands
	AllowTTY    bool              `yamlL:"allow_tty" json:"allow_tty"`
	Checksum    string            `yaml:"checksum" json:"checksum"`
	Metadata    map[string]string `yaml:"metadata" json:"metadata"`
	Credentials CredentialBundle  `yaml:"credentials" json:"credentials"`
}

type CredentialBundle struct {
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
}

type Result struct {
	JobID      string            `yaml:"job_id" json:"job_id"`
	Status     Status            `yaml:"status" json:"status"`
	StartedAt  time.Time         `yaml:"started_at" json:"started_at"`
	FinishedAt time.Time         `yaml:"finished_at" json:"finished_at"`
	ExitCode   int               `yaml:"exit_code" json:"exit_code"`
	Stdout     string            `yaml:"stdout" json:"stdout"`
	Stderr     string            `yaml:"stderr" json:"stderr"`
	Error      string            `yaml:"error" json:"error"`
	Metadata   map[string]string `yaml:"metadata" json:"metadata"`
}

func (j JobDefinition) Validate() error {
	if j.ID == "" {
		return errors.New("job id cannot be empty")
	}
	if j.TargetHost == "" {
		return fmt.Errorf("job %s missing target_host", j.ID)
	}
	if j.TargetUser == "" {
		return fmt.Errorf("job %s missing target_user", j.ID)
	}
	if j.Command == "" {
		return fmt.Errorf("job %s missing command", j.ID)
	}
	if j.Checksum == "" {
		return fmt.Errorf("job %s missing checksum", j.ID)
	}
	if err := j.Credentials.Validate(); err != nil {
		return fmt.Errorf("job %s credentials invalid: %w", j.ID, err)
	}
	return nil
}

func (c CredentialBundle) Validate() error {
	if strings.TrimSpace(c.Username) == "" {
		return errors.New("username required")
	}
	if strings.TrimSpace(c.Password) == "" {
		return errors.New("password required")
	}

	return nil
}
