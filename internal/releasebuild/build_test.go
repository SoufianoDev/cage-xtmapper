package releasebuild

import "testing"

func TestLittleCMSPluginLicenseRules(t *testing.T) {
	tests := []struct {
		archive, component, license, header string
	}{
		{
			archive:   "liblcms2_fast_float.a",
			component: "LittleCMS-fast-float",
			license:   "GPL-3.0-or-later",
			header:    "lcms2_fast_float.h",
		},
		{
			archive:   "liblcms2_threaded.a",
			component: "LittleCMS-threaded",
			license:   "GPL-3.0-or-later",
			header:    "lcms2_threaded.h",
		},
	}
	for _, tt := range tests {
		t.Run(tt.archive, func(t *testing.T) {
			got, ok := littleCMSPluginLicenseRule(tt.archive)
			if !ok {
				t.Fatal("plugin archive has no license rule")
			}
			if got.component != tt.component || got.license != tt.license || got.header != tt.header {
				t.Fatalf("license rule = %+v, want component=%q license=%q header=%q",
					got, tt.component, tt.license, tt.header)
			}
		})
	}
	if _, ok := littleCMSPluginLicenseRule("liblcms2_unknown.a"); ok {
		t.Fatal("unknown plugin archive accepted")
	}
}
