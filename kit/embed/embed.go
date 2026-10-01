// Package embed computes text embeddings for hybrid search (ADR-0021):
// the Embedder interface, a deterministic offline hash embedder, and a
// client for the LLM gateway's OpenAI-compatible embeddings endpoint.
package embed

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// Embedder turns texts into vectors of a fixed dimension.
type Embedder interface {
	// Model names the embedding model; vectors of different models are not
	// comparable.
	Model() string
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// HashModel is the model name of the hash embedder.
const HashModel = "hash-256"

// Hash is a deterministic embedder using feature hashing of words and word
// pairs (256 dimensions, L2-normalised). It needs no provider and captures
// lexical overlap only; use it for development, tests and as an offline
// fallback.
type Hash struct{}

// Model returns HashModel.
func (Hash) Model() string { return HashModel }

const hashDims = 256

// Embed embeds each text.
func (Hash) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = hashVector(t)
	}
	return out, nil
}

func hashVector(text string) []float32 {
	v := make([]float32, hashDims)
	words := Words(text)
	add := func(feature string, weight float32) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(feature))
		sum := h.Sum32()
		sign := float32(1)
		if sum&1 == 1 {
			sign = -1
		}
		v[(sum>>1)%hashDims] += sign * weight
	}
	for i, w := range words {
		add(w, 1)
		if i > 0 {
			add(words[i-1]+" "+w, 0.5)
		}
	}
	var norm float64
	for _, x := range v {
		norm += float64(x * x)
	}
	if norm == 0 {
		v[0] = 1 // empty text: a fixed unit vector keeps cosine defined
		return v
	}
	n := float32(math.Sqrt(norm))
	for i := range v {
		v[i] /= n
	}
	return v
}

// Words splits text into lower-case words of letters and digits; it is
// also a rough token count for metering.
func Words(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
