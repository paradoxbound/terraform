package local

import (
	"github.com/hashicorp/errwrap"
	"github.com/hashicorp/terraform/backend"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/hashicorp/terraform/state"
	"github.com/hashicorp/terraform/terraform"
)

// Local is an implementation of EnhancedBackend that performs all operations
// locally. This is the "default" backend and implements normal Terraform
// behavior as it is well known.
type Local struct {
	// StatePath is the local path where state is read from.
	//
	// StateOutPath is the local path where the state will be written.
	// If this is empty, it will default to StatePath.
	//
	// StateBackupPath is the local path where a backup file will be written.
	// If this is empty, no backup will be taken.
	StatePath       string
	StateOutPath    string
	StateBackupPath string

	// ContextOpts are the base context options to set when initializing a
	// Terraform context. Many of these will be overridden or merged by
	// Operation. See Operation for more details.
	ContextOpts *terraform.ContextOpts

	// Input, if true, will ask for necessary input prior to performing
	// any operations.
	Input bool

	// Backend, if non-nil, will use this backend for non-enhanced behavior.
	// This allows local behavior with remote state storage. It is a way to
	// "upgrade" a non-enhanced backend to an enhanced backend with typical
	// behavior.
	//
	// If this is nil, local performs normal state loading and storage.
	Backend backend.Backend

	schema *schema.Backend
}

func (b *Local) Validate(c *terraform.ResourceConfig) ([]string, []error) {
	f := b.schema.Validate
	if b.Backend != nil {
		f = b.Backend.Validate
	}

	return f(c)
}

func (b *Local) Configure(c *terraform.ResourceConfig) error {
	f := b.schema.Configure
	if b.Backend != nil {
		f = b.Backend.Configure
	}

	return f(c)
}

func (b *Local) State() (state.State, error) {
	// If we have a backend handling state, defer to that.
	if b.Backend != nil {
		return b.Backend.State()
	}

	// Otherwise, we need to load the state.
	var s state.State = &state.LocalState{
		Path:    b.StatePath,
		PathOut: b.StateOutPath,
	}

	// Load the state as a sanity check
	if err := s.RefreshState(); err != nil {
		return nil, errwrap.Wrapf("Error reading local state: {{err}}", err)
	}

	// If we are backing up the state, wrap it
	if path := b.StateBackupPath; path != "" {
		s = &state.BackupState{
			Real: s,
			Path: path,
		}
	}

	return s, nil
}

// Operation implements backend.Enhanced
//
// This will initialize an in-memory terraform.Context to perform the
// operation within this process.
//
// The given operation parameter will be merged with the ContextOpts on
// the structure with the following rules. If a rule isn't specified and the
// name conflicts, assume that the field is overwritten if set.
func (b *Local) Operation(op *backend.Operation) error {
	// Initialize our context options
	var opts terraform.ContextOpts
	if v := b.ContextOpts; v != nil {
		opts = *v
	}

	// Copy set options from the operation
	opts.Destroy = op.Destroy
	opts.Module = op.Module
	opts.Targets = op.Targets
	opts.Variables = op.Variables

	// Load our state
	state, err := b.State()
	if err != nil {
		return errwrap.Wrapf("Error loading state: {{err}}", err)
	}
	if err := state.RefreshState(); err != nil {
		return errwrap.Wrapf("Error loading state: {{err}}", err)
	}
	opts.State = state.State()

	// Build the context
	ctx, err := terraform.NewContext(&opts)
	if err != nil {
		return err
	}

	// If input asking is enabled, then do that
	if b.Input {
		mode := terraform.InputModeProvider
		mode |= terraform.InputModeVar
		mode |= terraform.InputModeVarUnset

		if err := ctx.Input(mode); err != nil {
			return errwrap.Wrapf("Error asking for user input: {{err}}", err)
		}
	}

	// TODO: validate context

	// Perform operation
	newState, err := ctx.Refresh()
	if err != nil {
		return errwrap.Wrapf("Error refreshing state: {{err}}", err)
	}

	// Write and persist the state
	if err := state.WriteState(newState); err != nil {
		return errwrap.Wrapf("Error writing state: {{err}}", err)
	}
	if err := state.PersistState(); err != nil {
		return errwrap.Wrapf("Error saving state: {{err}}", err)
	}

	return nil
}
