package api

import "embed"

//go:embed openapi.yaml
var OpenAPISpec []byte

//go:embed swagger-ui/*
var SwaggerUIFS embed.FS
