# Project Agent Guidelines

## Go development

Before any Go coding, review, debugging, troubleshooting, or setup task, load the `samber/cc-skills-golang@golang-how-to` skill first — it routes to whichever other Go skills the task needs.

## API & Documentation Maintenance

Whenever you add, modify, or remove any HTTP endpoint, route, request/response struct, query parameter, header, status code, or error mapping:

1. **Update Swagger annotations & model tags**:
   - Keep `@Summary`, `@Description`, `@Tags`, `@Accept`, `@Produce`, `@Param`, `@Success`, `@Failure`, and `@Router` annotations accurate in `internal/handler/*.go` (and top-level API info in `cmd/server/main.go`).
   - Keep `json`, `binding`, `enums`, and `example` struct tags accurate in `internal/model/*.go`.
   - Avoid Go generic type arguments inside Swaggo `@Success`/`@Failure` annotations (use nested composition like `model.APIResponse{data=model.YourType}` for `swag` v1 compatibility).
2. **Regenerate Swagger artifacts**:
   - Run `make swag` (or `swag init -g cmd/server/main.go`) so `docs/docs.go`, `docs/swagger.json`, and `docs/swagger.yaml` stay synchronized with the code.
3. **Update Markdown documentation (in English)**:
   - Update [`API.md`](API.md) with any changed endpoints, payload fields, query parameters, response examples, or error codes.
   - Update [`README.md`](README.md) if supported platforms, environment variables, Docker setup, or high-level features changed.
4. **Verify before committing**:
   - Run `go test -race -shuffle=on ./...` and `go vet ./...` after regenerating Swagger docs and updating handlers/models.
