package core

import (
	"encoding/json"
	"fmt"
	"io"
)

// DecodeStrictJSON decodes one value, rejecting unknown fields and trailing data.
// Numbers in untyped fields keep their original precision.
func DecodeStrictJSON(r io.Reader, dst any) error {
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	d.UseNumber()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
