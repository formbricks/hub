package models

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// openAPIParameters is the slice of openapi.yaml this test cares about: the query parameter
// components and the two bounds that mirror a Go-side cap.
type openAPIParameters struct {
	Components struct {
		Parameters map[string]struct {
			Name   string `yaml:"name"`
			Schema struct {
				Type     string `yaml:"type"`
				MaxItems *int   `yaml:"maxItems"`
				Items    struct {
					MaxLength *int `yaml:"maxLength"`
				} `yaml:"items"`
			} `yaml:"schema"`
		} `yaml:"parameters"`
	} `yaml:"components"`
}

// TestOpenAPICapsMatchTheFilterStruct closes the second half of the caps invariant.
//
// TestFilterValueCapsMatchTheirSets ties each `max=` tag to the thing it mirrors, but nothing tied
// the *published spec* back to those tags: adding a seventh emotion means editing the enum, the
// struct tag and `maxItems` in three places, and only the first two had a test. A spec that
// understates the real bound is the bad direction — schemathesis fuzzes inside the documented range,
// so it would keep passing while integrators are told the API accepts less than it does.
//
// Checks both bounds, since they drift for the same reason: `maxItems` against the slice cap and
// `items.maxLength` against the per-element cap.
func TestOpenAPICapsMatchTheFilterStruct(t *testing.T) {
	t.Parallel()

	spec := loadOpenAPIParameters(t)

	byName := make(map[string]string, len(spec.Components.Parameters))
	for key, parameter := range spec.Components.Parameters {
		if parameter.Name != "" {
			byName[parameter.Name] = key
		}
	}

	enumCaps := map[string]int{
		"field_type": len(ValidFieldTypeValues()),
		"sentiment":  len(SentimentValues()),
		"emotions":   len(EmotionValues()),
	}

	seen := map[string]bool{}

	for field := range reflect.TypeFor[ListFeedbackRecordsFilters]().Fields() {
		if field.Type.Kind() != reflect.Slice {
			continue
		}

		name, _, _ := strings.Cut(field.Tag.Get("form"), ",")

		key, documented := byName[name]
		if !documented {
			t.Fatalf("repeatable filter %q has no parameter in openapi.yaml; the spec cannot describe a filter it omits", name)
		}

		parameter := spec.Components.Parameters[key]

		if parameter.Schema.Type != "array" {
			t.Fatalf("filter %q is a repeatable slice in Go but %q in the spec", name, parameter.Schema.Type)
		}

		wantItems := MaxFilterValues
		if enumCap, isEnum := enumCaps[name]; isEnum {
			wantItems = enumCap
		}

		if parameter.Schema.MaxItems == nil {
			t.Fatalf("filter %q has no maxItems in openapi.yaml, so the spec documents an unbounded IN-list", name)
		}

		if *parameter.Schema.MaxItems != wantItems {
			t.Fatalf("filter %q: openapi.yaml maxItems=%d, Go caps at %d", name, *parameter.Schema.MaxItems, wantItems)
		}

		// Element width only applies to the string filters; the enum ones constrain by $ref instead.
		if _, isEnum := enumCaps[name]; !isEnum {
			_, elementPart, hasDive := strings.Cut(field.Tag.Get("validate"), ",dive,")

			if wantLength, ok := maxTagValue(t, elementPart); hasDive && ok {
				if parameter.Schema.Items.MaxLength == nil {
					t.Fatalf("filter %q caps elements at %d in Go but the spec sets no items.maxLength", name, wantLength)
				}

				if *parameter.Schema.Items.MaxLength != wantLength {
					t.Fatalf("filter %q: openapi.yaml items.maxLength=%d, Go caps at %d",
						name, *parameter.Schema.Items.MaxLength, wantLength)
				}
			}
		}

		seen[name] = true
	}

	// Guards against the struct being refactored out from under the loop and this test silently
	// asserting nothing — the same failure mode its sibling in feedback_records_filters_test.go
	// guards the same way. The enum filters are named explicitly because they are the ones whose
	// cap is a cardinality that moves when someone adds a value.
	for name := range enumCaps {
		if !seen[name] {
			t.Fatalf("enum filter %q is no longer a slice field; this test is checking nothing for it", name)
		}
	}

	if len(seen) < len(enumCaps) {
		t.Fatalf("only %d repeatable filters checked; the struct has none, so this test asserts nothing", len(seen))
	}
}

func loadOpenAPIParameters(t *testing.T) openAPIParameters {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve this test file's path")
	}

	specPath := filepath.Join(filepath.Dir(filename), "..", "..", "openapi.yaml")
	// #nosec G304 -- the repository-local spec path is derived from this test file's location.
	contents, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}

	var spec openAPIParameters
	if err := yaml.Unmarshal(contents, &spec); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}

	if len(spec.Components.Parameters) == 0 {
		t.Fatal("openapi.yaml parsed to zero parameters; this test would assert nothing")
	}

	return spec
}

// openAPIOperationParameters is the slice of openapi.yaml naming each list operation's query
// parameters, either inline or by $ref to a shared component.
type openAPIOperationParameters struct {
	Paths map[string]struct {
		Get struct {
			Parameters []struct {
				Ref    string `yaml:"$ref"`
				Name   string `yaml:"name"`
				Schema struct {
					Format string `yaml:"format"`
				} `yaml:"schema"`
			} `yaml:"parameters"`
		} `yaml:"get"`
	} `yaml:"paths"`
	Components struct {
		Parameters map[string]struct {
			Name   string `yaml:"name"`
			Schema struct {
				Format string `yaml:"format"`
			} `yaml:"schema"`
		} `yaml:"parameters"`
	} `yaml:"components"`
}

// TestOpenAPIDocumentsEveryFilterByItsFormName pins the public parameter names to the struct's form
// tags, in both directions. The integration tests set struct fields directly, so a tag typo such as
// `form:"updated_untl"` would pass every other test while the decoder silently ignored the real
// parameter — for updated_since (ENG-3420), a sync that reads the whole tenant every run.
//
// List must document every field; count every field except the four pagination and ordering ones,
// which it does not accept. Every documented parameter must in turn decode into a field, and every
// timestamp field must be documented as date-time.
func TestOpenAPIDocumentsEveryFilterByItsFormName(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve this test file's path")
	}

	// #nosec G304 -- the repository-local spec path is derived from this test file's location.
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "..", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}

	var spec openAPIOperationParameters
	if err := yaml.Unmarshal(contents, &spec); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}

	documented := func(path string) map[string]string {
		formats := map[string]string{}

		for _, parameter := range spec.Paths[path].Get.Parameters {
			if parameter.Ref == "" {
				formats[parameter.Name] = parameter.Schema.Format

				continue
			}

			component, found := spec.Components.Parameters[strings.TrimPrefix(parameter.Ref, "#/components/parameters/")]
			if !found {
				t.Fatalf("%s references %s, which openapi.yaml does not define", path, parameter.Ref)
			}

			formats[component.Name] = component.Schema.Format
		}

		return formats
	}

	listOnly := map[string]bool{"sort": true, "order": true, "limit": true, "cursor": true}
	timeType := reflect.TypeFor[*time.Time]()
	formNames := map[string]bool{}

	for _, operation := range []struct {
		path      string
		formats   map[string]string
		skipNames map[string]bool
	}{
		{"/v1/feedback-records", documented("/v1/feedback-records"), nil},
		{"/v1/feedback-records/count", documented("/v1/feedback-records/count"), listOnly},
	} {
		if len(operation.formats) == 0 {
			t.Fatalf("%s documents no parameters; this test would check nothing", operation.path)
		}

		for field := range reflect.TypeFor[ListFeedbackRecordsFilters]().Fields() {
			name, _, _ := strings.Cut(field.Tag.Get("form"), ",")
			formNames[name] = true

			if operation.skipNames[name] {
				continue
			}

			format, isDocumented := operation.formats[name]
			if !isDocumented {
				t.Fatalf("%s does not document %q, the form name of %s", operation.path, name, field.Name)
			}

			if field.Type == timeType && format != "date-time" {
				t.Fatalf("%s documents %q with format %q; %s is a timestamp", operation.path, name, format, field.Name)
			}
		}

		for name := range operation.formats {
			if !formNames[name] {
				t.Fatalf("%s documents %q, which no filter field decodes; the spec names a parameter the API ignores", operation.path, name)
			}
		}
	}
}
