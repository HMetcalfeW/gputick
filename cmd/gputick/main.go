package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	// version is injected at build time via -ldflags "-X main.version=...".
	version = "dev"

	errNotImplemented = errors.New("not implemented yet")
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "gputick",
		Short:         "Open time series of GPU rental prices and availability",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newCollectCmd(), newServeCmd(), newVersionCmd())
	return root
}

func newCollectCmd() *cobra.Command {
	var once bool
	cmd := &cobra.Command{
		Use:   "collect",
		Short: "Fetch all sources, write raw snapshots and observations",
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("collect (once=%t): %w", once, errNotImplemented)
		},
	}
	cmd.Flags().BoolVar(&once, "once", false, "run a single collection cycle and exit")
	return cmd
}

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Serve the JSON API and MCP server",
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("serve: %w", errNotImplemented)
		},
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the gputick version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "gputick", version)
			return err
		},
	}
}
