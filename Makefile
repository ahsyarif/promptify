DATABASE_URL ?= postgres://promptify:promptify@localhost:5432/promptify?sslmode=disable
TEST_DATABASE_URL ?= $(DATABASE_URL)

.PHONY: run build test db up down

run:
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/server

build:
	go build -o bin/promptify ./cmd/server

test:
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -race ./...

db:
	docker compose up -d db

up:
	docker compose up --build

down:
	docker compose down
