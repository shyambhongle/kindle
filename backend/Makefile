BIN=dashboard

.PHONY: build run token import seed collect backup pi test install-launchd restart

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

backup: build
	./$(BIN) backup

# macOS: start at login and keep running (uses deploy/com.shyam.dashboard.plist)
install-launchd: build
	sh deploy/install-launchd.sh

restart:
	launchctl kickstart -k gui/$$(id -u)/com.shyam.dashboard

# Raspberry Pi (64-bit OS). For 32-bit Pi OS use GOARCH=arm GOARM=7.
pi:
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(BIN)-pi ./cmd/dashboard

test:
	go test ./...
