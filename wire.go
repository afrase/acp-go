package acp

import (
	"bytes"
	"encoding/json"
	"strconv"
	"unicode/utf8"
)

// encodeValue encodes v as compact JSON, without HTML escapes; nil for
// null (params then go unsent).
func encodeValue(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	out := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	if bytes.Equal(out, []byte("null")) {
		return nil, nil
	}
	return out, nil
}

// The messages, each a line ending in "\n".

func encodeRequest(id int64, method string, params []byte) []byte {
	b := make([]byte, 0, 48+len(method)+len(params))
	b = append(b, `{"jsonrpc":"2.0","id":`...)
	b = strconv.AppendInt(b, id, 10)
	return endMessage(b, method, params)
}

func encodeNotification(method string, params []byte) []byte {
	b := make([]byte, 0, 40+len(method)+len(params))
	b = append(b, `{"jsonrpc":"2.0"`...)
	return endMessage(b, method, params)
}

func endMessage(b []byte, method string, params []byte) []byte {
	b = append(b, `,"method":`...)
	b = appendString(b, method)
	if params != nil {
		b = append(b, `,"params":`...)
		b = append(b, params...)
	}
	return append(b, "}\n"...)
}

func encodeResult(id json.RawMessage, result any) ([]byte, error) {
	r, err := encodeValue(result)
	if err != nil {
		return nil, err
	}
	if r == nil {
		r = []byte("null")
	}
	b := make([]byte, 0, 40+len(id)+len(r))
	b = append(b, `{"jsonrpc":"2.0","id":`...)
	b = append(b, id...)
	b = append(b, `,"result":`...)
	b = append(b, r...)
	return append(b, "}\n"...), nil
}

func encodeError(id json.RawMessage, e *Error) []byte {
	if id == nil {
		id = json.RawMessage("null")
	}
	ej, err := encodeValue(e)
	if err != nil { // its data isn't JSON: send it without
		ej, _ = encodeValue(&Error{Code: e.Code, Message: e.Message}) //nolint:errchkjson // a code and a string
	}
	b := make([]byte, 0, 40+len(id)+len(ej))
	b = append(b, `{"jsonrpc":"2.0","id":`...)
	b = append(b, id...)
	b = append(b, `,"error":`...)
	b = append(b, ej...)
	return append(b, "}\n"...)
}

// appendString appends s as a JSON string.
func appendString(b []byte, s string) []byte {
	const hex = "0123456789abcdef"
	b = append(b, '"')
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"' || c == '\\':
			b = append(b, '\\', c)
			i++
		case c < 0x20:
			b = append(b, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
			i++
		case c < utf8.RuneSelf:
			b = append(b, c)
			i++
		default:
			r, n := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && n == 1 {
				b = append(b, `�`...)
			} else {
				b = append(b, s[i:i+n]...)
			}
			i += n
		}
	}
	return append(b, '"')
}
