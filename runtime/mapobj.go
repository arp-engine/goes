package runtime

import "fmt"

func EncodeMapKey(v Value) (MapKey, error) {
	switch v.Kind {
	case Bool:
		return MapKey{Kind: Bool, B: v.Bool()}, nil
	case Int:
		return MapKey{Kind: Int, I64: v.Int()}, nil
	case Int64:
		return MapKey{Kind: Int64, I64: v.Int()}, nil
	case Float64:
		return MapKey{Kind: Float64, F64: v.Float64()}, nil
	case String:
		return MapKey{Kind: String, Str: v.String()}, nil
	case PointerKind:
		return MapKey{Kind: PointerKind, Cell: v.Pointer()}, nil
	default:
		return MapKey{}, fmt.Errorf("unsupported map key kind %d", v.Kind)
	}
}
