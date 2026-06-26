package models

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// jsonNull is the JSON null literal, used to detect an explicit null member in a
// merge-patch body.
var jsonNull = []byte("null")

// Optional captures the three states a member can take in an RFC 7396 (JSON
// Merge Patch) request body, which a plain pointer cannot distinguish:
//
//   - absent      — Present is false (the member was omitted; leave it unchanged)
//   - explicit null — Present is true, Value is nil (remove the member)
//   - a value     — Present is true, Value is non-nil (set the member)
//
// Its zero value is the "absent" state, so a struct field of this type is correct
// until JSON decoding marks it present. It is a decode-only input helper.
type Optional[T any] struct {
	Present bool
	Value   *T
}

// UnmarshalJSON records that the member was present. encoding/json only calls
// this for members that actually appear in the object (including when the value
// is null), so Present stays false for omitted members. A JSON null leaves Value
// nil to signal removal; any other value is decoded into Value.
func (o *Optional[T]) UnmarshalJSON(data []byte) error {
	o.Present = true

	if bytes.Equal(bytes.TrimSpace(data), jsonNull) {
		o.Value = nil

		return nil
	}

	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("decode optional value: %w", err)
	}

	o.Value = &v

	return nil
}
