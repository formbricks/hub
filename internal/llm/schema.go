// Package llm holds provider-agnostic types shared by the LLM client wrappers
// (internal/openai, internal/googleai). It lets a structured-output request be
// described once, in SDK-neutral terms, and rendered to each provider's native
// schema type — so callers (e.g. the sentiment enrichment) depend on this
// contract, not on a specific provider's JSON-schema representation.
package llm

// PropertyType is the JSON-schema type of a structured-output field. The set is
// intentionally the common subset that both OpenAI strict mode and Gemini
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
	// TypeArray is a JSON array; its element schema is given by Property.Items
	// (e.g. an array of enum strings for a multi-label classification).
	TypeArray PropertyType = "array"
)

// Property is one field of a structured-output object schema.
type Property struct {
	// Name is the JSON key. Must be unique within a Schema. Unused for an array's
	// Items element, which is unnamed.
	Name string
	// Type is the field's JSON type.
	Type PropertyType
	// Description is an optional hint the model uses to fill the field; it is
	// passed through to the provider schema rather than relying on the prompt.
	Description string
	// Enum, when non-empty, restricts a TypeString field to these exact values.
	// For an array of enums, set it on Items (the element), not on the array.
	Enum []string
	// Items describes the element schema when Type is TypeArray; it is ignored for
	// scalar types. Its Name is unused (array elements are unnamed).
	Items *Property
}

// Schema describes the JSON object an LLM must return. Every property is
// required and the object is closed (additionalProperties:false). Numeric
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

// JSONSchema renders the schema to a standard JSON Schema object: a closed object
// (additionalProperties:false) with every property required. Both OpenAI Structured Outputs
// (response_format json_schema, strict) and Gemini responseJsonSchema enforce this subset, so
// the same builder feeds both client wrappers — the closed-object contract is identical on
// every provider.
func (s Schema) JSONSchema() map[string]any {
	properties := make(map[string]any, len(s.Properties))
	required := make([]string, 0, len(s.Properties))

	for _, p := range s.Properties {
		properties[p.Name] = renderProperty(p)
		required = append(required, p.Name)
	}

	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

// renderProperty renders one property — or an array's element — to a JSON Schema fragment: its
// type, optional description and enum, and, for a TypeArray, its element schema under "items".
// It recurses through Items so an array of enum strings renders as
// {"type":"array","items":{"type":"string","enum":[…]}}.
func renderProperty(p Property) map[string]any {
	rendered := map[string]any{"type": string(p.Type)}
	if p.Description != "" {
		rendered["description"] = p.Description
	}

	if len(p.Enum) > 0 {
		rendered["enum"] = p.Enum
	}

	if p.Type == TypeArray && p.Items != nil {
		rendered["items"] = renderProperty(*p.Items)
	}

	return rendered
}
