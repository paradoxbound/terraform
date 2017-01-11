package command

// This file contains all the Backend-related function calls on Meta,
// exported and private.

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/hashicorp/errwrap"
	"github.com/hashicorp/terraform/backend"
	"github.com/hashicorp/terraform/builtin/backends/local"
	"github.com/hashicorp/terraform/config"
	"github.com/hashicorp/terraform/state"
	"github.com/hashicorp/terraform/terraform"
)

// Backend initializes and returns the backend for this CLI session.
//
// The backend is used to perform the actual Terraform operations. This
// abstraction enables easily sliding in new Terraform behavior such as
// remote state storage, remote operations, etc. while allowing the CLI
// to remain mostly identical.
//
// This will initialize a new backend for each call, which can carry some
// overhead with it. Please reuse the returned value for optimal behavior.
func (m *Meta) Backend(opts *BackendOpts) (backend.Enhanced, error) {
	// If no opts are set, then initialize
	if opts == nil {
		opts = &BackendOpts{}
	}

	// Setup the local state paths
	statePath := m.statePath
	stateOutPath := m.stateOutPath
	backupPath := m.backupPath
	if statePath == "" {
		statePath = DefaultStateFilename
	}
	if stateOutPath == "" {
		stateOutPath = statePath
	}
	if backupPath == "" {
		backupPath = stateOutPath + DefaultBackupExtension
	}
	if backupPath == "-" {
		// The local backend expects an empty string for not taking backups.
		backupPath = ""
	}

	// Get the local backend configuration.
	config, err := m.backendConfig(opts)
	if err != nil {
		return nil, fmt.Errorf("Error loading backend config: %s", err)
	}

	// Get the path to where we store a local cache of backend configuration
	// if we're using a remote backend. This may not yet exist which means
	// we haven't used a non-local backend before. That is okay.
	dataStatePath := filepath.Join(m.DataDir(), DefaultStateFilename)
	dataStateMgr := &state.LocalState{Path: dataStatePath}
	if err := dataStateMgr.RefreshState(); err != nil {
		return nil, fmt.Errorf("Error loading state: %s", err)
	}

	// Get the final backend configuration to use. This will handle any
	// conflicts (legacy remote state, new config, config changes, etc.)
	// and only return the final configuration to use.
	b, err := m.backendFromConfig(config, dataStateMgr)
	if err != nil {
		return nil, err
	}

	log.Printf("[INFO] command: backend initialized: %T", b)

	// If the result of loading the backend is an enhanced backend,
	// then return that as-is. This works even if b == nil (it will be !ok).
	if enhanced, ok := b.(backend.Enhanced); ok {
		return enhanced, nil
	}

	// We either have a non-enhanced backend or no backend configured at
	// all. In either case, we use local as our enhanced backend and the
	// non-enhanced (if any) as the state backend.

	log.Printf("[INFO] command: backend %T is not enhanced, wrapping in local", b)

	// Build the local backend
	return &local.Local{
		CLI:             m.Ui,
		CLIColor:        m.Colorize(),
		StatePath:       statePath,
		StateOutPath:    stateOutPath,
		StateBackupPath: backupPath,
		ContextOpts:     m.contextOpts(),
		Input:           m.Input(),
		Validation:      true,
		Backend:         b,
	}, nil
}

// Operation initializes a new backend.Operation struct.
//
// This prepares the operation. After calling this, the caller is expected
// to modify fields of the operation such as Sequence to specify what will
// be called.
func (m *Meta) Operation() *backend.Operation {
	return &backend.Operation{
		Targets: m.targets,
		UIIn:    m.UIInput(),
	}
}

// backendConfig returns the local configuration for the backend
func (m *Meta) backendConfig(opts *BackendOpts) (*config.Backend, error) {
	// If no explicit path was given then it is okay for there to be
	// no backend configuration found.
	emptyOk := opts.ConfigPath == "" && m.backendConfigPath == ""

	// Determine the path to the configuration. If the "-backend-config"
	// flag was set, that always takes priority.
	path := opts.ConfigPath
	if m.backendConfigPath != "" {
		path = m.backendConfigPath
	}

	// If we had no path set, it is an error. We can't initialize unset
	if path == "" {
		path = "."
	}

	// Expand the path
	if !filepath.IsAbs(path) {
		var err error
		path, err = filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf(
				"Error expanding path to backend config %q: %s", path, err)
		}
	}

	log.Printf("[DEBUG] command: loading backend config file: %s", path)

	// We first need to determine if we're loading a file or a directory.
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) && emptyOk {
			log.Printf(
				"[INFO] command: backend config not found, returning nil: %s",
				path)
			return nil, nil
		}

		return nil, err
	}

	var f func(string) (*config.Config, error) = config.LoadFile
	if fi.IsDir() {
		f = config.LoadDir
	}

	// Load the configuration
	c, err := f(path)
	if err != nil {
		// Check for the error where we have no config files and return nil
		// as the configuration type.
		if emptyOk && errwrap.ContainsType(err, new(config.ErrNoConfigsFound)) {
			log.Printf(
				"[INFO] command: backend config not found, returning nil: %s",
				path)
			return nil, nil
		}

		return nil, err
	}

	// If there is no Terraform configuration block, no backend config
	if c.Terraform == nil {
		return nil, nil
	}

	// Return the configuration which may or may not be set
	return c.Terraform.Backend, nil
}

// backendFromConfig returns the initialized (not configured) backend
// directly from the config/state..
//
// This function handles any edge cases around backend config loading. For
// example: legacy remote state, new config changes, backend type changes,
// etc.
//
// This function may query the user for input unless input is disabled, in
// which case this function will error.
func (m *Meta) backendFromConfig(
	c *config.Backend, sMgr state.State) (backend.Backend, error) {
	// Load the state, it must be non-nil for the tests below but can be empty
	s := sMgr.State()
	if s == nil {
		log.Printf("[DEBUG] command: no data state file found for backend config")
		s = terraform.NewState()
	}

	switch {
	// No configuration set at all. Pure local state.
	case c == nil && s.Remote.Empty() && s.Backend.Empty():
		return nil, nil

	// We're unsetting a backend (moving from backend => local)
	case c == nil && s.Remote.Empty() && !s.Backend.Empty():
		panic("unhandled")

	// We have a legacy remote state configuration but no new backend config
	case c == nil && !s.Remote.Empty() && s.Backend.Empty():
		panic("unhandled")

	// We have a legacy remote state configuration simultaneously with a
	// saved backend configuration while at the same time disabling backend
	// configuration.
	//
	// This is a naturally impossible case: Terraform will never put you
	// in this state, though it is theoretically possible through manual edits
	case c == nil && !s.Remote.Empty() && !s.Backend.Empty():
		panic("unhandled")

	// Configuring a backend for the first time.
	case c != nil && s.Remote.Empty() && s.Backend.Empty():
		fallthrough

	// Potentially changing a backend configuration
	case c != nil && s.Remote.Empty() && !s.Backend.Empty():
		// If our configuration is the same, then we're just initializing
		// a previously configured remote backend.
		if !s.Backend.Empty() && s.Backend.Hash == c.Hash() {
			panic("unhandled")
		}

		panic("unhandled")

	// Configuring a backend for the first time while having legacy
	// remote state. This is very possible if a Terraform user configures
	// a backend prior to ever running Terraform on an old state.
	case c != nil && !s.Remote.Empty() && s.Backend.Empty():
		panic("unhandled")

	// Configuring a backend with both a legacy remote state set
	// and a pre-existing backend saved.
	case c != nil && !s.Remote.Empty() && !s.Backend.Empty():
		// If the hashes are the same, we have a legacy remote state with
		// an unchanged stored backend state.
		if s.Backend.Hash == c.Hash() {
			panic("unhandled")
		}

		// We have change in all three
		panic("unhandled")
	default:
		// This should be impossible since all state possibilties are
		// tested above, but we need a default case anyways and we should
		// protect against the scenario where a case is somehow removed.
		return nil, fmt.Errorf(
			"Unhandled backend configuration state. This is a bug. Please\n"+
				"report this error with the following information.\n\n"+
				"Config Nil: %v\n"+
				"Saved Backend Empty: %v\n"+
				"Legacy Remote Empty: %v\n",
			c == nil, s.Backend.Empty(), s.Remote.Empty())
	}
}

// BackendOpts are the options used to initialize a backend.Backend.
type BackendOpts struct {
	// ConfigPath is a path to a file or directory containing the backend
	// configuration. If "-backend-config" is set and processed on Meta,
	// that will take priority.
	ConfigPath string
}
