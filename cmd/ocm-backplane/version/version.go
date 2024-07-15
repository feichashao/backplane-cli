package version

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/feichashao/backplane-cli/pkg/info"
)

// VersionCmd represents the version command
var VersionCmd = &cobra.Command{
	Use:   "version",
	Short: "Prints the version",
	Long:  `Display the version of Backplane CLI`,
	RunE:  runVersion,
}

func runVersion(cmd *cobra.Command, argv []string) error {

	buildInfo, available := debug.ReadBuildInfo()

	if len(info.Version) == 0 && available {
		// print version from build info
		_, _ = fmt.Fprintf(os.Stdout, "%s\n", buildInfo.Main.Version)
		return nil
	}

	// Print the version
	_, _ = fmt.Fprintf(os.Stdout, "%s\n", info.Version)

	return nil
}
