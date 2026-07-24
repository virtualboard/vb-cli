SHELL := /bin/bash
GO ?= go
GOFLAGS ?=
GOSEC_VERSION ?= v2.25.0
GOSEC := $(GO) run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)
BINARY := vb
DIST_DIR := dist
COVERAGE_FILE := coverage.out
COVERAGE_MIN ?= 75.0

.PHONY: build test coverage-check coverage package clean scan fmt tidy pre-commit version-bump

build:
	$(GO) build $(GOFLAGS) ./...

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

test:
	$(GO) test $(GOFLAGS) ./... -coverprofile=$(COVERAGE_FILE)
	@$(MAKE) coverage-check GO="$(GO)" COVERAGE_FILE="$(COVERAGE_FILE)" COVERAGE_MIN="$(COVERAGE_MIN)"

coverage-check:
	@test -s "$(COVERAGE_FILE)" || { echo "Coverage profile is missing: $(COVERAGE_FILE)"; exit 1; }
	@$(GO) tool cover -func=$(COVERAGE_FILE)
	@total=$$($(GO) tool cover -func=$(COVERAGE_FILE) | awk '/^total:/ {gsub(/%/, "", $$3); print $$3}'); \
	awk -v total="$$total" -v minimum="$(COVERAGE_MIN)" \
		'BEGIN { if ((total + 0) < (minimum + 0)) exit 1 }' || { \
		echo "Measured coverage $${total}% is below the $(COVERAGE_MIN)% minimum"; \
		exit 1; \
	}; \
	echo "Measured coverage: $${total}% (minimum: $(COVERAGE_MIN)%)"

coverage: test
	@$(GO) tool cover -html=$(COVERAGE_FILE) -o coverage.html
	@echo "Coverage report generated at coverage.html"

package: build
	@mkdir -p $(DIST_DIR)
	$(GO) build $(GOFLAGS) -o $(DIST_DIR)/$(BINARY) .

scan:
	$(GOSEC) -exclude-dir=.gomodcache -exclude-dir=dist ./...

pre-commit:
	@which pre-commit > /dev/null || (echo "pre-commit not installed. Run: pip install pre-commit" && exit 1)
	pre-commit run --all-files

version-bump:
	@if [ -z "$(VERSION)" ]; then \
		echo "Usage: make version-bump VERSION=v1.0.0"; \
		exit 1; \
	fi
	@./scripts/version-bump.sh $(VERSION)

clean:
	rm -f $(COVERAGE_FILE) coverage.html
	rm -rf $(DIST_DIR)
