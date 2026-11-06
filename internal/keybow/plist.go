package keybow

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// decodePlist reads an XML property list -- what `ioreg -a` prints -- into
// plain values: map[string]any, []any, string, int64, bool, and []byte for
// data. Only what ioreg emits is understood; that is all this is for.
func decodePlist(r io.Reader) (any, error) {
	d := xml.NewDecoder(r)
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		if start, ok := tok.(xml.StartElement); ok && start.Name.Local == "plist" {
			value, err := plistValue(d, nil)
			if err != nil {
				return nil, err
			}
			return value, nil
		}
	}
}

// plistValue reads the next value, or the one whose start element has
// already been read.
func plistValue(d *xml.Decoder, start *xml.StartElement) (any, error) {
	if start == nil {
		for {
			tok, err := d.Token()
			if err != nil {
				return nil, err
			}
			if s, ok := tok.(xml.StartElement); ok {
				start = &s
				break
			}
		}
	}
	switch start.Name.Local {
	case "dict":
		out := map[string]any{}
		for {
			tok, err := d.Token()
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.EndElement:
				return out, nil
			case xml.StartElement:
				if t.Name.Local != "key" {
					return nil, fmt.Errorf("plist: <%s> where a key belongs", t.Name.Local)
				}
				var key string
				if err := d.DecodeElement(&key, &t); err != nil {
					return nil, err
				}
				value, err := plistValue(d, nil)
				if err != nil {
					return nil, err
				}
				out[key] = value
			}
		}
	case "array":
		out := []any{}
		for {
			tok, err := d.Token()
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.EndElement:
				return out, nil
			case xml.StartElement:
				value, err := plistValue(d, &t)
				if err != nil {
					return nil, err
				}
				out = append(out, value)
			}
		}
	case "true", "false":
		if err := d.Skip(); err != nil {
			return nil, err
		}
		return start.Name.Local == "true", nil
	default:
		var text string
		if err := d.DecodeElement(&text, start); err != nil {
			return nil, err
		}
		switch start.Name.Local {
		case "integer":
			return strconv.ParseInt(strings.TrimSpace(text), 0, 64)
		case "data":
			return []byte(strings.TrimSpace(text)), nil
		default: // string, date, real: kept as text
			return text, nil
		}
	}
}
