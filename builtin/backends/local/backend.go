package local

import (
	"context"
	"fmt"
	"sync"

	"github.com/hashicorp/errwrap"
	"github.com/hashicorp/go-multierror"
	"github.com/hashicorp/terraform/backend"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/hashicorp/terraform/state"
	"github.com/hashicorp/terraform/terraform"
	"github.com/mitchellh/cli"
	"github.com/mitchellh/colorstring"
)

// Local is an implementation of EnhancedBackend that performs all operations
// locally. This is the "default" backend and implements normal Terraform
// behavior as it is well known.
type Local struct {
	// CLI and Colorize control the CLI output. If CLI is nil then no CLI
	// output will be done. If CLIColor is nil then no coloring will be done.
	CLI      cli.Ui
	CLIColor *colorstring.Colorize

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
	opLock sync.Mutex
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
	// Determine the function to call for our operation
	var f func(context.Context, *backend.Operation, *backend.RunningOperation)
	switch op.Type {
	case backend.OperationTypeRefresh:
		f = b.opRefresh
	case backend.OperationTypePlan:
		f = b.opPlan
	default:
		return nil, fmt.Errorf(
			"Unsupported operation type: %s\n\n" +
				"This is a bug in Terraform and should be reported. The local backend\n" +
				"is built-in to Terraform and should always support all operations.")
	}

	// Lock
	b.opLock.Lock()

	// Build our running operation
	runningCtx, runningCtxCancel := context.WithCancel(context.Background())
	runningOp := &backend.RunningOperation{Context: runningCtx}

	// Do it
	go func() {
		defer b.opLock.Unlock()
		defer runningCtxCancel()
		f(ctx, op, runningOp)
	}()

	// Return
	return runningOp, nil
}

// Context returns the terraform.Context struct for the given operation.
//
// This will also initialize the context by asking for input and performing
// validation, if the backend is configured to do so.
func (b *Local) Context(op *backend.Operation) (*terraform.Context, state.State, error) {
	var err error

	// Setup our state. If we have a plan given, the state is directly
	// from the plan itself.
	var s state.State
	if op.Plan != nil {
		s = &state.InmemState{}
		s.WriteState(op.Plan.State)
	}

	if s == nil {
		s, err = b.State()
		if err != nil {
			return nil, nil, errwrap.Wrapf("Error loading state: {{err}}", err)
		}
		if err := s.RefreshState(); err != nil {
			return nil, nil, errwrap.Wrapf("Error loading state: {{err}}", err)
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
	opts.State = s.State()

	// Build the context
	var tfCtx *terraform.Context
	if op.Plan != nil {
		tfCtx, err = op.Plan.Context(&opts)
	} else {
		tfCtx, err = terraform.NewContext(&opts)
	}
	if err != nil {
		return nil, nil, err
	}

	// If input asking is enabled, then do that
	if b.Input {
		mode := terraform.InputModeProvider
		mode |= terraform.InputModeVar
		mode |= terraform.InputModeVarUnset

		if err := tfCtx.Input(mode); err != nil {
			return nil, nil, errwrap.Wrapf("Error asking for user input: {{err}}", err)
		}
	}

	// If validation is enabled, validate
	if b.Validation {
		// We ignore warnings here on purpose. We expect users to be listening
		// to the terraform.Hook called after a validation.
		_, es := tfCtx.Validate()
		if len(es) > 0 {
			return nil, nil, multierror.Append(nil, es...)
		}
	}

	return tfCtx, s, nil
}

// Colorize returns the Colorize structure that can be used for colorizing
// output. This is gauranteed to always return a non-nil value and so is useful
// as a helper to wrap any potentially colored strings.
func (b *Local) Colorize() *colorstring.Colorize {
	if b.CLIColor != nil {
		return b.CLIColor
	}

	return &colorstring.Colorize{
		Colors:  colorstring.DefaultColors,
		Disable: true,
	}
}
