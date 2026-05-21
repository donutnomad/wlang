//go:build go2wlangexample

package examples

import (
	temporal "temporal"
	workflow "workflow"
)

type FailureReason struct {
	FailedStep string
	Message    string
	Type       string
}

type OrderInput struct {
	OrderID string
}

type ReserveResult struct {
	ID string
}

type MarkFailedInput struct {
	OrderID    string
	ReserveID  string
	FailedBy   string
	Reason     string
	ReasonType string
}

type Runner interface {
	Pay(workflow.Context, string) error
}

func OrderWorkflow(ctx workflow.Context, runner Runner, input OrderInput) (err error) {
	var compensations []func(workflow.Context, FailureReason) error
	failedStep := ""
	reserve := ReserveResult{ID: ""}

	defer func() {
		if err == nil {
			return
		}

		reason := BuildFailureReason(failedStep, err)
		for i := len(compensations) - 1; i >= 0; i-- {
			compErr := compensations[i](ctx, reason)
			if compErr != nil {
				err = temporal.NewApplicationError(
					"workflow failed and compensation failed",
					"CompensationFailed",
					compErr,
				)
				return
			}
		}
	}()

	failedStep = "step1_reserve"
	reserve = workflow.Reserve(ctx, input.OrderID)
	compensations = append(compensations, func(ctx workflow.Context, reason FailureReason) error {
		return workflow.MarkReserveFailed(ctx, MarkFailedInput{
			OrderID:    input.OrderID,
			ReserveID:  reserve.ID,
			FailedBy:   reason.FailedStep,
			Reason:     reason.Message,
			ReasonType: reason.Type,
		})
	})

	failedStep = "step10_pay"
	err = runner.Pay(ctx, input.OrderID)
	if err != nil {
		return err
	}

	return nil
}
