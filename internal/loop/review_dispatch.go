package loop

import (
	"context"
	"encoding/json"

	"cerveau/internal/episodic"
	"cerveau/internal/plan"
)

// This capability submits a proposal, not a new verification or an approval.
// The normal dispatcher journals its call/result; runStep stops the batch as
// soon as a valid request has been accepted.
type verificationReviewDispatcher struct {
	path, sessionID, runID, planID string
	index                          int
	original                       plan.Verify
	requested                      *verificationReviewRequest
}

func (t *verificationReviewDispatcher) Name() string { return verificationReviewName }
func (t *verificationReviewDispatcher) Description() string {
	return verificationReviewTool().Function.Description
}
func (t *verificationReviewDispatcher) Schema() map[string]any {
	return verificationReviewTool().Function.Parameters
}
func (t *verificationReviewDispatcher) Execute(_ context.Context, args json.RawMessage) (string, error) {
	events, err := episodic.Replay(t.path)
	if err != nil {
		return "", err
	}
	request, err := newVerificationReview(events, t.sessionID, t.runID, t.planID, t.index, &t.original, string(args))
	if err != nil {
		return "", err
	}
	t.requested = request
	return "Review requested. The harness will retain a fresh result from the unchanged committed check and stop for human review. The proposed check is not applied or executed.", nil
}
