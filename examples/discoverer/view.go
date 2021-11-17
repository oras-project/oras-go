package main

import (
	"context"
	"os"

	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"github.com/spf13/cobra"
	orascontent "oras.land/oras-go/pkg/content"
	ctxo "oras.land/oras-go/pkg/context"
	"oras.land/oras-go/pkg/oras"
	"oras.land/oras-go/pkg/oras/graph"
)

type viewOptions struct {
	targetRef    string
	artifactType string
	keep         bool

	verbose   bool
	debug     bool
	configs   []string
	username  string
	password  string
	insecure  bool
	plainHTTP bool
}

func viewCmd() *cobra.Command {
	var opts viewOptions
	cmd := &cobra.Command{
		Use:     "view <ref>",
		Aliases: []string{"cp"},
		Short:   "Print the manifest for the ref",
		Long: `Copy artifacts from one reference to another reference
	# Examples 
	Prints the reference manifest for the registry ref
		`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.targetRef = args[0]
			return runView(opts)
		},
	}

	cmd.Flags().StringVarP(&opts.artifactType, "artifact-type", "", "", "artifact type to copy from source")
	cmd.Flags().BoolVarP(&opts.verbose, "verbose", "v", false, "verbose output")
	cmd.Flags().BoolVarP(&opts.debug, "debug", "d", false, "debug mode")
	cmd.Flags().StringArrayVarP(&opts.configs, "config", "c", nil, "auth config path")
	cmd.Flags().StringVarP(&opts.username, "username", "u", "", "registry username")
	cmd.Flags().StringVarP(&opts.password, "password", "p", "", "registry password")
	cmd.Flags().BoolVarP(&opts.insecure, "insecure", "", false, "allow connections to SSL registry without certs")
	cmd.Flags().BoolVarP(&opts.plainHTTP, "plain-http", "", false, "use plain http and not https")

	return cmd
}

func runView(opts viewOptions) error {
	err := os.RemoveAll(".working")
	if err != nil {
		return err
	}

	err = os.Mkdir(".working", 0755)
	if err != nil {
		return err
	}

	cached, err := orascontent.NewOCI(".working")
	if err != nil {
		return err
	}

	ctx := context.Background()
	if !opts.verbose {
		ctx = ctxo.WithLoggerDiscarded(ctx)
	}

	client, err := orascontent.NewRegistryWithDiscover(opts.targetRef, orascontent.RegistryOptions{
		Configs:   opts.configs,
		Username:  opts.username,
		Password:  opts.password,
		Insecure:  opts.insecure,
		PlainHTTP: opts.plainHTTP,
	})
	if err != nil {
		return err
	}

	refs, err := oras.Graph(ctx, opts.targetRef, opts.artifactType, client.Resolver, graph.DownloadToArtifact(ctx, opts.targetRef, client.Resolver, cached))
	if err != nil {
		return err
	}

	for _, v := range refs[1:] {
		parent := v.Subject()
		if parent != nil && parent.ArtifactDescriptor().MediaType == artifactspec.MediaTypeArtifactManifest {
			var manifest artifactspec.Manifest

			err = parent.MarshalJsonFromArtifact(ctx, cached, &manifest)
			if err != nil {
				return err
			}
			printJSON(manifest)
		}

	}

	if !opts.keep {
		os.RemoveAll(".working")
	}

	return nil
}
