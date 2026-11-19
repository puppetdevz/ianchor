package store

import (
	"bytes"
	"encoding/json"
	"math"
	"sort"
	"strconv"
)

// This compatibility encoding preserves Node's list JSON, including dynamic
// SELECT * columns and their original SQLite values.
// schemaColumns is the column order Schema creates.
var schemaColumns = []string{"id", "name", "original_path", "type", "status", "created_at", "updated_at"}

// MarshalJSON writes the columns in JS property order with the values the Node
// driver reports, so the payload keeps SELECT * semantics instead of a fixed
// struct shape.
func (e Entry) MarshalJSON() ([]byte, error) {
	columns := e.order
	if columns == nil {
		columns = schemaColumns
	}
	columns = nodeKeyOrder(columns)
	if !hasColumn(columns, "status") {
		// The Node list command spreads { ...entry, status: live }, so a table
		// whose columns lack status gains the live one at the end.
		columns = append(columns, "status")
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, column := range columns {
		if i > 0 {
			buf.WriteByte(',')
		}
		value, err := marshalNode(e.value(column))
		if err != nil {
			return nil, err
		}
		buf.Write(marshalKey(column))
		buf.WriteByte(':')
		buf.Write(value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// hasColumn reports whether columns already names column.
func hasColumn(columns []string, column string) bool {
	for _, name := range columns {
		if name == column {
			return true
		}
	}
	return false
}

// value returns the reported value of column: the live status the list command
// just computed, the stored value for everything else, the typed field only for
// entries built in code. That is the shape Node's `{ ...entry, status }` spread
// produces, so opaque bytes stay a Buffer wherever they were stored.
func (e Entry) value(column string) any {
	if column == "status" {
		return e.Status
	}
	if stored, ok := e.raw[column]; ok {
		return stored
	}
	switch column {
	case "id":
		return e.ID
	case "name":
		return e.Name
	case "original_path":
		return e.OriginalPath
	case "type":
		return e.Type
	case "created_at":
		return e.CreatedAt
	case "updated_at":
		return e.UpdatedAt
	}
	return nil
}

// nodeKeyOrder puts the columns in the order JSON.stringify would enumerate
// them: canonical array-index keys first, ascending, then the rest in column
// order. A database column named "2" therefore precedes "name" exactly like a
// JavaScript object property does.
func nodeKeyOrder(columns []string) []string {
	indexes := make([]string, 0, len(columns))
	rest := make([]string, 0, len(columns))
	for _, column := range columns {
		if !isArrayIndex(column) {
			rest = append(rest, column)
			continue
		}
		indexes = append(indexes, column)
	}
	sort.Slice(indexes, func(i, j int) bool {
		left, _ := strconv.ParseUint(indexes[i], 10, 64)
		right, _ := strconv.ParseUint(indexes[j], 10, 64)
		return left < right
	})
	return append(indexes, rest...)
}

// isArrayIndex reports whether name is a canonical array index, the only string
// keys JavaScript orders numerically instead of by insertion.
func isArrayIndex(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if name[i] < '0' || name[i] > '9' {
			return false
		}
	}
	if len(name) > 1 && name[0] == '0' {
		return false
	}
	value, err := strconv.ParseUint(name, 10, 64)
	return err == nil && value <= math.MaxUint32-1
}

// marshalKey encodes a column name with JSON string rules. Encoding/json writes
// the line separators U+2028 and U+2029 as escapes while JSON.stringify leaves
// them in place, so they are put back.
func marshalKey(column string) []byte {
	encoded, _ := marshalJSON(column)
	encoded = bytes.ReplaceAll(encoded, []byte(`\u2028`), []byte("\u2028"))
	return bytes.ReplaceAll(encoded, []byte(`\u2029`), []byte("\u2029"))
}

// marshalNode encodes a stored value the way the Node driver reports it: opaque
// bytes arrive as a Buffer, numbers as JavaScript numbers (so non-finite values
// become null and integers beyond 2^53 round), null as null.
func marshalNode(value any) ([]byte, error) {
	switch v := value.(type) {
	case nil:
		return []byte("null"), nil
	case *string:
		if v == nil {
			return []byte("null"), nil
		}
		return marshalJSON(*v)
	case string:
		return marshalJSON(v)
	case int64:
		return marshalNumber(float64(v))
	case float64:
		return marshalNumber(v)
	case bool:
		return marshalJSON(v)
	case []byte:
		return marshalBuffer(v), nil
	}
	return marshalJSON(value)
}

// marshalNumber encodes a JavaScript number. JSON.stringify writes null for NaN
// and the infinities, which is what a caller sees instead of a failed command.
func marshalNumber(value float64) ([]byte, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return []byte("null"), nil
	}
	return marshalJSON(value)
}

// marshalBuffer encodes bytes as the object JSON.stringify builds for a Node
// Buffer, including the empty one.
func marshalBuffer(value []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString(`{"type":"Buffer","data":[`)
	for i, b := range value {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(strconv.Itoa(int(b)))
	}
	buf.WriteString("]}")
	return buf.Bytes()
}

// marshalJSON encodes value without escaping <, > and &, matching
// JSON.stringify in the Node implementation.
func marshalJSON(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
