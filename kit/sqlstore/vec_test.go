package sqlstore_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/sqlstore"
	"github.com/denyszorinets/ballet/kit/sqlstore/sqlstoretest"
)

func TestVecFunctions_MatchSqliteVecSemantics(t *testing.T) {
	db := sqlstoretest.New(t, nil)

	tests := []struct {
		name string
		a, b string
		want float64
	}{
		{name: "identical vectors", a: "[1,2,3]", b: "[1,2,3]", want: 0},
		{name: "orthogonal vectors", a: "[1,0]", b: "[0,1]", want: 1},
		{name: "opposite vectors", a: "[1,0]", b: "[-1,0]", want: 2},
		{name: "scale invariant", a: "[1,1]", b: "[3,3]", want: 0},
		{name: "45 degrees", a: "[1,0]", b: "[1,1]", want: 0.29289321881345254},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got float64
			err := db.QueryRow(t.Context(),
				"SELECT vec_distance_cosine(vec_f32(?), vec_f32(?))", tt.a, tt.b).Scan(&got)

			require.NoError(t, err)
			assert.InDelta(t, tt.want, got, 1e-6)
		})
	}
}

func TestVecF32_AcceptsBlobsProducedByEncodeFloat32(t *testing.T) {
	db := sqlstoretest.New(t, nil)

	var got float64
	err := db.QueryRow(t.Context(), "SELECT vec_distance_cosine(?, vec_f32('[0.5,0.25]'))",
		sqlstore.EncodeFloat32([]float32{0.5, 0.25})).Scan(&got)

	require.NoError(t, err)
	assert.InDelta(t, 0, got, 1e-6)
}

func TestVecFunctions_RejectInvalidInput(t *testing.T) {
	db := sqlstoretest.New(t, nil)

	for _, q := range []string{
		"SELECT vec_f32('not json')",
		"SELECT vec_distance_cosine(vec_f32('[1,2]'), vec_f32('[1,2,3]'))",
		"SELECT vec_distance_cosine(vec_f32('[0,0]'), vec_f32('[1,1]'))",
	} {
		var v any
		err := db.QueryRow(t.Context(), q).Scan(&v)
		assert.Error(t, err, q)
	}
}
