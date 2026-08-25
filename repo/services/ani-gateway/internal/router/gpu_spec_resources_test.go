package router

import "testing"

func TestSharesToSharingPolicy(t *testing.T) {
	tests := []struct {
		shares int
		want   string
	}{
		{4, "quarter"},
		{2, "half"},
		{1, ""},
		{3, ""},
		{8, ""},
		{0, ""},
	}
	for _, tc := range tests {
		got := sharesToSharingPolicy(tc.shares)
		if got != tc.want {
			t.Errorf("sharesToSharingPolicy(%d) = %q, want %q", tc.shares, got, tc.want)
		}
	}
}
