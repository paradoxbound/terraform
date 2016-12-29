package local

import (
	"testing"

	"github.com/hashicorp/terraform/backend"
)

func TestLocal_impl(t *testing.T) {
	var _ backend.Enhanced = new(Local)
}
