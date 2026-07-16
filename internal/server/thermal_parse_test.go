package server

import "testing"

// Captured verbatim from a real Apple Silicon Mac (Mac16,12, macOS 26): the smc
// sampler does not exist there, and `--samplers thermal` prints this.
const realAppleSiliconThermal = `Machine model: Mac16,12
OS version: 25F84
Boot arguments:
Boot time: Mon Jul 13 16:11:25 2026



*** Sampled system activity (Thu Jul 16 19:35:57 2026 +0530) (301.07ms elapsed) ***



**** Thermal pressure ****

Current pressure level: Nominal
`

func TestParseThermalPressure(t *testing.T) {
	cases := []struct{ name, out, want string }{
		{"real apple silicon nominal", realAppleSiliconThermal, "nominal"},
		{"serious", "**** Thermal pressure ****\n\nCurrent pressure level: Serious\n", "serious"},
		{"critical", "Current pressure level: Critical", "critical"},
		{"fair", "Current pressure level: Fair", "fair"},
		{"absent", "**** Thermal pressure ****\n\n(no data)\n", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseThermalPressure(tc.out); got != tc.want {
				t.Fatalf("parse = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPressureTooHot(t *testing.T) {
	for _, hot := range []string{"serious", "critical"} {
		if !pressureTooHot(hot) {
			t.Errorf("%q should be too hot", hot)
		}
	}
	for _, ok := range []string{"nominal", "fair", ""} {
		if pressureTooHot(ok) {
			t.Errorf("%q should not be too hot", ok)
		}
	}
}
