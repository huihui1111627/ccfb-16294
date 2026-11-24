package zupu

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Kind identifies the runtime value representation.
type Kind byte

const (
	KindNull Kind = iota
	KindInt
	KindString
	KindBool
)

// Value is a comparable scalar. Keeping it comparable lets encoded keys and
// Go equality stay cheap and deterministic.
type Value struct {
	Kind Kind
	I    int64
	S    string
	B    bool
}

func Null() Value { return Value{Kind: KindNull} }
func Int(v int64) Value {
	return Value{Kind: KindInt, I: v}
}
func String(v string) Value {
	return Value{Kind: KindString, S: v}
}
func Bool(v bool) Value {
	return Value{Kind: KindBool, B: v}
}

func (v Value) IsNull() bool { return v.Kind == KindNull }

func (v Value) String() string {
	switch v.Kind {
	case KindInt:
		return strconv.FormatInt(v.I, 10)
	case KindString:
		return strconv.Quote(v.S)
	case KindBool:
		if v.B {
			return "true"
		}
		return "false"
	default:
		return "null"
	}
}

// Plain returns a JSON-native value used by the web API.
func (v Value) Plain() any {
	switch v.Kind {
	case KindInt:
		return v.I
	case KindString:
		return v.S
	case KindBool:
		return v.B
	default:
		return nil
	}
}

func (v Value) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.Plain())
}

func ValuesFromAny(args []any) ([]Value, error) {
	out := make([]Value, len(args))
	for i, a := range args {
		v, err := ValueFromAny(a)
		if err != nil {
			return nil, fmt.Errorf("argument %d: %w", i, err)
		}
		out[i] = v
	}
	return out, nil
}

func ValueFromAny(a any) (Value, error) {
	switch x := a.(type) {
	case nil:
		return Null(), nil
	case string:
		return String(x), nil
	case bool:
		return Bool(x), nil
	case float64:
		return Int(int64(x)), nil
	case float32:
		return Int(int64(x)), nil
	case int:
		return Int(int64(x)), nil
	case int64:
		return Int(x), nil
	case json.Number:
		i, err := x.Int64()
		if err != nil {
			f, ferr := x.Float64()
			if ferr != nil {
				return Value{}, err
			}
			i = int64(f)
		}
		return Int(i), nil
	default:
		return Value{}, fmt.Errorf("unsupported value %T", a)
	}
}

// EncodeKey creates an unambiguous length-prefixed key. Separators alone would
// be unsafe because record IDs and place names may contain arbitrary text.
func EncodeKey(vs []Value) string {
	var b strings.Builder
	var num [binary.MaxVarintLen64]byte
	for _, v := range vs {
		switch v.Kind {
		case KindNull:
			b.WriteByte('N')
		case KindInt:
			b.WriteByte('I')
			n := binary.PutVarint(num[:], v.I)
			b.Write(num[:n])
		case KindBool:
			b.WriteByte('B')
			if v.B {
				b.WriteByte(1)
			} else {
				b.WriteByte(0)
			}
		case KindString:
			b.WriteByte('S')
			n := binary.PutUvarint(num[:], uint64(len(v.S)))
			b.Write(num[:n])
			b.WriteString(v.S)
		}
	}
	return b.String()
}

// ParseJSONArgs parses a JSON array while preserving integer precision.
func ParseJSONArgs(raw string) ([]Value, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var xs []any
	if err := dec.Decode(&xs); err != nil {
		return nil, err
	}
	return ValuesFromAny(xs)
}
