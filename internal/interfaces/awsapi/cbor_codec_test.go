package awsapi

import (
	"testing"

	smithycbor "github.com/aws/smithy-go/encoding/cbor"
)

func TestEncodeCBORPointerValues(t *testing.T) {
	zero := float64(0)
	empty := ""
	falseValue := false
	data, err := encodeCBOR(struct {
		N       *float64 `json:"N,omitempty"`
		S       *string  `json:"S,omitempty"`
		Enabled *bool    `json:"Enabled,omitempty"`
		Omitted *bool    `json:"Omitted,omitempty"`
		Null    *bool    `json:"Null"`
	}{N: &zero, S: &empty, Enabled: &falseValue})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := smithycbor.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := decoded.(smithycbor.Map)
	if !ok {
		t.Fatalf("decoded type = %T", decoded)
	}
	if m["N"] != smithycbor.Float64(0) || m["S"] != smithycbor.String("") || m["Enabled"] != smithycbor.Bool(false) {
		t.Fatalf("pointer values lost: %#v", m)
	}
	if _, ok := m["Omitted"]; ok {
		t.Fatalf("nil omitempty pointer present: %#v", m)
	}
	if _, ok := m["Null"].(*smithycbor.Nil); !ok {
		t.Fatalf("nil pointer without omitempty = %T", m["Null"])
	}
}
