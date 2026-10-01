#!/usr/bin/env bash
# Runs the same schema, atomic batch and hybrid query as main.go against
# rqlite with the sqlite-vec loadable extension (spike for issue #28).
# Requires: gh, curl. Downloads into ./tmp.
set -euo pipefail
cd "$(dirname "$0")" && mkdir -p tmp/ext && cd tmp
gh release download v10.4.0 --repo rqlite/rqlite --pattern '*linux-amd64.tar.gz' --clobber
gh release download v0.1.9 --repo asg017/sqlite-vec --pattern '*loadable-linux-x86_64.tar.gz' --clobber
tar xzf rqlite-v10.4.0-linux-amd64.tar.gz && tar xzf sqlite-vec-0.1.9-loadable-linux-x86_64.tar.gz -C ext
rm -rf data
./rqlite-v10.4.0-linux-amd64/rqlited -extensions-path="$PWD/ext" -http-addr 127.0.0.1:4001 -raft-addr 127.0.0.1:4002 data &
trap 'kill $!' EXIT
until curl -sf 127.0.0.1:4001/readyz >/dev/null; do sleep 0.5; done
x() { curl -s -XPOST "127.0.0.1:4001/db/$1" -H 'Content-Type: application/json' -d "$2"; echo; }
x execute '["CREATE TABLE docs (id INTEGER PRIMARY KEY, title TEXT NOT NULL, body TEXT NOT NULL, embedding BLOB)",
  "CREATE VIRTUAL TABLE docs_fts USING fts5(title, body, content='"'"'docs'"'"', content_rowid='"'"'id'"'"')"]'
x 'execute?transaction' '[
  ["INSERT INTO docs(id,title,body,embedding) VALUES(?,?,?,vec_f32(?))",1,"Invoice export","Export invoices as CSV files for accounting","[0.9,0.1,0,0]"],
  ["INSERT INTO docs_fts(rowid,title,body) VALUES(?,?,?)",1,"Invoice export","Export invoices as CSV files for accounting"],
  ["INSERT INTO docs(id,title,body,embedding) VALUES(?,?,?,vec_f32(?))",2,"Billing CSV","Download billing data in spreadsheet format","[0.85,0.2,0.05,0]"],
  ["INSERT INTO docs_fts(rowid,title,body) VALUES(?,?,?)",2,"Billing CSV","Download billing data in spreadsheet format"],
  ["INSERT INTO docs(id,title,body,embedding) VALUES(?,?,?,vec_f32(?))",3,"Login page","OIDC login with Keycloak","[0,0,0.9,0.3]"],
  ["INSERT INTO docs_fts(rowid,title,body) VALUES(?,?,?)",3,"Login page","OIDC login with Keycloak"]]'
x query '[["SELECT sqlite_version(), vec_version()"],
  ["WITH fts AS (SELECT rowid AS id, row_number() OVER (ORDER BY rank) AS r FROM docs_fts WHERE docs_fts MATCH ? ORDER BY rank LIMIT 20), vec AS (SELECT id, row_number() OVER (ORDER BY vec_distance_cosine(embedding, vec_f32(?))) AS r FROM docs WHERE embedding IS NOT NULL ORDER BY r LIMIT 20) SELECT d.title, SUM(1.0 / (60 + x.r)) AS score FROM (SELECT * FROM fts UNION ALL SELECT * FROM vec) x JOIN docs d ON d.id = x.id GROUP BY d.id ORDER BY score DESC LIMIT 5","invoices","[0.88,0.15,0,0]"]]'
