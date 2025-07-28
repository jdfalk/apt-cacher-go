package security

import (
	"testing"
)

func TestACLPermissions(t *testing.T) {
	// Example test case for ACL permissions
	permissions := map[string]bool{
		"read":  true,
		"write": false,
	}

	if permissions["read"] != true {
		t.Errorf("Expected read permission to be true")
	}

	if permissions["write"] != false {
		t.Errorf("Expected write permission to be false")
	}
}
