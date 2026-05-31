// Package commands wires the arcctl cobra command tree.
//
// Each top-level command lives in its own file. PR1 ships `root` +
// `config`. Later PRs add: query, write, import, db, auth, cluster, ops.
package commands

import "github.com/spf13/cobra"

// NewRoot returns the arcctl root command with all subcommands attached.
// The version string is injected by main() from a -ldflags-built var.
func NewRoot(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "arcctl",
		Short: "Arc CLI — operator-facing client for Arc time-series databases",
		Long: `arcctl talks to one or more Arc clusters via the HTTP API.

Manage multiple connections (dev/staging/prod) in ~/.arcctl/config.toml
with one marked active. Override per-command with -c/--connection or the
ARC_CONNECTION / ARC_ENDPOINT / ARC_TOKEN env vars.

First-time setup:
    arcctl config create --name local --endpoint http://localhost:8000 --token <T> --activate
    arcctl config current
`,
		Version: version,
		// Don't print usage on every error — most errors are runtime
		// (network, auth, server) where the usage text is noise.
		SilenceUsage: true,
	}

	root.AddCommand(newConfigCmd())
	return root
}
