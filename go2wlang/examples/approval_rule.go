//go:build go2wlangexample

package examples

import (
	"audit"
	"notify"
	policy "policykit"
)

type User struct {
	Name   string
	Age    int64
	Active bool
}

type Scorer interface {
	Score(User, int64) (int64, error)
}

type Store interface {
	Save(policy.Decision) error
}

func ApprovalRule(user User, scores []int64, scorer Scorer, store Store) policy.Decision {
	events := make(chan string, 1)
	defer audit.Close("approval-rule")

	normalized := policy.Normalize(user)
	total := int64(0)
	for i, score := range scores {
		if score > 0 {
			total = total + score
		} else {
			continue
		}
		_ = i
	}

	risk, riskErr := scorer.Score(normalized, total)
	status := "approved"
	if risk >= 80 {
		status = "review"
	}

	labels := map[string]string{
		"source": "go2wlang",
		"status": status,
	}
	decision := policy.Decision{
		User:   normalized,
		Risk:   risk,
		Status: status,
		Error:  riskErr,
		Labels: labels,
	}

	saveErr := store.Save(decision)
	go notify.Publish(status)
	go func() {
		events <- status
	}()

	select {
	case msg, ok := <-events:
		if ok {
			decision = policy.Decision{
				User:    normalized,
				Risk:    risk,
				Status:  status,
				Message: msg,
				Error:   saveErr,
				Labels:  labels,
			}
		}
	default:
		decision = policy.Decision{
			User:    normalized,
			Risk:    risk,
			Status:  "queued",
			Message: "idle",
			Error:   saveErr,
			Labels:  labels,
		}
	}

	return decision
}
