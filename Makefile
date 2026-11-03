.PHONY: build test cover lint vet check run doctor clean

VERSION ?= dev

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/ai-usage-bot .

vet:
	go vet ./...

test:
	go test -race -count=1 ./...

cover:
	go test -race -count=1 -covermode=atomic -coverprofile=cover.out ./...
	go tool cover -func=cover.out | tail -n 1

lint:
	golangci-lint run ./...

# Everything CI runs, locally.
check: vet lint test

run: build
	./bin/ai-usage-bot run

# Read every data source and print the message; sends nothing to Telegram.
doctor: build
	./bin/ai-usage-bot doctor

clean:
	rm -rf bin cover.out
