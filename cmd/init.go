package cmd

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

//go:embed default.toml
var defaultConfig []byte

var (
	initOutput string
	initForce  bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Generate a default miser.toml config file",
	Example: `  miser init                   Write ./miser.toml
  miser init -o /etc/miser.toml  Write to a custom path
  miser init --force           Overwrite existing file`,
	RunE: runInit,
}

func init() {
	initCmd.Flags().StringVarP(&initOutput, "output", "o", "miser.toml",
		"output file path")
	initCmd.Flags().BoolVarP(&initForce, "force", "f", false,
		"overwrite if file already exists")
	rootCmd.AddCommand(initCmd)
}

func runInit(_ *cobra.Command, _ []string) error {
	if _, err := os.Stat(initOutput); err == nil && !initForce {
		return fmt.Errorf("%s already exists (use --force to overwrite)", initOutput)
	}

	dir := filepath.Dir(initOutput)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating directory: %w", err)
		}
	}

	if err := os.WriteFile(initOutput, defaultConfig, 0o644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Printf("Wrote %s\n", initOutput)
	return nil
}
