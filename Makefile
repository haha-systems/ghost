.PHONY: build run test fmt vet

build:
	go build -o bin/ghost ./cmd/ghost

run:
	go run ./cmd/ghost

test:
	go test ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './.git/*')

vet:
	go vet ./...
