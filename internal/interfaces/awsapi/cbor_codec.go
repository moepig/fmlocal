package awsapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	smithycbor "github.com/aws/smithy-go/encoding/cbor"
)

// timestampFields is the set of JSON/CBOR field names that hold Unix-epoch timestamps.
// They must be encoded as CBOR Tag 1 so the AWS SDK can deserialise them as time.Time.
var timestampFields = map[string]bool{
	"StartTime":    true,
	"EndTime":      true,
	"CreationTime": true,
}

// cborBodyToJSON decodes a CBOR-encoded body and re-encodes it as JSON so that
// existing handlers can use decodeJSON without modification.
func cborBodyToJSON(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return nil, nil
	}
	cv, err := smithycbor.Decode(body)
	if err != nil {
		return nil, newInvalidRequest("parse cbor body: %v", err)
	}
	if _, ok := cv.(*smithycbor.Nil); ok {
		return nil, nil
	}
	return json.Marshal(cborToGoValue(cv))
}

// cborToGoValue converts a smithycbor.Value to a plain Go value that json.Marshal can handle.
func cborToGoValue(v smithycbor.Value) any {
	switch t := v.(type) {
	case smithycbor.String:
		return string(t)
	case smithycbor.Bool:
		return bool(t)
	case smithycbor.Uint:
		return float64(t)
	case smithycbor.NegInt:
		return -float64(t) - 1
	case smithycbor.Float32:
		return float64(t)
	case smithycbor.Float64:
		return float64(t)
	case smithycbor.Map:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = cborToGoValue(val)
		}
		return m
	case smithycbor.List:
		l := make([]any, len(t))
		for i, val := range t {
			l[i] = cborToGoValue(val)
		}
		return l
	case *smithycbor.Tag:
		// Epoch timestamp tag (1): return the numeric value so JSON gets a number.
		if t != nil && t.ID == 1 {
			return cborToGoValue(t.Value)
		}
		return nil
	default:
		return nil
	}
}

// encodeCBOR serialises a response DTO to CBOR bytes.
func encodeCBOR(v any) ([]byte, error) {
	cv, err := goToCBOR(reflect.ValueOf(v), "")
	if err != nil {
		return nil, err
	}
	return smithycbor.Encode(cv), nil
}

// goToCBOR converts a Go value to a smithycbor.Value.
// fieldName is the JSON key of the field being encoded; it is used to decide
// whether a float64 should be wrapped in CBOR Tag 1 (epoch timestamp).
func goToCBOR(rv reflect.Value, fieldName string) (smithycbor.Value, error) {
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return (*smithycbor.Nil)(nil), nil
		}
		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.String:
		return smithycbor.String(rv.String()), nil
	case reflect.Bool:
		return smithycbor.Bool(rv.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i := rv.Int()
		if i >= 0 {
			return smithycbor.Uint(uint64(i)), nil
		}
		return smithycbor.NegInt(uint64(-i - 1)), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return smithycbor.Uint(rv.Uint()), nil
	case reflect.Float32:
		return smithycbor.Float32(float32(rv.Float())), nil
	case reflect.Float64:
		f := rv.Float()
		if timestampFields[fieldName] {
			return &smithycbor.Tag{ID: 1, Value: smithycbor.Float64(f)}, nil
		}
		return smithycbor.Float64(f), nil
	case reflect.Slice:
		if rv.IsNil() {
			return (*smithycbor.Nil)(nil), nil
		}
		list := make(smithycbor.List, rv.Len())
		for i := range rv.Len() {
			val, err := goToCBOR(rv.Index(i), "")
			if err != nil {
				return nil, err
			}
			list[i] = val
		}
		return list, nil
	case reflect.Map:
		m := smithycbor.Map{}
		for _, key := range rv.MapKeys() {
			val, err := goToCBOR(rv.MapIndex(key), "")
			if err != nil {
				return nil, err
			}
			m[fmt.Sprint(key.Interface())] = val
		}
		return m, nil
	case reflect.Struct:
		return structToCBOR(rv)
	default:
		return (*smithycbor.Nil)(nil), nil
	}
}

// structToCBOR converts a struct to a smithycbor.Map using JSON struct tags for key names.
func structToCBOR(rv reflect.Value) (smithycbor.Map, error) {
	m := smithycbor.Map{}
	rt := rv.Type()
	for i := range rt.NumField() {
		field := rt.Field(i)
		fv := rv.Field(i)

		tag := field.Tag.Get("json")
		name, rest, _ := strings.Cut(tag, ",")
		if name == "" {
			name = field.Name
		}
		if name == "-" {
			continue
		}
		omitempty := strings.Contains(rest, "omitempty")

		if fv.Kind() == reflect.Ptr {
			if fv.IsNil() {
				if !omitempty {
					m[name] = (*smithycbor.Nil)(nil)
				}
				continue
			}
			fv = fv.Elem()
		}

		if omitempty && isZeroValue(fv) {
			continue
		}

		val, err := goToCBOR(fv, name)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", name, err)
		}
		m[name] = val
	}
	return m, nil
}

func isZeroValue(rv reflect.Value) bool {
	switch rv.Kind() {
	case reflect.String:
		return rv.Len() == 0
	case reflect.Bool:
		return !rv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rv.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return rv.Float() == 0
	case reflect.Slice, reflect.Map:
		return rv.IsNil() || rv.Len() == 0
	case reflect.Ptr, reflect.Interface:
		return rv.IsNil()
	default:
		return false
	}
}

// writeCBOR writes an APIError as a CBOR-encoded response with rpc-v2-cbor headers.
func (e *APIError) writeCBOR(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/cbor")
	w.Header().Set("smithy-protocol", "rpc-v2-cbor")
	if e.HTTPStatus == 0 {
		e.HTTPStatus = http.StatusBadRequest
	}
	w.WriteHeader(e.HTTPStatus)
	m := smithycbor.Map{
		"__type":  smithycbor.String(e.TypeName),
		"message": smithycbor.String(e.Message),
	}
	_, _ = w.Write(smithycbor.Encode(m))
}
