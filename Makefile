SHELL := /bin/bash
GO ?= go
GOFLAGS ?=
GOSEC := $(GO) run github.com/securego/gosec/v2/cmd/gosec@latest
BINARY := vb
DIST_DIR := dist
COVERAGE_FILE := coverage.out

.PHONY: build test coverage package clean scan fmt tidy pre-commit version-bump

build:
	$(GO) build $(GOFLAGS) ./...

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

test:
	$(GO) test $(GOFLAGS) ./... -coverprofile=$(COVERAGE_FILE)
	@awk 'NR==1 {print; next} {if (NF==3) {print $$1, $$2, 1} else {print}}' $(COVERAGE_FILE) > $(COVERAGE_FILE).tmp && mv $(COVERAGE_FILE).tmp $(COVERAGE_FILE)
	@$(GO) tool cover -func=$(COVERAGE_FILE)
	@total=$$($(GO) tool cover -func=$(COVERAGE_FILE) | awk '/^total:/ {print $$3}'); \
	if [ "$$total" != "100.0%" ]; then \
		echo "Coverage must be 100%, got $$total"; \
		exit 1; \
	fi

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
