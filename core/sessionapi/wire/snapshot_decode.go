package wire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// UnmarshalJSON checks the v1 producer's required fields before a client can
// interpret absent actions or inventory as a usable, disconnected session.
func (s *Snapshot) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var decoded Snapshot
	if version, present := fields["protocol_version"]; present {
		if err := json.Unmarshal(version, &decoded.ProtocolVersion); err != nil {
			return fmt.Errorf("snapshot protocol_version: %w", err)
		}
	}
	if decoded.ProtocolVersion == ProtocolVersion {
		if err := decodeFields(fields, reflect.ValueOf(&decoded).Elem()); err != nil {
			return err
		}
	}
	// The caller owns the established restart message for a missing or different version.
	*s = decoded
	return nil
}

func decodeFields(fields map[string]json.RawMessage, target reflect.Value) error {
	for i := 0; i < target.NumField(); i++ {
		field := target.Type().Field(i)
		name, option, _ := strings.Cut(field.Tag.Get("json"), ",")
		raw, present := fields[name]
		if !present {
			if option == "omitempty" {
				continue
			}
			return fmt.Errorf("snapshot is missing %s", name)
		}
		if err := decodeValue(raw, target.Field(i)); err != nil {
			return fmt.Errorf("snapshot %s: %w", name, err)
		}
	}
	return nil
}

func decodeValue(raw json.RawMessage, target reflect.Value) error {
	if bytes.Equal(raw, []byte("null")) {
		if target.Kind() == reflect.Pointer {
			return nil
		}
		return fmt.Errorf("unexpected null for %s", target.Type())
	}
	switch target.Kind() {
	case reflect.Pointer:
		target.Set(reflect.New(target.Type().Elem()))
		return decodeValue(raw, target.Elem())
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		return decodeFields(fields, target)
	case reflect.Slice:
		var rows []json.RawMessage
		if err := json.Unmarshal(raw, &rows); err != nil {
			return err
		}
		target.Set(reflect.MakeSlice(target.Type(), len(rows), len(rows)))
		for i, row := range rows {
			if err := decodeValue(row, target.Index(i)); err != nil {
				return fmt.Errorf("row %d: %w", i, err)
			}
		}
		return nil
	default:
		return json.Unmarshal(raw, target.Addr().Interface())
	}
}
