package sbctl

import (
	"sbctl/contracts"
	"strings"
	"testing"
)

// The release workflow compares the tag with the binary only. Keep the other
// two places that carry the version in step with it.
func TestVersionIsConsistentAcrossReleaseFiles(t *testing.T) {
	if got := str(mapping(object(contracts.OpenAPI)["info"])["version"]); got != Version {
		t.Errorf("contracts/openapi.json info.version is %s, binary is %s", got, Version)
	}
	compose := string(read("../../ops/controller/compose.yaml"))
	if want := "ghcr.io/domoxiaojun/sbctl:v" + Version + "}"; !strings.Contains(compose, want) {
		t.Errorf("ops/controller/compose.yaml default image tag does not match v%s", Version)
	}
}
