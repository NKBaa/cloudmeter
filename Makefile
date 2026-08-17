.PHONY: up down logs config web-test go-test
up:
	docker compose --env-file .env -f deploy/compose.yaml up -d --build
down:
	docker compose --env-file .env -f deploy/compose.yaml down
logs:
	docker compose --env-file .env -f deploy/compose.yaml logs -f
config:
	docker compose --env-file .env -f deploy/compose.yaml config --quiet
web-test:
	cd apps/web && npm run typecheck
go-test:
	go test ./...

