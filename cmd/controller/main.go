package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/Jeremiahtaylor2017/orchestration_engine/pkg/controller"
	"github.com/Jeremiahtaylor2017/orchestration_engine/pkg/jobs"
)

func main() {
	listen := flag.String("listen", ":8080", "controller address")
	flag.Parse()

	store := controller.NewStore()
	mux := http.NewServeMux()

	// POST /v1/jobs -> user uploads a job definition
	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		handleSubmit(w, r, store)
	})

	// GET /v1/jobs/{id} -> user polls status/result
	// POST /v1/jobs/{id}/results -> engine posts execution result
	mux.HandleFunc("/v1/jobs/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/results") {
			handleResult(w, r, store)
			return
		}

		if r.Method == http.MethodGet {
			handleStatus(w, r, store)
			return
		}

		http.NotFound(w, r)
	})

	// GET /v1/queue/next -> engine long-polls for the next jobs
	mux.HandleFunc("/v1/queue/next", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}

		handleNext(w, store)
	})

	log.Printf("controller listening on %s", *listen)
	log.Fatal(http.ListenAndServe(*listen, mux))
}

// handleSubmit ingests a job, validates it, and queues it for the engine
func handleSubmit(w http.ResponseWriter, r *http.Request, store *controller.Store) {
	var job jobs.JobDefinition
	if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
		http.Error(w, fmt.Sprintf("invalid job payload: %v", err), http.StatusBadRequest)
		return
	}

	if err := store.Enqueue(job); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// handleResult records the result emitted by an engine
func handleResult(w http.ResponseWriter, r *http.Request, store *controller.Store) {
	jobID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/jobs/"), "/results")
	if jobID == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}

	var result jobs.Result
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		http.Error(w, fmt.Sprintf("invalid result payload: %v", err), http.StatusBadRequest)
		return
	}
	if result.JobID == "" {
		result.JobID = jobID
	}
	if err := store.Complete(result); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// handleStatus lets users poll job progress and retrieve results
func handleStatus(w http.ResponseWriter, r *http.Request, store *controller.Store) {
	jobID := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	if jobID == "" {
		http.NotFound(w, r)
		return
	}

	status, result, ok := store.Lookup(jobID)
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if result == nil {
		// Job is accepted/running; 202 keeps clients polling
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(struct {
			Status jobs.Status `json:"status"`
		}{Status: status})
		return
	}

	json.NewEncoder(w).Encode(result)
}

// handleNext return the next pending job or 204 No Content when idle
func handleNext(w http.ResponseWriter, store *controller.Store) {
	job, ok := store.Next()
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(job)
}
