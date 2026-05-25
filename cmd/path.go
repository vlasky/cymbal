package cmd

import (
	"fmt"
	"strings"

	"github.com/1broseidon/cymbal/index"
	"github.com/spf13/cobra"
)

var pathCmd = &cobra.Command{
	Use:   "path <from> <to>",
	Short: "Find the shortest call chain between two symbols",
	Long: `Find the shortest call path from one symbol to another through the
call graph. Returns the ordered sequence of call edges, or reports that no
path exists.

  trace  = "what does X call?" (fan-out BFS)
  impact = "what calls X?" (fan-in BFS)
  path   = "how does X reach Y?" (targeted shortest-path)

The command distinguishes between "no path exists" (the target is
unreachable from the source) and "no path found within depth" (the
search was cut short — try increasing --depth).

Examples:
  cymbal path Execute BuildGraph              # how does Execute reach BuildGraph?
  cymbal path handleAuth validateToken        # auth flow path
  cymbal path handleAuth validateToken -D 8   # search deeper`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		plan := resolveDBs(cmd)
		ensureFresh(plan.Primary)
		jsonOut := getJSONFlag(cmd)
		depth, _ := cmd.Flags().GetInt("depth")
		scope, err := resolveScopeOrError(cmd)
		if err != nil {
			return err
		}

		_, from := parseSymbolArg(args[0])
		_, to := parseSymbolArg(args[1])

		opts := index.TraceOptions{Scope: scope}
		out, err := index.FindPath(plan.Primary, from, to, depth, opts)
		if err != nil {
			return err
		}

		switch out.Status {
		case index.PathNotReachable:
			if jsonOut {
				return writeJSON(map[string]any{
					"from":   from,
					"to":     to,
					"status": "not_reachable",
				})
			}
			return fmt.Errorf("no path exists from '%s' to '%s' (target is unreachable)", from, to)
		case index.PathDepthExhausted:
			if jsonOut {
				return writeJSON(map[string]any{
					"from":   from,
					"to":     to,
					"depth":  depth,
					"status": "depth_exhausted",
				})
			}
			return fmt.Errorf("no path from '%s' to '%s' within depth %d (try increasing --depth)", from, to, depth)
		}

		if jsonOut {
			payload := map[string]any{
				"from":          from,
				"to":            to,
				"hops":          len(out.Path),
				"status":        "found",
				"resolve_scope": string(scope),
				"path":          out.Path,
			}
			return writeJSON(payload)
		}

		var content strings.Builder
		for _, hop := range out.Path {
			fmt.Fprintf(&content, "  [%d] %s → %s  %s:%d\n",
				hop.Depth, hop.Caller, hop.Callee, hop.RelPath, hop.Line)
		}

		meta := []kv{
			{"from", from},
			{"to", to},
			{"hops", fmt.Sprintf("%d", len(out.Path))},
			{"resolve_scope", string(scope)},
		}
		frontmatter(meta, content.String())
		return nil
	},
}

func init() {
	pathCmd.Flags().IntP("depth", "D", 5, "max search depth")
	addResolveScopeFlag(pathCmd)
	rootCmd.AddCommand(pathCmd)
}
