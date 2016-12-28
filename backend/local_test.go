package backend

import (
	"testing"
)

func TestLocal_impl(t *testing.T) {
	var _ Enhanced = new(Local)
}
