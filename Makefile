.PHONY: run build install tidy test help release

APP=gitlag
BINDIR?=$(shell go env GOPATH)/bin

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

run: ## Run from source
	go run ./cmd/$(APP)

build: ## Build binary to bin/
	go build -o bin/$(APP) ./cmd/$(APP)

install: ## Install to $GOPATH/bin (~/go/bin)
	go build -o $(BINDIR)/$(APP) ./cmd/$(APP)

test: ## Run all tests
	go test ./... -count=1

tidy: ## Tidy go modules
	go mod tidy

MAJOR ?=
MINOR ?=
PATCH ?=

release: ## Tag & push release. Usage: make release [MAJOR=n] [MINOR=n] [PATCH=n]
	@latest=$$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0"); \
	ver=$${latest#v}; \
	cur_major=$$(echo "$$ver" | cut -d. -f1); \
	cur_minor=$$(echo "$$ver" | cut -d. -f2); \
	cur_patch=$$(echo "$$ver" | cut -d. -f3); \
	major=$${MAJOR:-$$cur_major}; \
	minor=$${MINOR:-$$cur_minor}; \
	patch=$${PATCH:-$$((cur_patch + 1))}; \
	tag="v$$major.$$minor.$$patch"; \
	git fetch --tags --quiet; \
	git tag "$$tag" && git push origin "$$tag" && echo "pushed $$tag"
