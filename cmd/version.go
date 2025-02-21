package cmd

import (
	"fmt"

	"github.com/imua-xyz/price-feeder/version"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "display version information",
	Long:  "display version information",
	PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
		// do nothing here, just override persistentPreRunE defined in parent
		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(version.Version)
	},
}
