// Package contracts contains the language-independent HTTP API contract.
package contracts

import _ "embed"

//go:embed openapi.json
var OpenAPI []byte
