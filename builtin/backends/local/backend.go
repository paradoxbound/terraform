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

func (b *Local) Operation(*backend.Operation) error {
	return nil
}
