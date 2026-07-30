.PHONY: dev test test-web test-api build docker-up docker-down

dev:
	npm run dev

test: test-api build test-web

test-web:
	npm test

test-api:
	cd backend && go test ./...

build:
	npm run build

docker-up:
	docker compose up --build

docker-down:
	docker compose down
