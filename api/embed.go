package api

import "embed"

// Governing: SPEC-0017 REQ-15 "OpenAPI Specification File" — embedded OpenAPI 3.1 spec served at /api/openapi.yaml
//
//go:embed openapi.yaml
var OpenAPISpec []byte

// Governing: SPEC-0017 REQ-16 "Swagger UI" — embedded Swagger UI assets served at /api/docs/
//
//go:embed swagger-ui/*
var SwaggerUIFS embed.FS
