package runtime

import (
	"fmt"
	"reflect"
)

func FromGoValue(v any, schemas map[string]StructSchema) (Value, error) {
	if v == nil {
		return NilValue(), nil
	}
	switch v := v.(type) {
	case bool:
		return BoolValue(v), nil
	case int:
		return IntValue(int64(v)), nil
	case int64:
		return Int64Value(v), nil
	case float64:
		return Float64Value(v), nil
	case string:
		return StringValue(v), nil
	}

	rv := reflect.ValueOf(v)
	rt := rv.Type()
	if rt.Kind() != reflect.Struct || rt.Name() == "" {
		return Value{}, fmt.Errorf("runtime: unsupported Go value type %s", rt.String())
	}
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if runtimeKindFromReflect(field.Type) == Invalid {
			return Value{}, fmt.Errorf("runtime: unsupported field type %s on %s.%s", field.Type.String(), rt.Name(), field.Name)
		}
	}
	schema, ok := schemas[rt.Name()]
	if !ok {
		return Value{}, fmt.Errorf("runtime: missing struct schema for %s", rt.Name())
	}
	if len(schema.FieldNames) != rt.NumField() {
		return Value{}, fmt.Errorf("runtime: struct schema field count mismatch for %s", rt.Name())
	}
	fields := make([]Cell, len(schema.FieldNames))
	for i, fieldName := range schema.FieldNames {
		sf, ok := rt.FieldByName(fieldName)
		if !ok {
			return Value{}, fmt.Errorf("runtime: struct %s missing field %s", rt.Name(), fieldName)
		}
		if !sf.IsExported() {
			return Value{}, fmt.Errorf("runtime: field %s on %s must be exported", fieldName, rt.Name())
		}
		if got, want := runtimeKindFromReflect(sf.Type), schema.FieldKinds[i]; got != want {
			return Value{}, fmt.Errorf("runtime: field %s on %s has kind %v, want %v", fieldName, rt.Name(), got, want)
		}
		fv, err := fromReflectValue(rv.FieldByName(fieldName), schemas)
		if err != nil {
			return Value{}, err
		}
		fields[i].Value = CloneForStore(fv)
	}
	return ObjectValue(&Struct{TypeName: schema.Name, Fields: fields}), nil
}

func ToGoValue(v Value, target reflect.Type, schemas map[string]StructSchema) (any, error) {
	if target == nil {
		return nil, fmt.Errorf("runtime: missing target type")
	}
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	switch target.Kind() {
	case reflect.Bool:
		if v.Kind != Bool {
			return nil, fmt.Errorf("runtime: cannot convert %v to %s", v.Kind, target.String())
		}
		return v.Bool(), nil
	case reflect.Int:
		if v.Kind != Int {
			return nil, fmt.Errorf("runtime: cannot convert %v to %s", v.Kind, target.String())
		}
		return int(v.Int()), nil
	case reflect.Int64:
		if v.Kind != Int64 {
			return nil, fmt.Errorf("runtime: cannot convert %v to %s", v.Kind, target.String())
		}
		return v.Int(), nil
	case reflect.Float64:
		if v.Kind != Float64 {
			return nil, fmt.Errorf("runtime: cannot convert %v to %s", v.Kind, target.String())
		}
		return v.Float64(), nil
	case reflect.String:
		if v.Kind != String {
			return nil, fmt.Errorf("runtime: cannot convert %v to %s", v.Kind, target.String())
		}
		return v.String(), nil
	case reflect.Slice:
		elemKind := runtimeKindFromReflect(target.Elem())
		if elemKind == Invalid {
			return nil, fmt.Errorf("runtime: unsupported Go target type %s", target.String())
		}
		obj, ok := v.Object().(*Slice)
		if v.Kind != ObjectKind || !ok || obj == nil {
			return nil, fmt.Errorf("runtime: cannot convert non-slice value to %s", target.String())
		}
		result := reflect.MakeSlice(target, len(obj.Elements), len(obj.Elements))
		for i, elem := range obj.Elements {
			converted, err := toReflectValue(elem.Value, target.Elem(), schemas)
			if err != nil {
				return nil, err
			}
			result.Index(i).Set(converted)
		}
		return result.Interface(), nil
	case reflect.Struct:
		obj, ok := v.Object().(*Struct)
		if v.Kind != ObjectKind || !ok || obj == nil {
			return nil, fmt.Errorf("runtime: cannot convert non-struct value to %s", target.String())
		}
		schema, ok := schemas[target.Name()]
		if !ok {
			return nil, fmt.Errorf("runtime: missing struct schema for %s", target.Name())
		}
		if obj.TypeName != "" && obj.TypeName != schema.Name {
			return nil, fmt.Errorf("runtime: struct type mismatch: got %s, want %s", obj.TypeName, schema.Name)
		}
		if len(obj.Fields) != len(schema.FieldNames) || len(schema.FieldNames) != target.NumField() {
			return nil, fmt.Errorf("runtime: struct field count mismatch for %s", target.Name())
		}
		result := reflect.New(target).Elem()
		for i, fieldName := range schema.FieldNames {
			sf, ok := target.FieldByName(fieldName)
			if !ok {
				return nil, fmt.Errorf("runtime: struct %s missing field %s", target.Name(), fieldName)
			}
			if !sf.IsExported() {
				return nil, fmt.Errorf("runtime: field %s on %s must be exported", fieldName, target.Name())
			}
			if got, want := runtimeKindFromReflect(sf.Type), schema.FieldKinds[i]; got != want {
				return nil, fmt.Errorf("runtime: field %s on %s has kind %v, want %v", fieldName, target.Name(), got, want)
			}
			converted, err := toReflectValue(obj.Fields[i].Value, sf.Type, schemas)
			if err != nil {
				return nil, err
			}
			result.FieldByName(fieldName).Set(converted)
		}
		return result.Interface(), nil
	default:
		return nil, fmt.Errorf("runtime: unsupported Go target type %s", target.String())
	}
}

func fromReflectValue(v reflect.Value, schemas map[string]StructSchema) (Value, error) {
	return FromGoValue(v.Interface(), schemas)
}

func toReflectValue(v Value, target reflect.Type, schemas map[string]StructSchema) (reflect.Value, error) {
	converted, err := ToGoValue(v, target, schemas)
	if err != nil {
		return reflect.Value{}, err
	}
	return reflect.ValueOf(converted), nil
}

func runtimeKindFromReflect(t reflect.Type) Kind {
	switch t.Kind() {
	case reflect.Bool:
		return Bool
	case reflect.Int:
		return Int
	case reflect.Int64:
		return Int64
	case reflect.Float64:
		return Float64
	case reflect.String:
		return String
	default:
		return Invalid
	}
}
