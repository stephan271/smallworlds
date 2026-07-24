package firstowner

import (
	"encoding/binary"
	"errors"
)

// This is a minimal CBOR decoder supporting only the subset needed to parse a
// WebAuthn attestation object and a COSE public key: unsigned and negative
// integers, byte and text strings, arrays, and maps. Map keys are normalised to
// int64 (numeric) or string so callers can look them up directly.

var errCBOR = errors.New("invalid CBOR")

func cborDecode(data []byte) (any, []byte, error) {
	if len(data) == 0 {
		return nil, nil, errCBOR
	}
	initial := data[0]
	major := initial >> 5
	minor := initial & 0x1f
	argument, rest, err := cborArgument(minor, data[1:])
	if err != nil {
		return nil, nil, err
	}
	switch major {
	case 0: // unsigned integer
		return argument, rest, nil
	case 1: // negative integer
		return int64(-1) - int64(argument), rest, nil
	case 2: // byte string
		if uint64(len(rest)) < argument {
			return nil, nil, errCBOR
		}
		return append([]byte(nil), rest[:argument]...), rest[argument:], nil
	case 3: // text string
		if uint64(len(rest)) < argument {
			return nil, nil, errCBOR
		}
		return string(rest[:argument]), rest[argument:], nil
	case 4: // array
		items := make([]any, 0, argument)
		for index := uint64(0); index < argument; index++ {
			var item any
			var err error
			item, rest, err = cborDecode(rest)
			if err != nil {
				return nil, nil, err
			}
			items = append(items, item)
		}
		return items, rest, nil
	case 5: // map
		result := make(map[any]any, argument)
		for index := uint64(0); index < argument; index++ {
			var key, value any
			var err error
			key, rest, err = cborDecode(rest)
			if err != nil {
				return nil, nil, err
			}
			value, rest, err = cborDecode(rest)
			if err != nil {
				return nil, nil, err
			}
			result[cborNormalizeKey(key)] = value
		}
		return result, rest, nil
	default:
		return nil, nil, errCBOR
	}
}

func cborArgument(minor byte, data []byte) (uint64, []byte, error) {
	switch {
	case minor < 24:
		return uint64(minor), data, nil
	case minor == 24:
		if len(data) < 1 {
			return 0, nil, errCBOR
		}
		return uint64(data[0]), data[1:], nil
	case minor == 25:
		if len(data) < 2 {
			return 0, nil, errCBOR
		}
		return uint64(binary.BigEndian.Uint16(data)), data[2:], nil
	case minor == 26:
		if len(data) < 4 {
			return 0, nil, errCBOR
		}
		return uint64(binary.BigEndian.Uint32(data)), data[4:], nil
	case minor == 27:
		if len(data) < 8 {
			return 0, nil, errCBOR
		}
		return binary.BigEndian.Uint64(data), data[8:], nil
	default:
		return 0, nil, errCBOR
	}
}

func cborNormalizeKey(key any) any {
	switch typed := key.(type) {
	case uint64:
		return int64(typed)
	case int64:
		return typed
	default:
		return key
	}
}

func cborInt(m map[any]any, key any) (int64, bool) {
	switch value := m[key].(type) {
	case int64:
		return value, true
	case uint64:
		return int64(value), true
	default:
		return 0, false
	}
}

func cborBytes(m map[any]any, key any) ([]byte, bool) {
	value, ok := m[key].([]byte)
	return value, ok
}

func cborString(m map[any]any, key any) (string, bool) {
	value, ok := m[key].(string)
	return value, ok
}

func cborMap(m map[any]any, key any) (map[any]any, bool) {
	value, ok := m[key].(map[any]any)
	return value, ok
}

func cborArray(m map[any]any, key any) ([]any, bool) {
	value, ok := m[key].([]any)
	return value, ok
}
