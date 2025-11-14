# Orchestration Engine

## Build instructions
- go build -o bin/controller ./cmd/controller
- go build -o bin/engine ./cmd/engine
- go build -o bin/orchcli ./cmd/orchcli

## Run Engine
./bin/engine -config path/to/engine.yaml

## Run Controller
./bin/controller -listen URL:PORT

## Use CLI
./bin/orchcli -job path/to/job.json -controller URL
