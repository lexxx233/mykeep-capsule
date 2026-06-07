package store

import (
	"reflect"
	"testing"

	"joyvend.io/internal/vector"
)

func vecIDs(xs []vector.Scored) []int64 {
	out := make([]int64, len(xs))
	for i, x := range xs {
		out[i] = x.ID
	}
	return out
}

func retainVec(t *testing.T, s *Store, bank string, id2vec map[int64][]float32, tags map[int64][]string) {
	t.Helper()
	// insert deterministically by ascending id so rowids match the map keys
	for id := int64(1); int(id) <= len(id2vec); id++ {
		v := id2vec[id]
		in := MemoryInput{Content: "doc", Embedding: v, EmbedModel: "m"}
		if tags != nil {
			in.Tags = tags[id]
		}
		if _, err := s.Retain(bank, []MemoryInput{in}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVec0AvailableAndParity(t *testing.T) {
	s := openTestStore(t)
	if !s.VecAvailable() {
		t.Skip("vec0 backend not available in this build")
	}
	set := map[int64][]float32{
		1: {1, 0, 0, 0},     // identical
		2: {0.9, 0.1, 0, 0}, // near
		3: {0.6, 0.8, 0, 0}, // sim 0.6
		4: {0, 0, 1, 0},     // orthogonal
	}
	retainVec(t, s, "b", set, nil)
	if !s.vecCreated {
		t.Fatal("vec_idx not created after retain with embeddings")
	}

	query := []float32{1, 0, 0, 0}
	knn, err := s.vec0KNN("b", "m", query, 4)
	if err != nil {
		t.Fatal(err)
	}
	bf, err := s.bruteForceSearch("b", "m", query, nil, "any", 4)
	if err != nil {
		t.Fatal(err)
	}

	want := []int64{1, 2, 3, 4}
	if got := vecIDs(knn); !reflect.DeepEqual(got, want) {
		t.Fatalf("vec0 order = %v, want %v", got, want)
	}
	if got := vecIDs(bf); !reflect.DeepEqual(got, want) {
		t.Fatalf("brute-force order = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(vecIDs(knn), vecIDs(bf)) {
		t.Fatalf("vec0 (%v) != brute-force (%v)", vecIDs(knn), vecIDs(bf))
	}
}

func TestVectorSearchTagsRouteToBruteForce(t *testing.T) {
	s := openTestStore(t)
	set := map[int64][]float32{
		1: {1, 0, 0, 0},
		2: {0.95, 0.05, 0, 0},
	}
	retainVec(t, s, "b", set, map[int64][]string{1: {"keep"}, 2: nil})

	// with a tag filter, only the tagged memory may be returned (brute-force path)
	res, err := s.VectorSearch("b", "m", []float32{1, 0, 0, 0}, []string{"keep"}, "any", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ID != 1 {
		t.Fatalf("tag-filtered vector search = %v, want only id 1", vecIDs(res))
	}
}

func TestVec0DeleteRemovesFromIndex(t *testing.T) {
	s := openTestStore(t)
	retainVec(t, s, "b", map[int64][]float32{1: {1, 0, 0, 0}, 2: {0, 1, 0, 0}}, nil)
	if _, err := s.DeleteMemory("b", 1); err != nil {
		t.Fatal(err)
	}
	res, err := s.VectorSearch("b", "m", []float32{1, 0, 0, 0}, nil, "any", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.ID == 1 {
			t.Fatal("deleted memory 1 still returned by vec0 search")
		}
	}
}
