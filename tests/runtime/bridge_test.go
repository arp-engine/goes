package runtime_test

import (
	"reflect"
	"strings"
	"testing"

	"goes/runtime"
)

type SkillState struct {
	Counter int
	Active  bool
	Name    string
}

func TestBridgeRoundTripsNamedStruct(t *testing.T) {
	schemas := map[string]runtime.StructSchema{
		"SkillState": {
			Name:       "SkillState",
			FieldNames: []string{"Counter", "Active", "Name"},
			FieldKinds: []runtime.Kind{runtime.Int, runtime.Bool, runtime.String},
		},
	}

	value, err := runtime.FromGoValue(SkillState{Counter: 2, Active: true, Name: "fire"}, schemas)
	if err != nil {
		t.Fatalf("FromGoValue: %v", err)
	}

	gotAny, err := runtime.ToGoValue(value, reflect.TypeOf(SkillState{}), schemas)
	if err != nil {
		t.Fatalf("ToGoValue: %v", err)
	}
	got := gotAny.(SkillState)
	if got.Counter != 2 || !got.Active || got.Name != "fire" {
		t.Fatalf("got = %+v", got)
	}
}

func TestBridgeRejectsUnsupportedFieldType(t *testing.T) {
	type Bad struct {
		Items []int
	}

	_, err := runtime.FromGoValue(Bad{Items: []int{1}}, nil)
	if err == nil {
		t.Fatalf("FromGoValue: expected error")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error = %q, want unsupported", err)
	}
}

func TestBridgeRejectsStructKindMismatch(t *testing.T) {
	type SkillState struct {
		Counter int
	}

	schemas := map[string]runtime.StructSchema{
		"SkillState": {
			Name:       "SkillState",
			FieldNames: []string{"Counter"},
			FieldKinds: []runtime.Kind{runtime.String},
		},
	}

	_, err := runtime.FromGoValue(SkillState{Counter: 1}, schemas)
	if err == nil {
		t.Fatalf("FromGoValue: expected error")
	}
	if !strings.Contains(err.Error(), "Counter") {
		t.Fatalf("error = %q, want field name", err)
	}
}
