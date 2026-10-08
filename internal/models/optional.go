package models

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
)

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
	// Both fields are set only by UnmarshalJSONFrom; the tags keep any default (de)serialization of
	// them out.
	Present bool `json:"-"`
	Value   *T   `json:"-"`
}

// UnmarshalJSONFrom records that the member was present. The JSON decoder only calls this for
// members that actually appear in the object (including when the value is null), so Present stays
// false for omitted members. A JSON null leaves Value nil to signal removal; any other value is
// decoded into Value.
//
// It is the encoding/json/v2 form so that the caller's options (exact member names, unknown members
// refused) also apply inside Value (ENG-3658). Decoder errors are returned unwrapped so v2 keeps
// their position in the body.
func (o *Optional[T]) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	o.Present = true

	if dec.PeekKind() == 'n' {
		if _, err := dec.ReadToken(); err != nil {
			return err //nolint:wrapcheck // Unwrapped on purpose: see above.
		}

		o.Value = nil

		return nil
	}

	var v T
	if err := json.UnmarshalDecode(dec, &v); err != nil {
		return err //nolint:wrapcheck // Unwrapped on purpose: see above.
	}

	o.Value = &v

	return nil
}
