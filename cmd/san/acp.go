package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/genai-io/san/internal/app"
	"github.com/genai-io/san/internal/log"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use: "acp", Short: "Serve the native agent over ACP on stdin/stdout", Args: cobra.NoArgs,
		SilenceUsage: true, SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			log.Stderr()
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			input, err := acpStdio(os.Stdin)
			if err != nil {
				return err
			}
			defer input.Close()
			output, err := acpStdio(os.Stdout)
			if err != nil {
				return err
			}
			defer output.Close()
			return app.RunACP(ctx, input, output, version)
		},
	})
}
