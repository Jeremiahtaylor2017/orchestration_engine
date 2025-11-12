package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Jeremiahtaylor2017/orchestration_engine/pkg/executor"
	"github.com/Jeremiahtaylor2017/orchestration_engine/pkg/jobs"
	"github.com/Jeremiahtaylor2017/orchestration_engine/pkg/transport"
)

// Config models engine.yaml to tweak behavior without recompiling
type Config struct {
	Transport TransportConfig `yaml:"transport"`
	Execution ExecutionConfig `yaml:"execution"`
}

// TransportConfig controls how the engine watches for work
type TransportConfig struct {
	// InboxDir is the file share
	InboxDir            string `yaml:"inbox_dir"`
	PollIntervalSeconds int    `yaml:"poll_interval_seconds"`
}

// ExecutionConfig owns everything related to remote execution policy
type ExecutionConfig struct {
	AllowedCommands []string `yaml:"allowed_commands"`
	// DialTimeoutSeconds bounds TCP handshakes so hung hosts do not stall engine
	DialTimeoutSeconds int `yaml:"dial_timeout_seconds"`
	//JobTimeoutSeconds bounds the entire remote execution (command + streaming output)
	JobTimeoutSeconds int    `yaml:"job_timeout_seconds"`
	PrivateKeyPath    string `yaml:"private_key_Path"`
	// Password is optional for legacy hosts with no key-based auth
	Password string `yaml:"password"`
	//HostKeyFingerprints pins trusted server keys (map keyed by host or host:port string)
	HostKeyFingerprints map[string]string `yaml:"host_key_fingerprints"`
}

func main() {
	// Users can pass -config=/etc/orchestrator/engine.yaml
	configPath := flag.String("config", "config/engine.yaml", "absolute or relative path to engine configuration file")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	privateKey, err := readPrivateKey(cfg.Execution.PrivateKeyPath)
	if err != nil {
		log.Fatalf("load private key: %v", err)
	}

	transport := &transport.FilesystemTransport{
		InboxDir:     cfg.Transport.InboxDir,
		PollInterval: pollInterval(cfg.Transport.PollIntervalSeconds),
	}

	exec := &executor.SSHExecutor{
		AllowedCommands: buildAllowlist(cfg.Execution.AllowedCommands),
		DialTimeout:     timeoutOrDefault(cfg.Execution.DialTimeoutSeconds, 10*time.Second),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := make(chan struct{})
	go func() {
		// Trap SIGINT/SIGTERM to exit gracefully and leave partial jobs untouched
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		close(stop)
		cancel()
	}()

	log.Printf("engine started; watching %s for *.job.json instructions", cfg.Transport.InboxDir)

	for {
		job, jobPath, err := transport.NextJob(stop)
		if err != nil {
			if stopped(stop) {
				log.Println("shutdown requested; exiting engine loop")
				return
			}
			log.Printf("polling error: %v", err)
			continue
		}

		jobCtx, jobCancel := context.WithTimeout(ctx, timeoutOrDefault(cfg.Execution.JobTimeoutSeconds, 2*time.Minute))
		result, execErr := exec.Execute(jobCtx, *job, buildCredentials(*job, cfg.Execution, privateKey))
		jobCancel()
		if execErr != nil {
			// Execute already encoded errors into the result struct; we still log
			log.Printf("job %s execution error: %v", job.ID, execErr)
		}

		if err := transport.WriteResult(jobPath, result); err != nil {
			log.Printf("job %s result write failed: %v", job.ID, err)
		} else {
			log.Printf("job %s finished with status=%s exit=%d", job.ID, result.Status, result.ExitCode)
		}
	}
}

// loadConfig reads YAML from disk and performs validation
func loadConfig(path string) (Config, error) {
	if path == "" {
		return Config{}, errors.New("config path cannot be empty")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Transport.InboxDir == "" {
		return Config{}, errors.New("transport.inbox_dir must be set")
	}

	return cfg, nil
}

// readPrivateKey laods the PEM once so we do not hit disk for every job.
func readPrivateKey(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("execution.private_key_path must be set for SSH key auth")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}

	return data, nil
}

// pollInterval converts seconds to time.Duration with a sane default.
func pollInterval(seconds int) time.Duration {
	if seconds <= 0 {
		return 5 * time.Second
	}

	return time.Duration(seconds) * time.Second
}

// timeoutOrDefault return duration when provided; fallback otherwise
func timeoutOrDefault(seconds int, fallback time.Duration) time.Duration {
	if seconds <= 0 {
		return fallback
	}

	return time.Duration(seconds) * time.Second
}

// buildAllowlist turns the slice into a constant-time lookup map
func buildAllowlist(commands []string) map[string]struct{} {
	if len(commands) == 0 {
		return nil
	}

	allow := make(map[string]struct{}, len(commands))
	for _, cmd := range commands {
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		allow[cmd] = struct{}{}
	}

	return allow
}

// stopped returns true once the stop channel has been closed
func stopped(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

// buildCredentials merges job-provided host/user info with engine-held secrets
func buildCredentials(job jobs.JobDefinition, execCfg ExecutionConfig, privateKey []byte) executor.SSHCredentials {
	address := fmt.Sprintf("%s:%d", job.TargetHost, effectivePort(job.TargetPort))
	fpKey := job.TargetHost
	if job.TargetPort != 0 {
		fpKey = fmt.Sprintf("%s:%d", job.TargetHost, job.TargetPort)
	}

	return executor.SSHCredentials{
		Address:       address,
		Username:      job.TargetUser,
		PrivateKeyPEM: privateKey,
		Password:      execCfg.Password,
		Fingerprint:   execCfg.HostKeyFingerprints[fpKey],
	}
}

// effectivePort uses 22 as default when omitted
func effectivePort(port int) int {
	if port <= 0 {
		return 22
	}

	return port
}
