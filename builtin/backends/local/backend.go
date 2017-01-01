package local

import (
	"context"
	"fmt"
	"os"

	"github.com/hashicorp/errwrap"
	"github.com/hashicorp/go-multierror"
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

	// Input will ask for necessary input prior to performing any operations.
	//
	// Validation will perform validation prior to running an operation. The
	// variable naming doesn't match the style of others since we have a func
	// Validate.
	Input      bool
	Validation bool

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
func (b *Local) Operation(ctx context.Context, op *backend.Operation) (*backend.RunningOperation, error) {
	// Build our running operation
	runningCtx, runningCtxCancel := context.WithCancel(context.Background())
	runningOp := &backend.RunningOperation{Context: runningCtx}

	// Do it
	go func() {
		defer runningCtxCancel()
		b.runOperation(ctx, op, runningOp)
	}()

	// Return
	return runningOp, nil
}

func (b *Local) runOperation(
	ctx context.Context,
	op *backend.Operation,
	runningOp *backend.RunningOperation) {
	// Check if our state exists if we're performing a refresh operation. We
	// only do this if we're managing state with this backend.
	if b.Backend == nil {
		if _, err := os.Stat(b.StatePath); err != nil {
			if os.IsNotExist(err) {
				runningOp.Err = fmt.Errorf(
					"The Terraform state file for your infrastructure does not\n"+
						"exist. The 'refresh' command only works and only makes sense\n"+
						"when there is existing state that Terraform is managing. Please\n"+
						"double-check the value given below and try again. If you\n"+
						"haven't created infrastructure with Terraform yet, use the\n"+
						"'terraform apply' command.\n\n"+
						"Path: %s",
					b.StatePath)
				return
			}

			runningOp.Err = fmt.Errorf(
				"There was an error reading the Terraform state that is needed\n"+
					"for refreshing. The path and error are shown below.\n\n"+
					"Path: %s\n\nError: %s",
				b.StatePath)
			return
		}
	}

	// Initialize our context options
	var opts terraform.ContextOpts
	if v := b.ContextOpts; v != nil {
		opts = *v
	}

	// Copy set options from the operation
	opts.Destroy = op.Destroy
	opts.Module = op.Module
	opts.Targets = op.Targets
	opts.UIInput = op.UIIn
	if op.Variables != nil {
		opts.Variables = op.Variables
	}

	// Load our state
	state, err := b.State()
	if err != nil {
		runningOp.Err = errwrap.Wrapf("Error loading state: {{err}}", err)
		return
	}
	if err := state.RefreshState(); err != nil {
		runningOp.Err = errwrap.Wrapf("Error loading state: {{err}}", err)
		return
	}
	opts.State = state.State()

	// Set the operation state to our initial state for now
	runningOp.State = opts.State

	// Build the context
	tfCtx, err := terraform.NewContext(&opts)
	if err != nil {
		runningOp.Err = err
		return
	}

	// If input asking is enabled, then do that
	if b.Input {
		mode := terraform.InputModeProvider
		mode |= terraform.InputModeVar
		mode |= terraform.InputModeVarUnset

		if err := tfCtx.Input(mode); err != nil {
			runningOp.Err = errwrap.Wrapf("Error asking for user input: {{err}}", err)
			return
		}
	}

	// If validation is enabled, validate
	if b.Validation {
		// We ignore warnings here on purpose. We expect users to be listening
		// to the terraform.Hook called after a validation.
		_, es := tfCtx.Validate()
		if len(es) > 0 {
			runningOp.Err = multierror.Append(nil, es...)
			return
		}
	}

	// Perform operation and write the resulting state to the running op
	newState, err := tfCtx.Refresh()
	runningOp.State = newState
	if err != nil {
		runningOp.Err = errwrap.Wrapf("Error refreshing state: {{err}}", err)
		return
	}

	// Write and persist the state
	if err := state.WriteState(newState); err != nil {
		runningOp.Err = errwrap.Wrapf("Error writing state: {{err}}", err)
		return
	}
	if err := state.PersistState(); err != nil {
		runningOp.Err = errwrap.Wrapf("Error saving state: {{err}}", err)
		return
	}
}
