package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/Jeremiahtaylor2017/orchestration_engine/pkg/jobs"
)

func main() {
	jobPath := flag.String("job", "", "path to job.json")
	controllerURL := flag.String("controller", "http://localhost:8080", "controller base URL")
	flag.Parse()

	if strings.TrimSpace(*jobPath) == "" {
		log.Fatal("-job is required")
	}

	job, err := loadJob(*jobPath)
	if err != nil {
		log.Fatalf("load job: %v", err)
	}

	reader := bufio.NewReader(os.Stdin)
	job.ID = ensureJobID(job.ID)
	job.TargetUser = promptUser(reader, job.TargetUser)
	job.Checksum = ensureChecksum(job.Command)
	job.Credentials = promptCredentials(reader, job.TargetUser)

	if err := job.Validate(); err != nil {
		log.Fatalf("job invalid: %v", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	// baseURL := strings.TrimRight(*controllerURL, "/")
	baseURL, err := normalizeControllerURL(*controllerURL)
	if err != nil {
		log.Fatalf("controller URL invalid: %v", err)
	}

	if err := submitJob(client, baseURL, job); err != nil {
		log.Fatalf("submit job: %v", err)
	}
	log.Printf("job %s queued; waiting for result...", job.ID)

	if err := pollResult(client, baseURL, job.ID); err != nil {
		log.Fatalf("poll result: %v", err)
	}
}

// loadJob reads the user-supplied JSON job file
func loadJob(path string) (jobs.JobDefinition, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return jobs.JobDefinition{}, err
	}

	var job jobs.JobDefinition
	if err := json.Unmarshal(data, &job); err != nil {
		return jobs.JobDefinition{}, err
	}

	return job, nil
}

// ensureJobID fills in a random identifier when the file omits one
func ensureJobID(current string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}

	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		log.Fatalf("generate job id: %v", err)
	}

	return hex.EncodeToString(buf)
}

// promptUser forces operators to confirm the account used for the job
func promptUser(reader *bufio.Reader, current string) string {
	fmt.Printf("Target user [%s]: ", strings.TrimSpace(current))
	text, _ := reader.ReadString('\n')
	text = strings.TrimSpace(text)
	if text == "" && current != "" {
		return current
	}

	return text
}

// ensureChecksum computes the command checksum so the controller/engine share the same digest
func ensureChecksum(command string) string {
	sum := sha256.Sum256([]byte(command))
	return hex.EncodeToString(sum[:])
}

// promptCredentials collects username/password without echoing secrets to the terminal
func promptCredentials(reader *bufio.Reader, user string) jobs.CredentialBundle {
	username := strings.TrimSpace(user)
	for username == "" {
		fmt.Print("Admin username: ")
		text, _ := reader.ReadString('\n')
		username = strings.TrimSpace(text)
	}

	password := readSecret("Admin password")
	return jobs.CredentialBundle{
		Username: username,
		Password: string(password),
	}
}

// readSecret hides user input
func readSecret(prompt string) []byte {
	fmt.Printf("%s: ", prompt)
	secret, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		log.Fatalf("read secret: %v", err)
	}

	return bytes.TrimSpace(secret)
}

// normalizeControllerURL ensures the controller flag can be provided as host:port
// or a full URL with scheme. It defaults to http when the scheme is omitted and
// trims any trailing slash so subsequent path joins are predictable
func normalizeControllerURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("empty controller URL")
	}

	if !strings.Contains(trimmed, "://") {
		trimmed = "http://" + trimmed
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}

	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("controller URL must include host (got %q)", raw)
	}

	return strings.TrimRight(u.String(), "/"), nil
}

// submitJob pushes the job to the controller over HTTPS
func submitJob(client *http.Client, baseURL string, job jobs.JobDefinition) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}

	resp, err := client.Post(baseURL+"/v1/jobs", "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("controller rejected job (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return nil
}

// pollResult keeps hitting /v1/jobs/{id} until the controller returns a finalized result
func pollResult(client *http.Client, baseURL, jobID string) error {
	for {
		resp, err := client.Get(fmt.Sprintf("%s/v1/jobs/%s", baseURL, jobID))
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusAccepted {
			var status struct {
				Status jobs.Status `json:"status"`
			}
			if err := json.Unmarshal(body, &status); err == nil {
				log.Printf("job %s status=%s", jobID, status.Status)
			}
			time.Sleep(2 * time.Second)
			continue
		}

		if resp.StatusCode == http.StatusOK {
			var result jobs.Result
			if err := json.Unmarshal(body, &result); err != nil {
				return err
			}
			printResult(result)
			return nil
		}

		return fmt.Errorf("controller returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

// printResult mirrors what users expect to see locally
func printResult(result jobs.Result) {
	log.Printf("job %s finished status=%s exit=%d", result.JobID, result.Status, result.ExitCode)
	if strings.TrimSpace(result.Stdout) != "" {
		fmt.Printf("----- stdout -----\n%s\n", result.Stdout)
	}
	if strings.TrimSpace(result.Stderr) != "" {
		fmt.Printf("----- stderr -----\n%s\n", result.Stderr)
	}
	if strings.TrimSpace(result.Error) != "" {
		fmt.Printf("error: %s\n", result.Error)
	}
}
