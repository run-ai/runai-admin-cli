package command

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

type CommandWrapper struct {
	runFunc (func(cmd *cobra.Command, args []string) error)
}

func WrapRunCommand(runFunc func(cmd *cobra.Command, args []string) error) func(cmd *cobra.Command, args []string) {
	return func(cmd *cobra.Command, args []string) {
		err := runFunc(cmd, args)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}
}
