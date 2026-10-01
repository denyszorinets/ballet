// Package api embeds Core's OpenAPI specification (openapi.yaml), the
// contract of the REST API.
package api

import _ "embed"

// OpenAPI is the OpenAPI 3 document of Core's REST API.
//
//go:embed openapi.yaml
var OpenAPI []byte
