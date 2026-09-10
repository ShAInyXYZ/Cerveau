package tools

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// Leave room for the short non-pass error appended by the tool execution loop.
const diagnosticMaxSummaryBytes = 7900

type diagnosticSummary struct {
	SchemaVersion       int                 `json:"schema_version"`
	Tool                string              `json:"tool"`
	Checker             string              `json:"checker"`
	CheckID             string              `json:"check_id"`
	Status              string              `json:"status"`
	Complete            bool                `json:"complete"`
	Reason              string              `json:"reason"`
	Scope               string              `json:"scope"`
	ReceiptPath         string              `json:"receipt_path"`
	ReceiptSHA256       string              `json:"receipt_sha256"`
	ReceiptBytes        int                 `json:"receipt_bytes"`
	ReceiptSaved        bool                `json:"receipt_saved"`
	ReceiptError        string              `json:"receipt_error,omitempty"`
	SourcesRecorded     int                 `json:"sources_recorded"`
	SourceManifestSHA   string              `json:"source_manifest_sha256"`
	ChecksRecorded      int                 `json:"checks_recorded"`
	DiagnosticsRecorded int                 `json:"diagnostics_recorded"`
	DiagnosticsOmitted  int                 `json:"diagnostics_omitted"`
	Diagnostics         []diagnosticFinding `json:"diagnostics"`
	EvidenceNote        string              `json:"evidence_note"`
	Limitations         []string            `json:"limitations"`
}

func (t *CodeDiagnostics) diagnosticFinish(report *diagnosticReport) (string, error) {
	data, receiptErr := t.diagnosticWriteReceipt(report)
	if receiptErr != nil {
		report.Status, report.Complete, report.Reason = "unverified", false, "could not save the complete evidence receipt"
	}
	sources, _ := json.Marshal(report.Sources)
	summary := diagnosticSummary{
		SchemaVersion: report.SchemaVersion, Tool: report.Tool, Checker: report.Checker, CheckID: report.CheckID,
		Status: report.Status, Complete: report.Complete, Reason: report.Reason, Scope: report.Scope,
		ReceiptPath: report.ReceiptPath, ReceiptSaved: receiptErr == nil,
		SourcesRecorded: len(report.Sources), SourceManifestSHA: diagnosticSHA(sources), ChecksRecorded: len(report.Checks),
		DiagnosticsRecorded: len(report.Diagnostics), DiagnosticsOmitted: len(report.Diagnostics), Diagnostics: []diagnosticFinding{},
		EvidenceNote: "Whole diagnostic previews only; diagnostics_omitted counts recorded findings available in the receipt. The receipt retains every check, source hash and captured raw stream. Capture is bounded at 128 KiB per stream; parsing at 128 findings and 4096 bytes per message. Truncated or incomplete evidence is unverified.",
		Limitations:  report.Limitations,
	}
	if receiptErr != nil {
		summary.ReceiptError = diagnosticShorten(receiptErr.Error(), 512)
		summary.EvidenceNote = "Receipt saving failed: complete evidence was not persisted. Whole diagnostic previews are shown with an explicit omission count. This result cannot verify the requested check."
	} else {
		summary.ReceiptSHA256, summary.ReceiptBytes = diagnosticSHA(data), len(data)
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return "", err
	}
	for _, finding := range report.Diagnostics {
		summary.Diagnostics = append(summary.Diagnostics, finding)
		summary.DiagnosticsOmitted--
		candidate, marshalErr := json.Marshal(summary)
		if marshalErr != nil {
			return "", marshalErr
		}
		if len(candidate) > diagnosticMaxSummaryBytes {
			break
		}
		encoded = candidate
	}
	// Fixed summary metadata has a bounded size independently of checker output.
	// This guard refuses rather than slicing JSON or diagnostic claims.
	if len(encoded) > diagnosticMaxSummaryBytes {
		return "", errors.New("code_diagnostics unverified: summary metadata exceeds output bound")
	}
	if report.Status != "pass" {
		return string(encoded), fmt.Errorf("code_diagnostics %s; see structured evidence", report.Status)
	}
	return string(encoded), nil
}

func (t *CodeDiagnostics) diagnosticWriteReceipt(report *diagnosticReport) ([]byte, error) {
	root, err := os.OpenRoot(t.j.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, path := range []string{".devcheck", ".devcheck/code-diagnostics"} {
		if err := root.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
			return nil, err
		}
		info, err := root.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("diagnostic evidence directory must be a real workspace directory")
		}
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	report.ReceiptPath = ".devcheck/code-diagnostics/" + hex.EncodeToString(id[:]) + ".json"
	data, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	f, err := root.OpenFile(report.ReceiptPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("exclusive diagnostic receipt: %w", err)
	}
	n, writeErr := f.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return nil, err
	}
	return data, nil
}
