package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newProviderCmd is a temporary compatibility stub.
// Some branches register this command in root.go but do not include
// the original implementation file.
func newProviderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provider",
		Short: "Manage provider settings (stub)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("provider command is not available in this branch")
		},
	}
	return cmd
}
