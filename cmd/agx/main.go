package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if isRuntimeInvocation(os.Args) {
		executeRuntimeCommand()
		return
	}
	if isRuntimeBackedInvocation(os.Args) {
		executeRuntimeBackedCommand()
		return
	}
	if isDoctorInvocation(os.Args) {
		executeDoctorCommand()
		return
	}
	executeRuntimeBackedCommand()
}

func isDoctorInvocation(args []string) bool {
	return len(args) > 1 && args[1] == "doctor"
}

func executeDoctorCommand() {
	rootCmd := &cobra.Command{
		Use:           "agx",
		Short:         "Run and manage local coding agents through the AGX runtime",
		Version:       versionString(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	rootCmd.AddCommand(newDoctorCmd())
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(exitCodeFor(err))
	}
}
