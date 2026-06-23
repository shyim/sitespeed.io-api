package kubernetes

import (
	"maps"
	"testing"
)

func TestParseNodeSelector(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want map[string]string
	}{
		{name: "empty", raw: "", want: nil},
		{name: "whitespace only", raw: "   ", want: nil},
		{name: "single pair", raw: "disktype=ssd", want: map[string]string{"disktype": "ssd"}},
		{
			name: "multiple pairs",
			raw:  "disktype=ssd,pool=ci",
			want: map[string]string{"disktype": "ssd", "pool": "ci"},
		},
		{
			name: "trims whitespace around pairs",
			raw:  " disktype = ssd , pool = ci ",
			want: map[string]string{"disktype": "ssd", "pool": "ci"},
		},
		{
			name: "skips entries without a key",
			raw:  "=ssd,pool=ci",
			want: map[string]string{"pool": "ci"},
		},
		{
			name: "skips entries without an equals sign",
			raw:  "broken,pool=ci",
			want: map[string]string{"pool": "ci"},
		},
		{name: "empty value is allowed", raw: "drain=", want: map[string]string{"drain": ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseNodeSelector(tt.raw)
			if !maps.Equal(got, tt.want) {
				t.Errorf("parseNodeSelector(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}
