.PHONY: run check generate db-up db-stop migrate-up migrate-down migrate-status migrate-create

# Generator and migration CLIs are pinned here and are not runtime dependencies.
OAPI_CODEGEN := go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.2
SQLC := go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
GOOSE_TAGS := no_mysql no_sqlite3 no_mssql no_azuresql no_clickhouse no_libsql no_vertica no_ydb
GOOSE := go run -tags '$(GOOSE_TAGS)' github.com/pressly/goose/v3/cmd/goose@v3.29.0
# goose reads the connection from the environment so it never appears in argv.
GOOSE_ENV = GOOSE_DRIVER=postgres GOOSE_DBSTRING="$$DATABASE_URL" GOOSE_MIGRATION_DIR=migrations

run:
	go run ./cmd/api

check:
	test -z "$$(gofmt -l .)"
	go test ./...
	go vet ./...

generate:
	$(OAPI_CODEGEN) -config api/oapi-codegen.yaml api/openapi.yaml
	$(SQLC) generate

db-up:
	docker compose up -d db

db-stop:
	docker compose stop db

migrate-up:
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL is not set" && exit 1)
	$(GOOSE_ENV) $(GOOSE) up

migrate-down:
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL is not set" && exit 1)
	$(GOOSE_ENV) $(GOOSE) down

migrate-status:
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL is not set" && exit 1)
	$(GOOSE_ENV) $(GOOSE) status

# Usage: make migrate-create NAME=create_homes
migrate-create:
	@test -n "$(NAME)" || (echo "NAME is required" && exit 1)
	$(GOOSE) -dir migrations -s create $(NAME) sql
