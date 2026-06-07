package ingest

import "testing"

func ptr(s string) *string { return &s }

func TestResolveFactType(t *testing.T) {
	cases := []struct {
		in      *string
		want    string
		wantErr bool
	}{
		{nil, "experience", false},
		{ptr(""), "experience", false},
		{ptr("experience"), "experience", false},
		{ptr("world"), "world", false},
		{ptr("observation"), "observation", false},
		{ptr("mental_model"), "mental_model", false},
		{ptr("bogus"), "", true},
		{ptr("MENTAL_MODEL"), "", true}, // case-sensitive
	}
	for _, c := range cases {
		got, err := resolveFactType(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("resolveFactType(%v): expected error", c.in)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("resolveFactType(%v) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}
