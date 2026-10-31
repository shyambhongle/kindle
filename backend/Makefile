BIN=dashboard

.PHONY: build run token import seed collect pi test

build:
	go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/dashboard

run: build
	./$(BIN) serve

token: build
	./$(BIN) token

import: build
	./$(BIN) import

seed: build
	./$(BIN) seed

collect: build
	./$(BIN) collect

# Raspberry Pi (64-bit OS). For 32-bit Pi OS use GOARCH=arm GOARM=7.
pi:
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(BIN)-pi ./cmd/dashboard

test:
	go test ./...
