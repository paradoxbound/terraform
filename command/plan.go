package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform/backend"
)

// PlanCommand is a Command implementation that compares a Terraform
// configuration to an actual infrastructure and shows the differences.
type PlanCommand struct {
	Meta
}

func (c *PlanCommand) Run(args []string) int {
	var destroy, refresh, detailed bool
	var outPath string
	var moduleDepth int

	args = c.Meta.process(args, true)

	cmdFlags := c.Meta.flagSet("plan")
	cmdFlags.BoolVar(&destroy, "destroy", false, "destroy")
	cmdFlags.BoolVar(&refresh, "refresh", true, "refresh")
	c.addModuleDepthFlag(cmdFlags, &moduleDepth)
	cmdFlags.StringVar(&outPath, "out", "", "path")
	cmdFlags.IntVar(
		&c.Meta.parallelism, "parallelism", DefaultParallelism, "parallelism")
	cmdFlags.StringVar(&c.Meta.statePath, "state", DefaultStateFilename, "path")
	cmdFlags.BoolVar(&detailed, "detailed-exitcode", false, "detailed-exitcode")
	cmdFlags.Usage = func() { c.Ui.Error(c.Help()) }
	if err := cmdFlags.Parse(args); err != nil {
		return 1
	}

	configPath, err := ModulePath(cmdFlags.Args())
	if err != nil {
		c.Ui.Error(err.Error())
		return 1
	}

	// Load the module
	mod, err := c.Module(configPath)
	if err != nil {
		c.Ui.Error(fmt.Sprintf("Failed to load root config module: %s", err))
		return 1
	}

	// Load the backend
	b, err := c.Backend(nil)
	if err != nil {
		c.Ui.Error(fmt.Sprintf("Failed to load backend: %s", err))
		return 1
	}

	// Build the operation
	opReq := c.Operation()
	opReq.Destroy = destroy
	opReq.Module = mod
	opReq.PlanRefresh = refresh
	opReq.PlanOutPath = outPath
	opReq.Type = backend.OperationTypePlan

	// Perform the operation
	op, err := b.Operation(context.Background(), opReq)
	if err != nil {
		c.Ui.Error(fmt.Sprintf("Error starting operation: %s", err))
		return 1
	}

	// Wait for the operation to complete
	<-op.Done()
	if err := op.Err; err != nil {
		c.Ui.Error(err.Error())
		return 1
	}

	/*
		if planned {
			c.Ui.Output(c.Colorize().Color(
				"[reset][bold][yellow]" +
					"The plan command received a saved plan file as input. This command\n" +
					"will output the saved plan. This will not modify the already-existing\n" +
					"plan. If you wish to generate a new plan, please pass in a configuration\n" +
					"directory as an argument.\n\n"))

			// Disable refreshing no matter what since we only want to show the plan
			refresh = false
		}
	*/

	/*
		err = terraform.SetDebugInfo(DefaultDataDir)
		if err != nil {
			c.Ui.Error(err.Error())
			return 1
		}
	*/

	if detailed && !op.PlanEmpty {
		return 2
	}

	return 0
}

func (c *PlanCommand) Help() string {
	helpText := `
Usage: terraform plan [options] [DIR-OR-PLAN]

  Generates an execution plan for Terraform.

  This execution plan can be reviewed prior to running apply to get a
  sense for what Terraform will do. Optionally, the plan can be saved to
  a Terraform plan file, and apply can take this plan file to execute
  this plan exactly.

  If a saved plan is passed as an argument, this command will output
  the saved plan contents. It will not modify the given plan.

Options:

  -destroy            If set, a plan will be generated to destroy all resources
                      managed by the given configuration and state.

  -detailed-exitcode  Return detailed exit codes when the command exits. This
                      will change the meaning of exit codes to:
                      0 - Succeeded, diff is empty (no changes)
                      1 - Errored
                      2 - Succeeded, there is a diff

  -input=true         Ask for input for variables if not directly set.

  -module-depth=n     Specifies the depth of modules to show in the output.
                      This does not affect the plan itself, only the output
                      shown. By default, this is -1, which will expand all.

  -no-color           If specified, output won't contain any color.

  -out=path           Write a plan file to the given path. This can be used as
                      input to the "apply" command.

  -parallelism=n      Limit the number of concurrent operations. Defaults to 10.

  -refresh=true       Update state prior to checking for differences.

  -state=statefile    Path to a Terraform state file to use to look
                      up Terraform-managed resources. By default it will
                      use the state "terraform.tfstate" if it exists.

  -target=resource    Resource to target. Operation will be limited to this
                      resource and its dependencies. This flag can be used
                      multiple times.

  -var 'foo=bar'      Set a variable in the Terraform configuration. This
                      flag can be set multiple times.

  -var-file=foo       Set variables in the Terraform configuration from
                      a file. If "terraform.tfvars" is present, it will be
                      automatically loaded if this flag is not specified.
`
	return strings.TrimSpace(helpText)
}

func (c *PlanCommand) Synopsis() string {
	return "Generate and show an execution plan"
}
