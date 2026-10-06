package main

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/server"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// When 'pmox launch' runs on a terminal with no default template, it
// settles one before asking for the VM's name and size: pick one of the
// cluster's existing templates, or build a new one right away (the
// template build's own pickers let the user back out with Ctrl-C).
// Whichever is chosen becomes the server's default template.

var (
	ensureLaunchTemplateFn = ensureLaunchTemplate
	pickLaunchTemplateFn   = tui.Select
)

const buildNewTemplate = "\x00pmox-build-template"

func ensureLaunchTemplate(cmd *cobra.Command, client *pveclient.Client, resolved *server.Resolved) error {
	ctx := cmd.Context()
	stderr := cmd.ErrOrStderr()
	all, err := client.ClusterResources(ctx, "vm")
	if err != nil {
		return fmt.Errorf("list templates: %w", err)
	}
	var tmpls []pveclient.Resource
	for _, r := range all {
		if r.IsTemplate() && (resolved.Server.Node == "" || r.Node == resolved.Server.Node) {
			tmpls = append(tmpls, r)
		}
	}
	sort.Slice(tmpls, func(i, j int) bool { return tmpls[i].VMID < tmpls[j].VMID })

	choice := buildNewTemplate
	if len(tmpls) > 0 {
		opts := make([]huh.Option[string], 0, len(tmpls)+1)
		for _, t := range tmpls {
			opts = append(opts, huh.NewOption(fmt.Sprintf("%d  %s", t.VMID, t.Name), strconv.Itoa(t.VMID)))
		}
		opts = append(opts, huh.NewOption("+ Build a new Ubuntu template", buildNewTemplate))
		if choice, err = pickLaunchTemplateFn("No default template is set — launch from:", opts); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(stderr, "No template on the cluster yet — building one now ('pmox template create'). Ctrl-C to cancel.")
	}

	if choice == buildNewTemplate {
		upload, closeUpload := newSnippetUploader(resolved)
		defer closeUpload()
		bridge := firstNonEmpty(resolved.Server.Bridge, "vmbr0")
		r, err := templateRunFn(ctx, buildTemplateOptions(cmd, client, resolved.Server.Node, bridge, createTemplateDuringInitWait, upload))
		if err != nil {
			return fmt.Errorf("build template: %w", err)
		}
		choice = strconv.Itoa(r.VMID)
		fmt.Fprintf(stderr, "built template %s (vmid %d)\n", r.Name, r.VMID)
	}

	resolved.Server.Template = choice
	cfg, err := config.Load()
	if err == nil {
		if srv, ok := cfg.Servers[resolved.URL]; ok {
			srv.Template = choice
			err = cfg.Save()
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s\n", tui.Warnf(fmt.Sprintf("warning: could not save template %s as the default: %v", choice, err)))
	} else {
		fmt.Fprintf(stderr, "template %s saved as the default for %s\n", choice, contextLabelFor(cfg, resolved.URL))
	}
	return nil
}
