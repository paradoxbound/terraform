package command

import (
	"fmt"
	"os"
	"strconv"

	"github.com/hashicorp/terraform/backend"
	"github.com/hashicorp/terraform/builtin/backends/local"
	"github.com/hashicorp/terraform/config/module"
	"github.com/hashicorp/terraform/terraform"
)

// NOTE: This is a temporary file during the backend branch. This will be
// merged back into meta.go when the work here is done. This just helps keep
// track of what we're adding.

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

	// TODO: "legacy" remote state

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

// Input returns whether or not input asking is enabled.
func (m *Meta) Input() bool {
	if test || !m.input {
		return false
	}

	if envVar := os.Getenv(InputModeEnvVar); envVar != "" {
		if v, err := strconv.ParseBool(envVar); err == nil && !v {
			return false
		}
	}

	return true
}

// Module loads the module tree for the given root path.
//
// It expects the modules to already be downloaded. This will never
// download any modules.
func (m *Meta) Module(path string) (*module.Tree, error) {
	mod, err := module.NewTreeModule("", path)
	if err != nil {
		return nil, err
	}

	err = mod.Load(m.moduleStorage(m.DataDir()), module.GetModeNone)
	if err != nil {
		return nil, fmt.Errorf("Error loading modules: %s", err)
	}

	return mod, nil
}

// Plan returns the plan for the given path.
//
// This only has an effect if the path itself looks like a plan.
// If error is nil and the plan is nil, then the path didn't look like
// a plan.
//
// Error will be non-nil if path looks like a plan and loading the plan
// failed.
func (m *Meta) Plan(path string) (*terraform.Plan, error) {
	// Open the path no matter if its a directory or file
	f, err := os.Open(path)
	defer f.Close()
	if err != nil {
		return nil, fmt.Errorf(
			"Failed to load Terraform configuration or plan: %s", err)
	}

	// Stat it so we can check if its a directory
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf(
			"Failed to load Terraform configuration or plan: %s", err)
	}

	// If this path is a directory, then it can't be a plan. Not an error.
	if fi.IsDir() {
		return nil, nil
	}

	// Read the plan
	p, err := terraform.ReadPlan(f)
	if err != nil {
		return nil, err
	}

	// We do a validation here that seems odd but if any plan is given,
	// we must not have set any extra variables. The plan itself contains
	// the variables and those aren't overwritten.
	if len(m.variables) > 0 {
		return nil, fmt.Errorf(
			"You can't set variables with the '-var' or '-var-file' flag\n" +
				"when you're applying a plan file. The variables used when\n" +
				"the plan was created will be used. If you wish to use different\n" +
				"variable values, create a new plan file.")
	}

	return p, nil
}

// BackendOpts are the options used to initialize a backend.Backend.
type BackendOpts struct {
	// Nothing at the moment, but experience has shown that something
	// will likely be useful here in the future. To avoid API changes,
	// we'll set this up now.
}
