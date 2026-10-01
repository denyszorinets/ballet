package main

import (
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math"
	"os"

	"modernc.org/sqlite"
)

func f32blob(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}

// vec_distance_cosine with sqlite-vec semantics: 1 - cosine similarity of two
// little-endian float32 blobs of equal length.
func cosineDistance(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	a, ok1 := args[0].([]byte)
	b, ok2 := args[1].([]byte)
	if !ok1 || !ok2 || len(a) != len(b) || len(a)%4 != 0 {
		return nil, errors.New("vec_distance_cosine: vectors must be float32 blobs of equal length")
	}
	var dot, na, nb float64
	for i := 0; i < len(a); i += 4 {
		x := float64(math.Float32frombits(binary.LittleEndian.Uint32(a[i:])))
		y := float64(math.Float32frombits(binary.LittleEndian.Uint32(b[i:])))
		dot, na, nb = dot+x*y, na+x*x, nb+y*y
	}
	return 1 - dot/(math.Sqrt(na)*math.Sqrt(nb)), nil
}

const schema = `
CREATE TABLE docs (id INTEGER PRIMARY KEY, title TEXT NOT NULL, body TEXT NOT NULL, embedding BLOB);
CREATE VIRTUAL TABLE docs_fts USING fts5(title, body, content='docs', content_rowid='id');
`

const hybrid = `
WITH fts AS (
  SELECT rowid AS id, row_number() OVER (ORDER BY rank) AS r
  FROM docs_fts WHERE docs_fts MATCH ? ORDER BY rank LIMIT 20
), vec AS (
  SELECT id, row_number() OVER (ORDER BY vec_distance_cosine(embedding, ?)) AS r
  FROM docs WHERE embedding IS NOT NULL
  ORDER BY r LIMIT 20
)
SELECT d.title, SUM(1.0 / (60 + x.r)) AS score
FROM (SELECT * FROM fts UNION ALL SELECT * FROM vec) x JOIN docs d ON d.id = x.id
GROUP BY d.id ORDER BY score DESC LIMIT 5`

func main() {
	if err := sqlite.RegisterDeterministicScalarFunction("vec_distance_cosine", 2, cosineDistance); err != nil {
		log.Fatal(err)
	}
	_ = os.Remove("spike.db")
	db, err := sql.Open("sqlite", "file:spike.db?_pragma=journal_mode(wal)")
	if err != nil {
		log.Fatal(err)
	}
	var sv string
	if err := db.QueryRow("SELECT sqlite_version()").Scan(&sv); err != nil {
		log.Fatal(err)
	}
	fmt.Println("sqlite", sv)
	if _, err := db.Exec(schema); err != nil {
		log.Fatal("schema: ", err)
	}
	docs := []struct {
		t, b string
		v    []float32
	}{
		{"Invoice export", "Export invoices as CSV files for accounting", []float32{0.9, 0.1, 0, 0}},
		{"Billing CSV", "Download billing data in spreadsheet format", []float32{0.85, 0.2, 0.05, 0}},
		{"Login page", "OIDC login with Keycloak", []float32{0, 0, 0.9, 0.3}},
	}
	for i, d := range docs {
		// One atomic batch: row + FTS entry (rqlite-compatible: no interactive tx).
		if _, err := db.Exec(`INSERT INTO docs(id,title,body,embedding) VALUES(?,?,?,?); INSERT INTO docs_fts(rowid,title,body) VALUES(?,?,?);`,
			i+1, d.t, d.b, f32blob(d.v), i+1, d.t, d.b); err != nil {
			log.Fatal(err)
		}
	}
	rows, err := db.Query(hybrid, "invoices", f32blob([]float32{0.88, 0.15, 0, 0}))
	if err != nil {
		log.Fatal("hybrid: ", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		var s float64
		if err := rows.Scan(&t, &s); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%-16s %.4f\n", t, s)
	}
}
