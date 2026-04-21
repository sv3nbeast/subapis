.PHONY: build generate test test-unit test-integration test-e2e build-antigravityworker build-antigravityworker-boringcrypto run-local-antigravity-fidelity smoke-antigravity-local

VERSION ?= $(shell tr -d '\r\n' < ./cmd/server/VERSION)
LDFLAGS ?= -s -w -X main.Version=$(VERSION)

build:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -trimpath -o bin/server ./cmd/server

build-antigravityworker:
	./scripts/build-antigravity-worker.sh

build-antigravityworker-boringcrypto:
	./scripts/build-antigravity-worker.sh --boringcrypto

run-local-antigravity-fidelity:
	./scripts/run-local-antigravity-fidelity.sh

smoke-antigravity-local:
	./scripts/smoke-antigravity-local.sh

generate:
	go generate ./ent
	go generate ./cmd/server

test:
	go test ./...
	golangci-lint run ./...

test-unit:
	go test -tags=unit ./...

test-integration:
	go test -tags=integration ./...

test-e2e:
	./scripts/e2e-test.sh

test-e2e-local:
	go test -tags=e2e -v -timeout=300s ./internal/integration/...
