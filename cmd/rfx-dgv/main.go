// rfx-dgv is a one-shot Reflex helper: one JSON argument object in, one JSON
// result out. The process working directory is the session workspace.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"cerveau/internal/rfxdgv"
)

func main() {
	if err := run(); err != nil {
		if !errors.Is(err, errFailedCheck) {
			_ = json.NewEncoder(os.Stdout).Encode(errorEnvelope(err))
		}
		os.Exit(1)
	}
}

var errFailedCheck = errors.New("DGV check reported findings; full structured result already emitted")

func errorEnvelope(err error) map[string]any {
	message := strings.ToValidUTF8(err.Error(), "�")
	truncated := len(message) > 2048
	if truncated {
		message = message[:2048]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	code, _, found := strings.Cut(message, ":")
	if !found || len(code) > 40 || strings.ContainsAny(code, " \n\t\r") {
		code = "dgv_failed"
	}
	return map[string]any{"ok": false, "error": map[string]any{"code": code, "message": message, "truncated": truncated}}
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: rfx-dgv list|catalog|context|read|check|create|update (JSON on stdin)")
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, rfxdgv.MaxInputBytes+1))
	if err != nil {
		return err
	}
	workspace, err := os.Getwd()
	if err != nil {
		return err
	}
	core, err := rfxdgv.ResolveCore()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := rfxdgv.Client{Workspace: workspace, Core: core}
	result, err := client.Run(ctx, os.Args[1], raw)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		return err
	}
	if result["ok"] == false {
		return errFailedCheck
	}
	return nil
}
