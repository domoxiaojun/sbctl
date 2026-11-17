package sbctl

import (
	"sbctl/contracts"
	"strings"
	"testing"
)

// Release metadata follows the binary version. Deployment defaults to latest,
// while SBCTL_IMAGE can still select a specific release or digest.
func TestVersionIsConsistentAcrossReleaseFiles(t *testing.T) {
	if got := str(mapping(object(contracts.OpenAPI)["info"])["version"]); got != Version {
		t.Errorf("contracts/openapi.json info.version is %s, binary is %s", got, Version)
	}
	compose := string(read("../../ops/controller/compose.yaml"))
	if !strings.Contains(compose, "${SBCTL_IMAGE:-ghcr.io/domoxiaojun/sbctl:latest}") {
		t.Error("ops/controller/compose.yaml must default to latest and preserve SBCTL_IMAGE overrides")
	}
}
