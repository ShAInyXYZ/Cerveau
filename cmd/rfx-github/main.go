package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"cerveau/internal/rfxgithub"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: rfx-github <talent> (one JSON object on stdin)")
		os.Exit(2)
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot resolve workspace")
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(rfxgithub.DecodeAndRun(ctx, dir, os.Args[1], os.Stdin, os.Stdout, os.Getenv("CRV_RFX_HUMAN_APPROVED") == "1"))
}
