package main

import (
	"context"
	"errors"
	"os"

	"github.com/containerd/containerd/errdefs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/cobra"
	orascontent "oras.land/oras-go/pkg/content"
	ctxo "oras.land/oras-go/pkg/context"
	"oras.land/oras-go/pkg/oras"
	"oras.land/oras-go/pkg/oras/graph"
	"oras.land/oras-go/pkg/target"
)

type targetOptions struct {
	targetRef string

	debug     bool
	configs   []string
	username  string
	password  string
	insecure  bool
	plainHTTP bool
}

type copyOptions struct {
	from                   targetOptions
	fromDiscover           discoverOptions
	to                     targetOptions
	rescursive             bool
	keep                   bool
	matchAnnotationInclude []string
	matchAnnotationExclude []string
}

func copyCmd() *cobra.Command {
	var opts copyOptions
	cmd := &cobra.Command{
		Use:     "copy <from-ref> <to-ref>",
		Aliases: []string{"cp"},
		Short:   "Copy files from ref to ref",
		Long: `Copy artifacts from one reference to another reference
	# Examples 

	## Copy image only 
	oras cp localhost:5000/net-monitor:v1 localhost:5000/net-monitor-copy:v1

	## Copy image and artifacts
	oras cp localhost:5000/net-monitor:v1 localhost:5000/net-monitor-copy:v1 -r

	# Advanced Examples - Copying with annotation filters 

	## Copy image and artifacts with match include filter
	oras cp localhost:5000/net-monitor:v1 localhost:5000/net-monitor-copy:v1 -r -m annotation.name /test/

	## Copy image and artifacts with match exclude filter
	oras cp localhost:5000/net-monitor:v1 localhost:5000/net-monitor-copy:v1 -r -x annotation.name /test/

	## Copy image with both filters
	oras cp localhost:5000/net-monitor:v1 localhost:5000/net-monitor-copy:v1 -r -m annotation.name /test/ -x other.annotation.name /test/

	## Copy image with multiple match expressions 
	oras cp localhost:5000/net-monitor:v1 localhost:5000/net-monitor-copy:v1 -r -m annotation.name /test/ -m other.annotation.name /test/
		`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.from.targetRef = args[0]
			opts.to.targetRef = args[1]
			return runCopy(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.from.debug, "from-debug", false, "debug mode")
	cmd.Flags().StringArrayVar(&opts.from.configs, "from-config", nil, "auth config path")
	cmd.Flags().StringVar(&opts.from.username, "from-username", "", "registry username")
	cmd.Flags().StringVar(&opts.from.password, "from-password", "", "registry password")
	cmd.Flags().BoolVar(&opts.from.insecure, "from-insecure", false, "allow connections to SSL registry without certs")
	cmd.Flags().BoolVar(&opts.from.plainHTTP, "from-plain-http", false, "use plain http and not https")

	cmd.Flags().StringVarP(&opts.fromDiscover.artifactType, "artifact-type", "", "", "artifact type to copy from source")
	cmd.Flags().BoolVarP(&opts.fromDiscover.verbose, "verbose", "v", false, "verbose output")
	cmd.Flags().BoolVarP(&opts.fromDiscover.debug, "debug", "d", false, "debug mode")
	cmd.Flags().StringArrayVarP(&opts.fromDiscover.configs, "config", "c", nil, "auth config path")
	cmd.Flags().StringVarP(&opts.fromDiscover.username, "username", "u", "", "registry username")
	cmd.Flags().StringVarP(&opts.fromDiscover.password, "password", "p", "", "registry password")
	cmd.Flags().BoolVarP(&opts.fromDiscover.insecure, "insecure", "", false, "allow connections to SSL registry without certs")
	cmd.Flags().BoolVarP(&opts.fromDiscover.plainHTTP, "plain-http", "", false, "use plain http and not https")

	cmd.Flags().BoolVar(&opts.to.debug, "to-debug", false, "debug mode")
	cmd.Flags().StringArrayVar(&opts.to.configs, "to-config", nil, "auth config path")
	cmd.Flags().StringVar(&opts.to.username, "to-username", "", "registry username")
	cmd.Flags().StringVar(&opts.to.password, "to-password", "", "registry password")
	cmd.Flags().BoolVar(&opts.to.insecure, "to-insecure", false, "allow connections to SSL registry without certs")
	cmd.Flags().BoolVar(&opts.to.plainHTTP, "to-plain-http", false, "use plain http and not https")

	cmd.Flags().BoolVarP(&opts.rescursive, "recursive", "r", false, "recursively copy artifacts that reference the artifact being copied")
	cmd.Flags().BoolVarP(&opts.keep, "keep", "k", false, "keep source that is being copied")
	cmd.Flags().StringArrayVarP(&opts.matchAnnotationInclude, "match-annotation-include", "m", nil, "provide an annotation name and regular expression, matches will be included (only applicable with --recursive and -r)")
	cmd.Flags().StringArrayVarP(&opts.matchAnnotationExclude, "match-annotation-exclude", "x", nil, "provide an annotation name and regular expression, matches will be excluded (only applicable with --recursive and -r)")

	return cmd
}

func runCopy(opts copyOptions) error {
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
	if !opts.fromDiscover.verbose {
		ctx = ctxo.WithLoggerDiscarded(ctx)
	}

	// This will clone the objects starting from the targetReference as root
	// it will return the source Target where these references were found, the subject's resolved Object,
	// and an array of Objects that share the subject as a root
	source, subject, references, err := cloneGraph(ctx, opts.from.targetRef, cached, opts)
	if err != nil {
		return err
	}

	if err != nil {
		return err
	}

	var manifest struct {
		Version   int                  `json:"schemaVersion"`
		MediaType string               `json:"mediaType"`
		Config    ocispec.Descriptor   `json:"config"`
		Layers    []ocispec.Descriptor `json:"layers"`
	}

	err = subject.MarshalJsonFromArtifact(ctx, cached, &manifest)
	if err != nil {
		return err
	}

	// Setting up destination
	destination, err := orascontent.NewRegistryWithDiscover(opts.to.targetRef, orascontent.RegistryOptions{
		Configs:   opts.to.configs,
		Username:  opts.to.username,
		Password:  opts.to.password,
		Insecure:  opts.to.insecure,
		PlainHTTP: opts.to.plainHTTP,
	})
	if err != nil {
		return err
	}

	// Deep-copy the image, i.e A -> B, where A = source, -> = cached, B = destination
	deepCopyImage(ctx, subject, manifest, opts, source, cached, destination)

	// If recursive also move objects that share the subject as their root
	if opts.rescursive {
		for _, r := range references {
			err := r.Move(ctx, cached, destination, opts.to.targetRef)
			if err != nil {
				if !errors.Is(err, errdefs.ErrAlreadyExists) {
					return err
				}
			}
		}
	}

	if !opts.keep {
		os.RemoveAll(".working")
	}

	opts.fromDiscover.targetRef = opts.to.targetRef
	opts.fromDiscover.outputType = "tree"
	runDiscover(&opts.fromDiscover)

	return nil
}

// deepCopyImage is an internal function that copies a subject and it's contents from a source target.Target to a destination target.Target
// The vector that connects the two is represented by the cached target.Artifact
// i.e A -> B, where `A` = `source`, `->`` = `cached`, `B` = `destination`
func deepCopyImage(ctx context.Context, subject *target.Object, manifest struct {
	Version   int                  `json:"schemaVersion"`
	MediaType string               `json:"mediaType"`
	Config    ocispec.Descriptor   `json:"config"`
	Layers    []ocispec.Descriptor `json:"layers"`
}, opts copyOptions, source target.Target, cached target.Artifact, destination target.Target) error {
	// Images all share a common format, so instead of referencing one or the other,
	// declare what we are expecting/need to deserialize
	switch subject.Descriptor().MediaType {
	// By default we are expecting that the subject is a type of image manifest. Whether it is an OCI image or Docker Distribution image,
	// They both share this common format
	// If we cannot deserialize the manifest into this format, then we do not have logic that can handle that format, in which case a new case must be added to this switch statement
	default:
		objects, err := target.FromImageManifest(opts.from.targetRef, "", subject.Descriptor(), manifest)
		if err != nil {
			return err
		}

		for _, o := range objects[1:] {
			err := o.Download(ctx, source, cached)
			if err != nil {
				if !errors.Is(err, errdefs.ErrAlreadyExists) {
					return err
				}
			}

			err = o.Move(ctx, cached, destination, opts.to.targetRef)
			if err != nil {
				if !errors.Is(err, errdefs.ErrAlreadyExists) {
					return err
				}
			}
		}

		err = subject.Move(ctx, cached, destination, opts.to.targetRef)
		if err != nil {
			if !errors.Is(err, errdefs.ErrAlreadyExists) {
				return err
			}
		}

		return nil
	}
}

// cloneGraph is an internal function that clones a graph of objects that share the subject reference as it's root
// references that are found are downloaded into the local target.Artifact
func cloneGraph(ctx context.Context, subject string, local target.Artifact, opts copyOptions) (target.Target, *target.Object, []target.Object, error) {
	registry, err := orascontent.NewRegistryWithDiscover(opts.from.targetRef, orascontent.RegistryOptions{
		Configs:   opts.from.configs,
		Username:  opts.from.username,
		Password:  opts.from.password,
		Insecure:  opts.from.insecure,
		PlainHTTP: opts.from.plainHTTP,
	})
	if err != nil {
		return nil, nil, nil, err
	}

	if !opts.fromDiscover.verbose {
		ctx = ctxo.WithLoggerDiscarded(ctx)
	}

	objects, err := oras.Graph(ctx,
		opts.from.targetRef,
		opts.fromDiscover.artifactType,
		registry.Resolver,
		graph.DownloadToArtifact(ctx, opts.from.targetRef, registry, local))
	if err != nil {
		return nil, nil, nil, err
	}

	sobj := &objects[0]
	err = sobj.Download(ctx, registry, local)
	if err != nil {
		return nil, nil, nil, err
	}

	return registry, sobj, objects[1:], nil
}
