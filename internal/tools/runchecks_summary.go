package tools

import (
	"encoding/json"
	"unicode/utf8"
)

const runChecksSummaryLimit = 7500

type runChecksEvidence struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

type runChecksSummary struct {
	runChecksReceipt
	Evidence         runChecksEvidence `json:"evidence"`
	ParsedTestCounts map[string]int    `json:"parsed_test_counts"`
	SummaryTruncated bool              `json:"summary_truncated"`
	Omitted          map[string]int    `json:"omitted,omitempty"`
}

// Compact only the model-facing copy. The already-written receipt retains all
// bounded streams, test records, source hashes and unchanged command provenance.
func summarizeRunChecks(receipt *runChecksReceipt, full []byte) (string, error) {
	s := runChecksSummary{runChecksReceipt: *receipt,
		Evidence:         runChecksEvidence{Path: receipt.ReceiptPath, SHA256: runChecksSHA(full), Bytes: len(full)},
		ParsedTestCounts: map[string]int{"pass": 0, "fail": 0, "skip": 0, "unverified": 0},
		Omitted:          map[string]int{},
	}
	for _, test := range receipt.Tests {
		s.ParsedTestCounts[test.Status]++
	}
	encode := func() ([]byte, error) { return json.Marshal(s) }
	b, err := encode()
	if err != nil {
		return "", err
	}
	if len(b) <= runChecksSummaryLimit {
		return string(b), nil
	}
	s.SummaryTruncated = true
	s.Omitted["sources_before"] = len(s.Before)
	s.Omitted["sources_after"] = len(s.After)
	s.Before = nil
	s.After = nil
	preview := func(value string, limit int) string {
		if len(value) <= limit {
			return value
		}
		out := value[:limit]
		for !utf8.ValidString(out) && len(out) > 0 {
			out = out[:len(out)-1]
		}
		return out
	}
	streams := func(limit int) {
		s.Stdout = preview(receipt.Stdout, limit)
		s.Stderr = preview(receipt.Stderr, limit)
		s.Omitted["stdout_bytes"] = len(receipt.Stdout) - len(s.Stdout)
		s.Omitted["stderr_bytes"] = len(receipt.Stderr) - len(s.Stderr)
	}
	streams(1024)
	s.Tests = append([]runChecksTest(nil), receipt.Tests...)
	if len(s.Tests) > 8 {
		s.Tests = s.Tests[:8]
	}
	s.Omitted["tests"] = len(receipt.Tests) - len(s.Tests)
	for i := range s.Tests {
		test := &s.Tests[i]
		before, _ := json.Marshal(test)
		test.Name = preview(test.Name, 160)
		test.Package = preview(test.Package, 160)
		test.Location = preview(test.Location, 160)
		test.Evidence = preview(test.Evidence, 256)
		if len(test.Expected) > 128 {
			test.Expected = nil
		}
		if len(test.Actual) > 128 {
			test.Actual = nil
		}
		after, _ := json.Marshal(test)
		if string(before) != string(after) {
			s.Omitted["test_records_with_shortened_details"]++
		}
	}
	if command, _ := json.Marshal(s.Command); len(command) > 1800 {
		s.Omitted["command_arguments"] = len(s.Command.Args)
		s.Command.Args = nil
		if len(s.Command.CWD) > 300 {
			s.Omitted["command_cwd_bytes"] = len(s.Command.CWD)
			s.Command.CWD = ""
		}
		if len(s.Command.Executable) > 300 {
			s.Omitted["command_executable_bytes"] = len(s.Command.Executable)
			s.Command.Executable = ""
		}
	}
	s.Reasons = append([]string(nil), receipt.Reasons...)
	if len(s.Reasons) > 3 {
		s.Reasons = s.Reasons[:3]
	}
	s.Omitted["reasons"] = len(receipt.Reasons) - len(s.Reasons)
	for i := range s.Reasons {
		original := s.Reasons[i]
		s.Reasons[i] = preview(original, 256)
		s.Omitted["reason_bytes"] += len(original) - len(s.Reasons[i])
	}
	b, err = encode()
	if err != nil {
		return "", err
	}
	if len(b) <= runChecksSummaryLimit {
		return string(b), nil
	}
	// JSON escaping can multiply diagnostic bytes. Reduce the detail copies and
	// measure encoded bytes again instead of assuming character counts suffice.
	streams(128)
	if len(s.Tests) > 2 {
		s.Tests = s.Tests[:2]
	}
	s.Omitted["tests"] = len(receipt.Tests) - len(s.Tests)
	b, err = encode()
	if err != nil {
		return "", err
	}
	if len(b) <= runChecksSummaryLimit {
		return string(b), nil
	}
	// This last form has no unbounded caller text. The receipt remains the source
	// of exact command, before/after SHA versions, diagnostics and comparisons.
	minimal := map[string]any{
		"version": receipt.Version, "runner": receipt.Runner, "status": receipt.Status,
		"receipt_path": receipt.ReceiptPath, "evidence": s.Evidence,
		"check_identity_sha256": receipt.Identity, "input_sha256": receipt.InputSHA256,
		"elapsed_ms": receipt.ElapsedMS, "exit_code": receipt.ExitCode, "timed_out": receipt.TimedOut,
		"verification_scope": receipt.VerificationScope, "parsed_test_counts": s.ParsedTestCounts,
		"summary_truncated": true, "details": "Full bounded output, exact command, source hashes and comparison are in the hashed evidence receipt.",
		"omitted": map[string]int{"tests": len(receipt.Tests), "sources_before": len(receipt.Before), "sources_after": len(receipt.After), "stdout_bytes": len(receipt.Stdout), "stderr_bytes": len(receipt.Stderr), "command_arguments": len(receipt.Command.Args), "reasons": len(receipt.Reasons)},
	}
	b, err = json.Marshal(minimal)
	return string(b), err
}
