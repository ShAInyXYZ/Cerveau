package loop

import "encoding/json"

// Identity failures are harness admission failures, never failed implementation
// attempts that can be overridden by a passing check from a stale plan.
type planSnapshotError struct{ detail string }

func (e *planSnapshotError) Error() string { return e.detail }

func samePlanSnapshot(a, b *Plan) bool {
	if a == nil || b == nil {
		return false
	}
	ar, err := json.Marshal(a)
	if err != nil {
		return false
	}
	br, err := json.Marshal(b)
	return err == nil && string(ar) == string(br)
}

func requireCurrentPlan(path string, expected *Plan) (string, error) {
	current, id, err := LatestPlan(path)
	if err != nil {
		return "", err
	}
	if !samePlanSnapshot(expected, current) {
		return "", &planSnapshotError{"plan changed; refresh before executing or saving step state"}
	}
	return id, nil
}
