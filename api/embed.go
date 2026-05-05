// Package api embeds the OpenAPI specification.
package api

import _ "embed"

//go:embed openapi.yaml
var OpenAPISpec []byte
