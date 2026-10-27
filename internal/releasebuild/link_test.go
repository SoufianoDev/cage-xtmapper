package releasebuild

import (
	"strings"
	"testing"
)

func TestNativeContractDropsMesonDiagnosticLinkerFlag(t *testing.T) {
	contract, err := NativeContract(
		"/tmp/build/meson",
		"/tmp/source",
		"cc -o cage_xtmapper target.o /tmp/build/meson/subprojects/wlroots/libwlroots-0.20.a -Wl,--fatal-warnings -pthread",
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(contract, "-Wl,--fatal-warnings") {
		t.Fatalf("diagnostic-only flag was forwarded to cgo:\n%s", contract)
	}
	if !strings.Contains(contract, "/tmp/build/meson/subprojects/wlroots/libwlroots-0.20.a") {
		t.Fatalf("native static archive was dropped from contract:\n%s", contract)
	}
	if !strings.Contains(contract, "-pthread") {
		t.Fatalf("required link option was dropped from contract:\n%s", contract)
	}
}
