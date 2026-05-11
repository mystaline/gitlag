.PHONY: run build install tidy help

APP=gitlag
BINDIR?=$(shell go env GOPATH)/bin

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

run: ## Run from source
	go run ./cmd/$(APP)

build: ## Build binary to bin/
	go build -o bin/$(APP) ./cmd/$(APP)

install: ## Install to $GOPATH/bin (~/go/bin)
	@mkdir -p $(BINDIR)
	go build -o $(BINDIR)/$(APP) ./cmd/$(APP)
	@echo "Installed → $(BINDIR)/$(APP)"

tidy: ## Tidy go modules
	go mod tidy
