set shell := ["bash", "-cu"]
set positional-arguments := true
set dotenv-load := true

binary  := "sharetext-server"
image   := "sharetext:local"
image_dev   := "fabiop85/sharetext:dev"
port    := "8080"
# Override at invocation: `just version=v1.2.3 build`
version := env_var_or_default("VERSION", "")

# Compose -ldflags conditionally so a missing VERSION keeps the in-code default.
ldflags := if version == "" { "-s -w" } else { "-s -w -X sharetext/internal/version.Version=" + version }

default:
    @just --list

docker-login:
    echo $DOCKER_PASSWORD | docker login --username $DOCKER_LOGIN --password-stdin

push-docker-dev:
    @just docker-login
    docker buildx rm sharetext-builder || true
    docker buildx create --use --name sharetext-builder || true
    docker buildx build --platform linux/amd64,linux/arm64 -t {{image_dev}} --push .
    docker buildx rm sharetext-builder || true

# Run locally with go run
run:
    go run ./cmd/server

# Build native binary (optionally bake VERSION via env)
build:
    CGO_ENABLED=0 go build -ldflags='{{ldflags}}' -o {{binary}} ./cmd/server

# Run Go tests
test:
    go test ./... -count=1

# Race detector
test-race:
    go test -race ./... -count=1

# Run JS tests (block parser + countdown + download helpers + editor lock)
test-js:
    node --test cmd/server/static/blocks.test.mjs cmd/server/static/countdown.test.mjs cmd/server/static/download.test.mjs cmd/server/static/files.test.mjs cmd/server/static/sync.test.mjs cmd/server/static/editor.test.mjs cmd/server/static/lock.test.mjs

# Run all tests
test-all: test test-js

# go vet
vet:
    go vet ./...

# Format code
fmt:
    gofmt -w .

# Remove build artifacts
clean:
    rm -f {{binary}} *.db *.db-wal *.db-shm

# Build Docker image
docker-build:
    docker build -t {{image}} .

# Compose up (build + run, detached)
up:
    docker compose up --build -d

# Compose down
down:
    docker compose down

# Tail compose logs
logs:
    docker compose logs -f

# Smoke test against running compose stack
smoke:
    @set -e; \
    echo "healthz:"; curl -fsS http://localhost:{{port}}/healthz; echo; \
    echo "create:"; SLUG=$(curl -fsS -X POST http://localhost:{{port}}/api/sessions | tee /dev/stderr | python3 -c 'import sys,json;print(json.load(sys.stdin)["slug"])'); echo; \
    echo "put:"; curl -fsS -X PUT -H 'content-type: application/json' -d '{"content":"smoke"}' http://localhost:{{port}}/api/sessions/$SLUG; echo; \
    echo "get:"; curl -fsS http://localhost:{{port}}/api/sessions/$SLUG; echo
