ROOT := $(CURDIR)
GO := $(ROOT)/.tools/go/bin/go
UV := $(ROOT)/.tools/uv/bin/uv
export GOTOOLCHAIN := local
export GOPATH := $(ROOT)/.cache/go
export GOCACHE := $(ROOT)/.cache/go-build
export UV_CACHE_DIR := $(ROOT)/.cache/uv
export UV_PYTHON_INSTALL_DIR := $(ROOT)/.tools/python
export UV_PYTHON_BIN_DIR := $(ROOT)/.tools/python-bin
export LANGGRAPH_STRICT_MSGPACK := true
export PLAYWRIGHT_BROWSERS_PATH := $(ROOT)/.tools/playwright

.PHONY: bootstrap sync check content e2e-tools check-browser tasks task build build-e2e run test test-go test-worker e2e run-generation-lab quality-live
bootstrap:
	python3 scripts/bootstrap.py

sync:
	$(GO) mod download
	$(UV) sync --project worker --locked --python 3.13.15

content:
	$(UV) run --directory worker --locked python ../scripts/fetch_content.py

check:
	$(GO) mod verify
	$(GO) vet ./...
	$(GO) run -mod=readonly ./cmd/dependencycheck
	$(UV) run --directory worker --locked python dependency_check.py
	$(UV) run --directory worker --locked python ../scripts/check_contracts.py
	python3 scripts/tasks.py check
	python3 scripts/freeze_design.py --check

e2e-tools:
	$(UV) sync --project e2e --locked --python 3.13.15
	$(UV) run --directory e2e --locked python -m playwright install chromium

check-browser:
	$(UV) run --directory e2e --locked python check_browser.py

tasks:
	python3 scripts/tasks.py list

task:
	python3 scripts/tasks.py show $(ID)

build:
	$(GO) build -o bin/avocab ./cmd/avocab

build-e2e:
	$(GO) build -tags e2e -o bin/avocab-e2e ./cmd/avocab

run: build
	./bin/avocab serve --data-dir data/local --listen 127.0.0.1:8080 --policy config/policy-v2.json

test: test-go test-worker

test-go:
	$(GO) test ./...

test-worker:
	$(UV) run --directory worker --locked python -m unittest

e2e: build build-e2e
	$(UV) run --directory e2e --locked python runner.py $(MODULE) $(E2E_ARGS)

run-generation-lab: build build-e2e
	$(UV) run --directory worker --locked python -m avocab_worker lab --data-dir $(if $(DATA_DIR),$(DATA_DIR),artifacts/manual/M07)

quality-live:
	$(UV) run --directory worker --locked python -m avocab_worker.quality_benchmark --allow-paid --profile $(if $(PROFILE),$(PROFILE),config/production-profile.json)
