// Package llm holds provider-agnostic types shared by the LLM client wrappers
// (internal/openai, internal/googleai). It lets a structured-output request be
// described once, in SDK-neutral terms, and rendered to each provider's native
// schema type — so callers (e.g. the sentiment enrichment) depend on this
// contract, not on a specific provider's JSON-schema representation.
package llm

// PropertyType is the JSON-schema scalar type of a structured-output field. The
// set is intentionally the common subset that both OpenAI strict mode and Gemini
// responseSchema enforce.
type PropertyType string

const (
	// TypeString is a JSON string (optionally constrained to a fixed Enum set).
	TypeString PropertyType = "string"
	// TypeNumber is a JSON number (float/double).
	TypeNumber PropertyType = "number"
	// TypeInteger is a JSON integer.
	TypeInteger PropertyType = "integer"
	// TypeBoolean is a JSON boolean.
	TypeBoolean PropertyType = "boolean"
)

// Property is one field of a structured-output object schema.
type Property struct {
	// Name is the JSON key. Must be unique within a Schema.
	Name string
	// Type is the field's scalar JSON type.
	Type PropertyType
	// Description is an optional hint the model uses to fill the field; it is
	// passed through to the provider schema rather than relying on the prompt.
	Description string
	// Enum, when non-empty, restricts a TypeString field to these exact values.
	Enum []string
}

// Schema describes the JSON object an LLM must return. Every property is
// required and the object is closed (additionalProperties:false) — the common
// subset OpenAI strict mode and Gemini responseSchema both guarantee. Numeric
// bounds are deliberately omitted (OpenAI strict mode does not support
// minimum/maximum), so callers validate ranges after parsing.
type Schema struct {
	// Name identifies the schema. OpenAI requires it (a-z, A-Z, 0-9, underscore,
	// dash; max 64 chars); Gemini ignores it.
	Name string
	// Properties are the object's fields, in a stable order (preserved as the
	// provider property ordering where supported).
	Properties []Property
}
