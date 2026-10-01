package sqlstore

import (
	"database/sql/driver"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"

	"modernc.org/sqlite"
)

// The vector functions below reproduce the sqlite-vec functions of the same
// names, so identical SQL runs on embedded SQLite and on rqlite with the
// sqlite-vec extension loaded (ADR-0019, ADR-0021). Vectors are blobs of
// little-endian float32 values.

var registerOnce sync.Once

func registerFunctions() {
	registerOnce.Do(func() {
		// Registration fails only on duplicate names, which Once prevents.
		_ = sqlite.RegisterDeterministicScalarFunction("vec_f32", 1, vecF32)
		_ = sqlite.RegisterDeterministicScalarFunction("vec_distance_cosine", 2, vecDistanceCosine)
	})
}

// EncodeFloat32 encodes v in the vector blob format used by vec_f32.
func EncodeFloat32(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}

// vecF32 converts a JSON array of numbers (text) into a vector blob; blobs
// pass through after validation.
func vecF32(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	switch v := args[0].(type) {
	case []byte:
		if len(v) == 0 || len(v)%4 != 0 {
			return nil, errors.New("vec_f32: blob length must be a positive multiple of 4")
		}
		return v, nil
	case string:
		var nums []float32
		if err := json.Unmarshal([]byte(v), &nums); err != nil || len(nums) == 0 {
			return nil, fmt.Errorf("vec_f32: expected a non-empty JSON array of numbers")
		}
		return EncodeFloat32(nums), nil
	default:
		return nil, fmt.Errorf("vec_f32: unsupported argument type %T", v)
	}
}

// vecDistanceCosine returns 1 - cosine similarity of two vectors of equal
// dimension: 0 for identical direction, 1 for orthogonal, 2 for opposite.
func vecDistanceCosine(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	a, okA := args[0].([]byte)
	b, okB := args[1].([]byte)
	if !okA || !okB {
		return nil, errors.New("vec_distance_cosine: arguments must be vector blobs")
	}
	if len(a) != len(b) || len(a) == 0 || len(a)%4 != 0 {
		return nil, fmt.Errorf("vec_distance_cosine: dimension mismatch (%d vs %d bytes)", len(a), len(b))
	}
	var dot, normA, normB float64
	for i := 0; i < len(a); i += 4 {
		x := float64(math.Float32frombits(binary.LittleEndian.Uint32(a[i:])))
		y := float64(math.Float32frombits(binary.LittleEndian.Uint32(b[i:])))
		dot += x * y
		normA += x * x
		normB += y * y
	}
	if normA == 0 || normB == 0 {
		return nil, errors.New("vec_distance_cosine: zero vector has no direction")
	}
	return 1 - dot/(math.Sqrt(normA)*math.Sqrt(normB)), nil
}
