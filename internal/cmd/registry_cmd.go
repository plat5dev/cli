package cmd

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/plat5dev/cli/internal/prompt"
	"github.com/plat5dev/cli/internal/registry"
	"github.com/spf13/cobra"
)

func newRegistryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Manage plugin registries (first-party and third-party)",
		Long: `Manage plugin registries used by plat5 init and plat5 add.

The official registry (plat5dev/registry) is configured by default. Add private
or community registries with plat5 registry add; they use the exact same format.`,
	}

	cmd.AddCommand(
		newRegistryListCmd(),
		newRegistryAddCmd(),
		newRegistryRemoveCmd(),
		newRegistryUpdateCmd(),
		newRegistrySearchCmd(),
		newRegistryValidateCmd(),
	)
	return cmd
}

func newRegistryListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured plugin registries",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := registry.LoadConfig()
			if err != nil {
				return err
			}
			for _, r := range cfg.Registries {
				tag := ""
				if r.Default {
					tag = " (default)"
				}
				fmt.Printf("  %-14s %s%s\n", r.Name, r.Source, tag)
			}
			return nil
		},
	}
}

func newRegistryAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <name> <source>",
		Short: "Add a third-party or local plugin registry",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			source := strings.TrimSpace(args[1])
			if name == "" || strings.ContainsAny(name, "/\\ \t") {
				return fmt.Errorf("invalid registry name %q", name)
			}
			cfg, err := registry.LoadConfig()
			if err != nil {
				return err
			}
			updated := false
			for i := range cfg.Registries {
				if cfg.Registries[i].Name == name {
					cfg.Registries[i].Source = source
					updated = true
					break
				}
			}
			if !updated {
				cfg.Registries = append(cfg.Registries, registry.RegistryRef{
					Name:   name,
					Source: source,
				})
			}
			if err := registry.SaveConfig(cfg); err != nil {
				return err
			}
			fmt.Printf("Added registry %q (%s).\n", name, source)
			return nil
		},
	}
}

func newRegistryRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a configured plugin registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == registry.DefaultRegistryName {
				return fmt.Errorf("cannot remove the default %q registry", registry.DefaultRegistryName)
			}
			cfg, err := registry.LoadConfig()
			if err != nil {
				return err
			}
			var kept []registry.RegistryRef
			removed := false
			for _, r := range cfg.Registries {
				if r.Name == name {
					removed = true
					continue
				}
				kept = append(kept, r)
			}
			if !removed {
				return fmt.Errorf("registry %q not found", name)
			}
			cfg.Registries = kept
			if err := registry.SaveConfig(cfg); err != nil {
				return err
			}
			fmt.Printf("Removed registry %q.\n", name)
			return nil
		},
	}
}

func newRegistryUpdateCmd() *cobra.Command {
	var sourcesDir string
	cmd := &cobra.Command{
		Use:   "update [name]",
		Short: "Refresh cached registry index(es)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			res := &registry.Resolver{SourcesDir: absIfSet(sourcesDir)}
			if err := res.Update(name); err != nil {
				return err
			}
			if name == "" {
				fmt.Println("Updated all registries.")
			} else {
				fmt.Printf("Updated registry %q.\n", name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&sourcesDir, "sources", "", "Resolve first-party repos from local checkouts in this directory")
	return cmd
}

func newRegistrySearchCmd() *cobra.Command {
	var (
		filterType string
		sourcesDir string
		regSrc     string
	)
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search available plugins across configured registries",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := ""
			if len(args) == 1 {
				query = args[0]
			}
			res := &registry.Resolver{
				SourcesDir:       absIfSet(sourcesDir),
				RegistryOverride: regSrc,
			}
			results, err := res.Search(query, filterType)
			if err != nil {
				return err
			}
			if len(results) == 0 {
				fmt.Println("No matching plugins.")
				return nil
			}
			fmt.Printf("  %-24s %-18s %-10s %s\n", "NAME", "TYPE", "REGISTRY", "DESCRIPTION")
			for _, r := range results {
				name := r.Entry.ID
				if r.Registry != registry.DefaultRegistryName {
					name = r.Registry + "/" + r.Entry.ID
				}
				desc := r.Entry.Description
				if !r.Compatible {
					desc += " (requires newer CLI)"
				}
				fmt.Printf("  %-24s %-18s %-10s %s\n", name, r.Entry.Type, r.Registry, desc)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&filterType, "type", "", "Filter by plugin type (preset, idp, authorizer, service, customer-console, operator-console, observability, ci, deploy, recipe)")
	cmd.Flags().StringVar(&sourcesDir, "sources", "", "Resolve first-party repos from local checkouts in this directory")
	cmd.Flags().StringVar(&regSrc, "registry", "", "Override the default registry source")
	return cmd
}

func newRegistryValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [directory]",
		Short: "Validate registry.yml and every inline plat5.plugin.yml offline",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			abs, err := filepath.Abs(prompt.ExpandPath(dir))
			if err != nil {
				return err
			}
			problems, err := registry.ValidateDir(abs)
			if err != nil {
				return err
			}
			if len(problems) > 0 {
				for _, p := range problems {
					fmt.Println("✗", p)
				}
				return errors.New("registry validation failed")
			}
			fmt.Printf("✓ %s is valid.\n", filepath.Join(abs, registry.IndexFileName))
			return nil
		},
	}
}

func absIfSet(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(prompt.ExpandPath(p))
	if err != nil {
		return p
	}
	return abs
}
