package docs

import _ "embed"

// SwaggerYAML is the generated OpenAPI/Swagger document in YAML form, served at
// GET /openapi.yaml for API clients and codegen. Regenerate with `make swag`.
//
//go:embed swagger.yaml
var SwaggerYAML []byte
