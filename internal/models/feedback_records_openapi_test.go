package models

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type openAPINumberBounds struct {
	Components struct {
		Schemas map[string]struct {
			Properties map[string]struct {
				Minimum *float64 `yaml:"minimum"`
				Maximum *float64 `yaml:"maximum"`
			} `yaml:"properties"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

// TestOpenAPIValueNumberBoundsMatchTheValidateTag pins value_number's documented minimum/maximum to
// the validate tag the server enforces. ENG-2276 was the two drifting apart: the spec documented
// ±1e15 while the request structs had no bound, so the Hub stored values the spec called invalid.
func TestOpenAPIValueNumberBoundsMatchTheValidateTag(t *testing.T) {
	t.Parallel()

	var spec openAPINumberBounds
	loadOpenAPISpec(t, &spec)

	cases := map[string]reflect.Type{
		"CreateFeedbackRecordInputBody": reflect.TypeFor[CreateFeedbackRecordRequest](),
		"UpdateFeedbackRecordInputBody": reflect.TypeFor[UpdateFeedbackRecordRequest](),
	}

	for schema, structType := range cases {
		property, documented := spec.Components.Schemas[schema].Properties["value_number"]
		if !documented || property.Minimum == nil || property.Maximum == nil {
			t.Errorf("%s.value_number has no minimum/maximum in openapi.yaml", schema)

			continue
		}

		field, ok := structType.FieldByName("ValueNumber")
		if !ok {
			t.Fatalf("%s has no ValueNumber field", structType.Name())
		}

		tag := field.Tag.Get("validate")
		gte, lte := validateNumberBounds(t, tag)

		if gte == nil || *gte != *property.Minimum {
			t.Errorf("%s.ValueNumber validate tag %q: gte does not match openapi.yaml %s.value_number minimum %g",
				structType.Name(), tag, schema, *property.Minimum)
		}

		if lte == nil || *lte != *property.Maximum {
			t.Errorf("%s.ValueNumber validate tag %q: lte does not match openapi.yaml %s.value_number maximum %g",
				structType.Name(), tag, schema, *property.Maximum)
		}
	}
}

// validateNumberBounds returns the gte and lte values of a validate tag, nil where a rule is absent.
func validateNumberBounds(t *testing.T, tag string) (gte, lte *float64) {
	t.Helper()

	for rule := range strings.SplitSeq(tag, ",") {
		name, raw, found := strings.Cut(rule, "=")
		if !found || (name != "gte" && name != "lte") {
			continue
		}

		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			t.Fatalf("validate tag %q: %s is not a number: %v", tag, rule, err)
		}

		if name == "gte" {
			gte = &value
		} else {
			lte = &value
		}
	}

	return gte, lte
}
