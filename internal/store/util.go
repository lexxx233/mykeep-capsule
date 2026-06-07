package store

import "time"

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func ptr[T any](v T) *T { return &v }

func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func isoFromUnix(sec int64) string {
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}
