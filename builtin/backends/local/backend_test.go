package local

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform/backend"
	"github.com/hashicorp/terraform/config/module"
	"github.com/hashicorp/terraform/terraform"
)

func TestLocal_impl(t *testing.T) {
	var _ backend.Enhanced = new(Local)
}

func TestLocal_refresh(t *testing.T) {
	b := TestLocal(t)
	p := TestLocalProvider(t, b, "test")
	terraform.TestStateFile(t, b.StatePath, testRefreshState())

	p.RefreshFn = nil
	p.RefreshReturn = &terraform.InstanceState{ID: "yes"}

	mod, modCleanup := module.TestTree(t, "./test-fixtures/refresh")
	defer modCleanup()

	op := &backend.Operation{
		Type:   backend.OperationTypeRefresh,
		Module: mod,
	}
	if err := b.Operation(op); err != nil {
		t.Fatalf("bad: %s", err)
	}

	if !p.RefreshCalled {
		t.Fatal("refresh should be called")
	}

	checkState(t, b.StateOutPath, `
test_instance.foo:
  ID = yes
	`)
}

func TestLocal_refreshInput(t *testing.T) {
	b := TestLocal(t)
	p := TestLocalProvider(t, b, "test")
	terraform.TestStateFile(t, b.StatePath, testRefreshState())

	p.ConfigureFn = func(c *terraform.ResourceConfig) error {
		if v, ok := c.Get("value"); !ok || v != "bar" {
			return fmt.Errorf("no value set")
		}

		return nil
	}

	p.RefreshFn = nil
	p.RefreshReturn = &terraform.InstanceState{ID: "yes"}

	mod, modCleanup := module.TestTree(t, "./test-fixtures/refresh-var-unset")
	defer modCleanup()

	// Enable input asking since it is normally disabled by default
	b.Input = true
	b.ContextOpts.UIInput = &terraform.MockUIInput{InputReturnString: "bar"}

	op := &backend.Operation{
		Type:   backend.OperationTypeRefresh,
		Module: mod,
	}
	if err := b.Operation(op); err != nil {
		t.Fatalf("bad: %s", err)
	}

	if !p.RefreshCalled {
		t.Fatal("refresh should be called")
	}

	checkState(t, b.StateOutPath, `
test_instance.foo:
  ID = yes
	`)
}

func TestLocal_refreshValidate(t *testing.T) {
	b := TestLocal(t)
	p := TestLocalProvider(t, b, "test")
	terraform.TestStateFile(t, b.StatePath, testRefreshState())

	p.RefreshFn = nil
	p.RefreshReturn = &terraform.InstanceState{ID: "yes"}

	mod, modCleanup := module.TestTree(t, "./test-fixtures/refresh")
	defer modCleanup()

	// Enable validation
	b.Validation = true

	op := &backend.Operation{
		Type:   backend.OperationTypeRefresh,
		Module: mod,
	}
	if err := b.Operation(op); err != nil {
		t.Fatalf("bad: %s", err)
	}

	if !p.ValidateCalled {
		t.Fatal("validate should be called")
	}

	checkState(t, b.StateOutPath, `
test_instance.foo:
  ID = yes
	`)
}

// testRefreshState is just a common state that we use for testing refresh.
func testRefreshState() *terraform.State {
	return &terraform.State{
		Version: 2,
		Modules: []*terraform.ModuleState{
			&terraform.ModuleState{
				Path: []string{"root"},
				Resources: map[string]*terraform.ResourceState{
					"test_instance.foo": &terraform.ResourceState{
						Type: "test_instance",
						Primary: &terraform.InstanceState{
							ID: "bar",
						},
					},
				},
				Outputs: map[string]*terraform.OutputState{},
			},
		},
	}
}

func checkState(t *testing.T, path, expected string) {
	// Read the state
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	state, err := terraform.ReadState(f)
	f.Close()
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	actual := strings.TrimSpace(state.String())
	expected = strings.TrimSpace(expected)
	if actual != expected {
		t.Fatalf("state does not match! actual:\n%s\n\nexpected:\n%s", actual, expected)
	}
}
