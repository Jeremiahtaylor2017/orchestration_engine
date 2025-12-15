package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Jeremiahtaylor2017/orchestration_engine/pkg/jobs"
)

type SSHCredentials struct {
	// host:port (default 22 when omitted)
	Address     string
	Username    string
	Password    string
	Fingerprint string
}

type SSHExecutor struct {
	AllowedCommands map[string]struct{}
	DialTimeout     time.Duration
}

// Execute runs the job remotely and return stdout/sterr/exit code
func (e *SSHExecutor) Execute(ctx context.Context, job jobs.JobDefinition, creds SSHCredentials) (jobs.Result, error) {
	started := time.Now().UTC()
	if err := e.validateJob(job); err != nil {
		// return jobs.Result{}, err
		return e.buildResult(job, started, "", "", err), err
	}

	client, err := e.newClient(ctx, creds)
	if err != nil {
		// return jobs.Result{}, err
		return e.buildResult(job, started, "", "", err), err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		// return jobs.Result{}, fmt.Errorf("start session: %w", err)
		err = fmt.Errorf("start session: %w", err)
		return e.buildResult(job, started, "", "", err), err
	}
	defer session.Close()

	// Connect stdout/stderr to capture buffers for auditing
	var stdoutBuf, stderrBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf

	// Allocate PTY only when explicitly allowed
	if job.AllowTTY {
		if err := session.RequestPty("xterm", 80, 24, ssh.TerminalModes{}); err != nil {
			return jobs.Result{}, fmt.Errorf("request pty: %w", err)
		}
	}

	// start := time.Now().UTC()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- session.Run(job.Command)
	}()

	select {
	case <-runCtx.Done():
		// return jobs.Result{}, runCtx.Err()
		err := runCtx.Err()
		return e.buildResult(job, started, stdoutBuf.String(), stderrBuf.String(), err), err
	case err := <-done:
		// return e.buildResult(job, start, stdoutBuf.String(), stderrBuf.String(), err), nil
		return e.buildResult(job, started, stdoutBuf.String(), stderrBuf.String(), err), err
	}
}

func (e *SSHExecutor) validateJob(job jobs.JobDefinition) error {
	if err := job.Validate(); err != nil {
		return err
	}

	if len(e.AllowedCommands) > 0 {
		if _, ok := e.AllowedCommands[job.Command]; !ok {
			return fmt.Errorf("command %s not allowed", job.Command)
		}
	}

	// Recompute checksum locally fo integrity
	sum := sha256.Sum256([]byte(job.Command))
	if hex.EncodeToString(sum[:]) != job.Checksum {
		return errors.New("checksum mismatch")
	}

	return nil
}

func (e *SSHExecutor) newClient(ctx context.Context, creds SSHCredentials) (*ssh.Client, error) {
	if creds.Address == "" || creds.Username == "" {
		return nil, errors.New("missing SSH address or username")
	}
	if creds.Password == "" {
		return nil, errors.New("missing password")
	}

	config := &ssh.ClientConfig{
		User:            creds.Username,
		HostKeyCallback: e.makeHostKeyCallback(creds.Fingerprint),
		Timeout:         e.DialTimeout,
	}

	config.Auth = []ssh.AuthMethod{ssh.Password(creds.Password)}

	dialer := &net.Dialer{Timeout: e.DialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", creds.Address)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", creds.Address, err)
	}

	c, chans, reqs, err := ssh.NewClientConn(conn, creds.Address, config)
	if err != nil {
		return nil, fmt.Errorf("handshake: %w", err)
	}

	return ssh.NewClient(c, chans, reqs), nil
}

func (e *SSHExecutor) makeHostKeyCallback(expected string) ssh.HostKeyCallback {
	if expected == "" {
		return ssh.InsecureIgnoreHostKey()
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		fingerprint := ssh.FingerprintSHA256(key)
		if fingerprint != expected {
			return fmt.Errorf("host key mismatch for %s: got %s want %s", hostname, fingerprint, expected)
		}
		return nil
	}
}

func (e *SSHExecutor) buildResult(job jobs.JobDefinition, started time.Time, stdout, stderr string, runErr error) jobs.Result {
	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*ssh.ExitError); ok {
			exitCode = exitErr.ExitStatus()
		} else {
			exitCode = -1
		}
	}

	return jobs.Result{
		JobID:      job.ID,
		Status:     e.statusFromError(runErr),
		StartedAt:  started,
		FinishedAt: time.Now().UTC(),
		ExitCode:   exitCode,
		Stdout:     stdout,
		Stderr:     stderr,
		Error:      errorString(runErr),
		Metadata:   job.Metadata,
	}
}

func (e *SSHExecutor) statusFromError(err error) jobs.Status {
	if err != nil {
		return jobs.StatusFailed
	}

	return jobs.StatusSucceeded
}

func errorString(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}
