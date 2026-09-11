# API documentation

The OpenAPI spec is **generated from code annotations** with
[`swaggo/swag`](https://github.com/swaggo/swag). You do not hand-write YAML —
you annotate handlers and run `make swag`.

| File | What it is | Edit it? |
|---|---|---|
| `swagger.yaml`, `swagger.json`, `docs.go` | **Generated** by `make swag`. Checked in so the binary builds without the tool. | ❌ never |
| `embed.go` | Embeds `swagger.yaml` as `docs.SwaggerYAML`, served at `GET /openapi.yaml`. | rarely |
| `README.md` | This file. | ✅ |
| `adding-endpoints.md` | Step-by-step guide to annotating a new endpoint. | ✅ |

The annotations live next to the handlers:

- **General info** (title, version, security scheme): the `// @…` block above
  `main()` in `cmd/server/main.go`.
- **Per endpoint**: the `// @Summary … // @Router …` block above each handler in
  `internal/api/*.go`.
- **Schemas**: generated from the Go structs referenced in `@Param` / `@Success`
  / `@Failure` — these are the DTOs in `internal/api/dto/`.

## How it is served

`internal/server/server.go` mounts (no auth):

| Route | Response |
|---|---|
| `GET /swagger/index.html` | Swagger UI (`hertz-contrib/swagger` + `swaggo/files`) |
| `GET /swagger/doc.json` | the generated spec, served by the UI handler |
| `GET /openapi.yaml` | the generated `swagger.yaml` (`docs.SwaggerYAML`) for clients / codegen |
| `GET /docs` | 302 redirect to `/swagger/index.html` |

Start the server and open <http://localhost:8080/docs>. Click **Authorize** and
paste an API key (from `make seed`) to try endpoints from the browser.

Everything is embedded at build time — `go build ./cmd/server` produces a binary
that serves the docs on its own. The Swagger UI JS/CSS assets are vendored by
`swaggo/files` (also embedded), so `/swagger` works fully offline.

## Regenerating

```bash
make swag     # runs: swag init -g cmd/server/main.go -o docs --parseInternal
make test     # TestRoutesMatchOpenAPISpec fails if a route has no annotation
```

`make swag` uses `go run github.com/swaggo/swag/cmd/swag@v1.16.6` — nothing to
install. Commit the regenerated `docs/` files together with the handler change.

## Guard test

`internal/server/routes_test.go` → `TestRoutesMatchOpenAPISpec` registers every
Hertz route and compares it against the `paths` in the generated `swagger.yaml`.
It fails if a route has no matching path (you forgot to annotate / regenerate) or
a documented path has no route. It runs in `make test`.

The docs-plumbing routes (`/swagger/*any`, `/docs`, `/openapi.yaml`) are
deliberately exempt (`docInfraPaths` in the test).

## Notes / trade-offs

- `swag` v1 emits **Swagger 2.0** (not OpenAPI 3.x). Swagger UI renders it fine;
  most Go tooling and client generators accept it. If you need 3.1, `swag` v2 is
  still pre-release.
- "string or array" bodies (`messages[].content`, `system`) are tagged
  `swaggertype:"object"` on the DTO so `swag` can render them; a plain string is
  also accepted at runtime (see the field comment).
- Keep DTO structs and their `json` tags accurate — they *are* the schema.
