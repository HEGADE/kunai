package server

import "testing"

// The powermetrics reader can't run on the Linux dev box, but its parsing is the
// part that can silently go wrong, so it is tested against captured output shapes.
func TestParsePowermetricsTemp(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want float64
	}{
		{
			name: "intel smc block",
			out: "**** SMC sensors ****\n\n" +
				"CPU Thermal level: 0\n" +
				"CPU die temperature: 51.23 C\n" +
				"GPU die temperature: 44.00 C\n",
			want: 51.23,
		},
		{
			name: "cpu wins over hotter gpu",
			out:  "GPU die temperature: 70.0 C\nCPU die temperature: 55.5 C\n",
			want: 55.5,
		},
		{
			name: "no cpu line falls back to hottest die",
			out:  "GPU die temperature: 61.0 C\nANE die temperature: 48.0 C\n",
			want: 61.0,
		},
		{
			name: "nothing readable",
			out:  "**** SMC sensors ****\nCPU Thermal level: 0\n",
			want: 0,
		},
		{
			name: "empty",
			out:  "",
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parsePowermetricsTemp(tc.out); got != tc.want {
				t.Fatalf("parse = %v, want %v", got, tc.want)
			}
		})
	}
}
