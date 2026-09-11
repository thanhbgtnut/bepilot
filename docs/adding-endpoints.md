# Adding an endpoint (and keeping Swagger in sync)

The spec is generated from annotations. You never edit YAML — you write a
comment block above the handler and run `make swag`.

## The 4 steps

1. **Write the handler**, returning a typed DTO (not `map[string]any`).
2. **Register the route** in `internal/server/server.go`.
3. **Annotate the handler** with a `// @…` block.
4. **`make swag && make test`**, then commit the regenerated `docs/` files with your change.

---

## Worked example: `GET /v1/skills/{slug}`

### 1. Handler (typed response)

Add a DTO if the response shape is new — put it in `internal/api/dto/`:

```go
// internal/api/dto/misc.go
type LoadedSkill struct {
	Slug         string   `json:"slug"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Body         string   `json:"body"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
	Resources    []string `json:"resources,omitempty"`
}
```

The handler, in `internal/api/skills.go`:

```go
// GetSkill handles GET /v1/skills/{slug}.
//
// @Summary   Get one skill's full document
// @Tags      Skills
// @Produce   json
// @Param     slug  path      string  true  "Skill slug"
// @Success   200   {object}  dto.LoadedSkill
// @Failure   401   {object}  dto.ErrorResponse
// @Failure   404   {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/skills/{slug} [get]
func (h *Handlers) GetSkill(ctx context.Context, c *app.RequestContext) {
	loaded, err := h.Skills.Load(ctx, c.Param("slug"))
	if err == store.ErrNotFound {
		h.notFound(c, "skill not found")
		return
	}
	if err != nil {
		h.serverError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.LoadedSkill{ /* map from loaded */ })
}
```

### 2. Route

`internal/server/server.go`, inside the `v1` group:

```go
v1.GET("/skills/:slug", h.GetSkill)
```

Hertz uses `:slug`; the annotation uses `{slug}`. The guard test converts
between them.

### 3. Regenerate + verify

```bash
make swag     # rewrites docs/swagger.{yaml,json} and docs/docs.go
make test     # TestRoutesMatchOpenAPISpec must pass
```

If you forgot the annotation, the guard prints:

```
route "GET /v1/skills/{slug}" is registered but missing from the generated spec
(annotate the handler + run make swag)
```

If the `@Router` path doesn't match the route (typo, `:id` vs `:slug`):

```
path "GET /v1/skills/{slug}" is in the generated spec but not registered as a route
```

### 4. View

```bash
make run
open http://localhost:8080/docs
```

`docs/` is embedded at build time, so `make run` always serves the current spec.

---

## Annotation cheatsheet

Put these `// @key value` lines in the doc comment directly above the handler
func. Order doesn't matter; `@Router` is required.

| Line | Meaning |
|---|---|
| `@Summary <text>` | one-line title in the UI |
| `@Description <text>` | longer prose (one line; repeat for more) |
| `@Tags <name>` | group in the UI (`Messages`, `Sessions`, `Skills`, `Health`) |
| `@Accept json` | request content type |
| `@Produce json` | response content type (add `@Produce text/event-stream` for SSE) |
| `@Param name in type required "desc"` | `in` = `path` \| `query` \| `body` \| `header`; add `default(30)` / `format(uuid)` as extra tokens |
| `@Param request body dto.Foo true "desc"` | request body schema |
| `@Success 200 {object} dto.Foo` | success response schema (`{array}` for slices) |
| `@Failure 404 {object} dto.ErrorResponse` | error response (reuse `dto.ErrorResponse`) |
| `@Security ApiKeyAuth` | require the `x-api-key` header (omit for public routes) |
| `@Router /v1/foo/{id} [get]` | **required** — path + method |

### Schema rules

- Reference a **named struct** from `internal/api/dto` in `@Param`/`@Success`/
  `@Failure`. `swag` reads its `json` tags to build the schema.
- New response shape → add a struct to `internal/api/dto/` (don't return
  `map[string]any`; `swag` can't type that).
- `omitempty` field → not marked `required`.
- Pointer field that serializes to `null` → add `` `swaggertype:"..."` `` or an
  `example:"..."` tag as needed.
- "string or array" field → tag the struct field `` `swaggertype:"object"` ``
  and note the string form in the field comment (see `MessagesRequest.System`).

## Changing an existing endpoint

- **New body field** → add it to the DTO struct, `make swag`.
- **New query param** → add a `@Param ... query ...` line, `make swag`.
- **New status code** → add a `@Success`/`@Failure` line, `make swag`.
- **Rename path** → change it in `server.go` and the `@Router` line; the guard
  test catches a mismatch.
- **Remove endpoint** → delete the route, the handler (with its annotations),
  and any now-unused DTO; `make swag`.

## General API info

Title, version, base path, and the `x-api-key` security scheme are the `// @…`
block above `main()` in `cmd/server/main.go`. Edit there for global changes.

## Stricter linting (optional)

```bash
npx @redocly/cli lint docs/swagger.yaml
npx @stoplight/spectral-cli lint docs/swagger.yaml
```

## Generate a client from the spec

```bash
openapi-generator-cli generate -i docs/swagger.yaml -g <lang> -o ./client
```
