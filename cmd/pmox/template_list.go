package main

import (
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// newTemplateListCmd lists the cluster's templates (pmox-built or not),
// the values 'pmox launch --template' accepts.
func newTemplateListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List templates",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			client, resolved, err := buildClient(ctx, cmd)
			if err != nil {
				return err
			}
			sp := startSpin("Loading templates…")
			all, err := client.ClusterResources(ctx, "vm")
			sp.Stop()
			if err != nil {
				return fmt.Errorf("list templates: %w", err)
			}
			type row struct {
				VMID    int    `json:"vmid"`
				Name    string `json:"name"`
				Node    string `json:"node"`
				Default bool   `json:"default"`
			}
			rows := []row{}
			for _, r := range all {
				if !r.IsTemplate() {
					continue
				}
				def := resolved.Server.Template == fmt.Sprint(r.VMID) || resolved.Server.Template == r.Name
				rows = append(rows, row{r.VMID, r.Name, r.Node, def})
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].VMID < rows[j].VMID })
			w := cmd.OutOrStdout()
			if outputMode == "json" {
				return printJSON(w, rows)
			}
			if len(rows) == 0 {
				fmt.Fprintln(w, "no templates — build one with 'pmox template create'")
				return nil
			}
			tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
			fmt.Fprintln(tw, "VMID\tNAME\tNODE\tDEFAULT")
			for _, r := range rows {
				d := ""
				if r.Default {
					d = "*"
				}
				fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", r.VMID, r.Name, r.Node, d)
			}
			return tw.Flush()
		},
	}
}
