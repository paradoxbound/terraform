package local

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/hashicorp/errwrap"
	"github.com/hashicorp/terraform/backend"
	"github.com/hashicorp/terraform/command/format"
	"github.com/hashicorp/terraform/terraform"
)

func (b *Local) opPlan(
	ctx context.Context,
	op *backend.Operation,
	runningOp *backend.RunningOperation) {
	log.Printf("[INFO] backend/local: starting Plan operation")

	// Get our state
	state, err := b.State()
	if err != nil {
		runningOp.Err = errwrap.Wrapf("Error loading state: {{err}}", err)
		return
	}
	if err := state.RefreshState(); err != nil {
		runningOp.Err = errwrap.Wrapf("Error loading state: {{err}}", err)
		return
	}
	runningOp.State = state.State()

	// Setup our count hook that keeps track of resource changes
	countHook := new(CountHook)
	if b.ContextOpts == nil {
		b.ContextOpts = new(terraform.ContextOpts)
	}
	old := b.ContextOpts.Hooks
	defer func() { b.ContextOpts.Hooks = old }()
	b.ContextOpts.Hooks = append(b.ContextOpts.Hooks, countHook)

	// Get our context
	tfCtx, err := b.Context(op, state)
	if err != nil {
		runningOp.Err = err
		return
	}

	// If we're refreshing before plan, perform that
	if op.PlanRefresh {
		log.Printf("[INFO] backend/local: plan calling Refresh")
		_, err := tfCtx.Refresh()
		if err != nil {
			runningOp.Err = errwrap.Wrapf("Error refreshing state: {{err}}", err)
			return
		}
	}

	// Perform the plan
	log.Printf("[INFO] backend/local: plan calling Plan")
	plan, err := tfCtx.Plan()
	if err != nil {
		runningOp.Err = errwrap.Wrapf("Error running plan: {{err}}", err)
		return
	}

	// Record state
	runningOp.PlanEmpty = plan.Diff.Empty()

	// Save the plan to disk
	if path := op.PlanOutPath; path != "" {
		log.Printf("[INFO] backend/local: writing plan output to: %s", path)
		f, err := os.Create(path)
		if err == nil {
			err = terraform.WritePlan(plan, f)
		}
		f.Close()
		if err != nil {
			runningOp.Err = fmt.Errorf("Error writing plan file: %s", err)
			return
		}
	}

	// Perform some output tasks if we have a CLI to output to.
	if b.CLI != nil {
		if plan.Diff.Empty() {
			b.CLI.Output(
				"No changes. Infrastructure is up-to-date. This means that Terraform\n" +
					"could not detect any differences between your configuration and\n" +
					"the real physical resources that exist. As a result, Terraform\n" +
					"doesn't need to do anything.")
		}

		if path := op.PlanOutPath; path == "" {
			b.CLI.Output(strings.TrimSpace(planHeaderNoOutput) + "\n")
		} else {
			b.CLI.Output(fmt.Sprintf(
				strings.TrimSpace(planHeaderYesOutput)+"\n",
				path))
		}

		b.CLI.Output(format.Plan(&format.PlanOpts{
			Plan:        plan,
			Color:       b.Colorize(),
			ModuleDepth: -1,
		}))

		b.CLI.Output(b.Colorize().Color(fmt.Sprintf(
			"[reset][bold]Plan:[reset] "+
				"%d to add, %d to change, %d to destroy.",
			countHook.ToAdd+countHook.ToRemoveAndAdd,
			countHook.ToChange,
			countHook.ToRemove+countHook.ToRemoveAndAdd)))
	}
}

const planHeaderNoOutput = `
The Terraform execution plan has been generated and is shown below.
Resources are shown in alphabetical order for quick scanning. Green resources
will be created (or destroyed and then created if an existing resource
exists), yellow resources are being changed in-place, and red resources
will be destroyed. Cyan entries are data sources to be read.

Note: You didn't specify an "-out" parameter to save this plan, so when
"apply" is called, Terraform can't guarantee this is what will execute.
`

const planHeaderYesOutput = `
The Terraform execution plan has been generated and is shown below.
Resources are shown in alphabetical order for quick scanning. Green resources
will be created (or destroyed and then created if an existing resource
exists), yellow resources are being changed in-place, and red resources
will be destroyed. Cyan entries are data sources to be read.

Your plan was also saved to the path below. Call the "apply" subcommand
with this plan file and Terraform will exactly execute this execution
plan.

Path: %s
`
