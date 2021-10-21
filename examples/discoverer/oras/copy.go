package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/reference"
	"github.com/containerd/containerd/remotes"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"oras.land/oras-go/pkg/oras"

	orascontent "oras.land/oras-go/pkg/content"
	ctxo "oras.land/oras-go/pkg/context"
)

type pullOptions struct {
	targetRef          string
	allowedMediaTypes  []string
	allowAllMediaTypes bool
	allowEmptyName     bool
	keepOldFiles       bool
	verbose            bool

	debug     bool
	configs   []string
	username  string
	password  string
	insecure  bool
	plainHTTP bool
}

type pushOptions struct {
	targetRef    string
	artifactType string
	artifactRefs string
	verbose      bool

	debug     bool
	configs   []string
	username  string
	password  string
	insecure  bool
	plainHTTP bool
}

type copyOptions struct {
	from                   pullOptions
	fromDiscover           discoverOptions
	to                     pushOptions
	rescursive             bool
	keep                   bool
	matchAnnotationInclude []string
	matchAnnotationExclude []string
}

type copyObject struct {
	manifest     *ocispec.Descriptor
	digest       digest.Digest
	name         string
	subject      string
	artifactType string
	mediaType    string
	size         int64
	annotations  map[string]string
}

type copyRecursiveOptions struct {
	additionalFiles []copyObject
	filter          func(artifactspec.Descriptor) bool
	artifactType    string
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

	cmd.Flags().StringArrayVar(&opts.from.allowedMediaTypes, "from-media-type", nil, "allowed media types to be pulled")
	cmd.Flags().BoolVar(&opts.from.keepOldFiles, "from-keep-old-files", false, "do not replace existing files when pulling, treat them as errors")
	cmd.Flags().BoolVar(&opts.from.verbose, "from-verbose", false, "verbose output")
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

	cmd.Flags().BoolVar(&opts.to.verbose, "to-verbose", false, "verbose output")
	cmd.Flags().BoolVar(&opts.to.debug, "to-debug", false, "debug mode")
	cmd.Flags().StringArrayVar(&opts.to.configs, "to-config", nil, "auth config path")
	cmd.Flags().StringVar(&opts.to.username, "to-username", "", "registry username")
	cmd.Flags().StringVar(&opts.to.password, "to-password", "", "registry password")
	cmd.Flags().BoolVar(&opts.to.insecure, "to-insecure", false, "allow connections to SSL registry without certs")
	cmd.Flags().BoolVar(&opts.to.plainHTTP, "to-plain-http", false, "use plain http and not https")

	cmd.Flags().BoolVarP(&opts.rescursive, "recursive", "r", false, "recursively copy artifacts that reference the artifact being copied")
	cmd.Flags().BoolVarP(&opts.keep, "keep", "k", false, "keep source files that were copied")
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

	var recursiveOptions *copyRecursiveOptions
	if opts.rescursive {
		recursiveOptions = &copyRecursiveOptions{
			artifactType: opts.fromDiscover.artifactType,
		}

		if opts.matchAnnotationInclude != nil || opts.matchAnnotationExclude != nil {
			recursiveOptions.filter = build_match_filter(opts.matchAnnotationInclude, opts.matchAnnotationExclude)
		}
	}

	opts.from.allowAllMediaTypes = true
	opts.from.allowEmptyName = true

	desc, pulled, err := copy_source(opts.from, opts.to.targetRef, cached, recursiveOptions)
	if err != nil {
		return err
	}

	err = copy_dest(opts.to, cached, &desc, pulled...)
	if err != nil {
		return err
	}

	if opts.rescursive {
		_, host, namespace, _, err := parse(opts.to.targetRef)
		if err != nil {
			return err
		}

		for _, r := range recursiveOptions.additionalFiles {
			p := pushOptions{
				targetRef:    fmt.Sprintf("%s/%s", host, namespace),
				artifactType: r.artifactType,
				artifactRefs: r.subject,
			}

			err = copy_dest(p, cached, r.manifest, ocispec.Descriptor{
				Size:        r.size,
				Digest:      r.digest,
				MediaType:   r.mediaType,
				Annotations: r.annotations,
			})
			if err != nil {
				return err
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

func copy_dest(opts pushOptions, store content.Store, parent *ocispec.Descriptor, files ...ocispec.Descriptor) error {
	ctx := context.Background()
	if opts.debug {
		logrus.SetLevel(logrus.DebugLevel)
	} else if !opts.verbose {
		ctx = ctxo.WithLoggerDiscarded(ctx)
	}

	registry, err := orascontent.NewRegistryWithDiscover(opts.targetRef, orascontent.RegistryOptions{
		Configs:   opts.configs,
		Username:  opts.username,
		Password:  opts.password,
		Insecure:  opts.insecure,
		PlainHTTP: opts.plainHTTP,
	})
	if err != nil {
		return err
	}

	if len(files) == 0 {
		fmt.Println("Uploading empty artifact")
	}

	if registry == nil {
		return oras.ErrResolverUndefined
	}

	pusher, err := registry.Pusher(ctx, opts.targetRef)
	if err != nil {
		return err
	}

	for _, f := range files {
		// Skip the parent, this will be pushed last
		if f.Digest == parent.Digest {
			continue
		}

		err := copy_push(ctx, store, pusher, f)
		if err != nil {
			return err
		}
	}

	err = copy_push(ctx, store, pusher, *parent)
	if err != nil {
		return err
	}

	fmt.Println("Pushed", opts.targetRef)
	fmt.Println("Digest:", parent.Digest)
	fmt.Println("Artifact type:", opts.artifactType)
	fmt.Println("Subject:", opts.artifactRefs)

	return nil
}

func copy_push(ctx context.Context, source content.Store, pusher remotes.Pusher, desc ocispec.Descriptor) error {
	w, err := pusher.Push(ctx, desc)
	if err != nil {
		if errors.Is(err, errdefs.ErrAlreadyExists) {
			return nil
		}
		return err
	}
	defer w.Close()

	r, err := source.ReaderAt(ctx, desc)
	if err != nil {
		return err
	}
	defer r.Close()

	err = content.Copy(ctx, w, content.NewReader(r), desc.Size, desc.Digest)
	if err != nil {
		return err
	}

	return nil
}

func copy_source(opts pullOptions, destref string, store *orascontent.OCI, recursiveOptions *copyRecursiveOptions) (ocispec.Descriptor, []ocispec.Descriptor, error) {
	desc, pulled, err := copy_fetch(opts, store)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}

	if recursiveOptions != nil {
		discoverOpts := discoverOptions{
			targetRef:    opts.targetRef,
			artifactType: recursiveOptions.artifactType,
		}

		runDiscover(&discoverOpts)

		_, host, namespace, _, err := parse(opts.targetRef)
		if err != nil {
			return ocispec.Descriptor{}, nil, err
		}

		match := func(artifactspec.Descriptor) bool {
			return true
		}

		if recursiveOptions.filter != nil {
			match = recursiveOptions.filter
		}

		for _, a := range discoverOpts.outputRefs {
			if match(a) {
				opts := pullOptions{
					targetRef:          fmt.Sprintf("%s/%s@%s", host, namespace, a.Digest),
					allowAllMediaTypes: true,
					allowEmptyName:     true,
				}

				_, _, destnamespace, _, err := parse(destref)
				if err != nil {
					return ocispec.Descriptor{}, nil, err
				}

				destRef := fmt.Sprintf("%s/%s@%s", host, destnamespace, a.Digest)

				// In the next call to copy_source, I might end up adding additional files from my children
				// In that case, I will need to ensure those additional files get added after the files that will be added here
				// The below logic does this by first checking if that is the case, restoring state to what it was before the call
				// and caching the additional files in another array. The code then proceeds normally, until the end when I concatenate the array back
				before := len(recursiveOptions.additionalFiles)
				p, blobs, err := copy_source(opts, destRef, store, recursiveOptions)
				if err != nil {
					return ocispec.Descriptor{}, nil, err
				}
				after := len(recursiveOptions.additionalFiles)

				insertBlobs := after > before
				var insertAfter []copyObject
				if insertBlobs {
					// additional files were added while copying, we need to insert at the old location
					newBlobs := recursiveOptions.additionalFiles[before:]
					insertAfter = make([]copyObject, len(newBlobs))
					copy(insertAfter, newBlobs)

					recursiveOptions.additionalFiles = recursiveOptions.additionalFiles[:before]
				}

				for _, blob := range blobs {
					if blob.MediaType == artifactspec.MediaTypeArtifactManifest {
						continue
					}

					name := blob.Annotations[ocispec.AnnotationTitle]

					// I've declared the struct this way in places where I construct the actual copyObject value
					// This is so that if I update the type definition, the below code will not compile until it is updated
					// This is useful to know from an maintainability aspect. If someone changes the type they will know each place it must be updated,
					// This can help the contributor consider what types of members make sense to add.
					// For example if I added a member to the underlying type, but I see that in the places I construct instances of underlying type,
					// I have no access to the the desired value of the member, I might need to reconsider or plan for additional changes
					recursiveOptions.additionalFiles = append(recursiveOptions.additionalFiles, struct {
						manifest     *ocispec.Descriptor
						digest       digest.Digest
						name         string
						subject      string
						artifactType string
						mediaType    string
						size         int64
						annotations  map[string]string
					}{
						manifest:     &p,
						digest:       blob.Digest,
						name:         name,
						annotations:  blob.Annotations,
						size:         blob.Size,
						subject:      destref,
						artifactType: a.ArtifactType,
						mediaType:    blob.MediaType,
					})
				}

				if insertAfter != nil {
					recursiveOptions.additionalFiles = append(recursiveOptions.additionalFiles, insertAfter...)
				}
			}
		}
	}

	return desc, pulled, nil
}

func copy_fetch(opts pullOptions, store *orascontent.OCI) (ocispec.Descriptor, []ocispec.Descriptor, error) {
	ctx := context.Background()
	if opts.debug {
		logrus.SetLevel(logrus.DebugLevel)
	} else if !opts.verbose {
		ctx = ctxo.WithLoggerDiscarded(ctx)
	}

	if opts.allowAllMediaTypes {
		opts.allowedMediaTypes = nil
	} else if len(opts.allowedMediaTypes) == 0 {
		opts.allowedMediaTypes = []string{orascontent.DefaultBlobMediaType, orascontent.DefaultBlobDirMediaType}
	}

	registry, err := orascontent.NewRegistryWithDiscover(opts.targetRef, orascontent.RegistryOptions{
		Configs:   opts.configs,
		Username:  opts.username,
		Password:  opts.password,
		Insecure:  opts.insecure,
		PlainHTTP: opts.plainHTTP,
	})
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}

	pullOpts := []PullOpt{
		WithAllowedMediaTypes(opts.allowedMediaTypes),
		WithPullStatusTrack(os.Stdout),
		WithPullEmptyNameAllowed(),
	}

	desc, artifacts, err := Pull(ctx, registry.Resolver, opts.targetRef, store, pullOpts...)
	if err != nil {
		if err == reference.ErrObjectRequired {
			return ocispec.Descriptor{}, nil, fmt.Errorf("image reference format is invalid. Please specify <name:tag|name@digest>")
		}
		return ocispec.Descriptor{}, nil, err
	}

	fmt.Println("Pulled", opts.targetRef)
	fmt.Println("Digest:", desc.Digest)

	return desc, artifacts, nil
}

var (
	referenceRegex = regexp.MustCompile(`([.\w\d:-]+)\/{1,}?([a-z0-9]+(?:[/._-][a-z0-9]+)*(?:[a-z0-9]+(?:[/._-][a-z0-9]+)*)*)[:@]([a-zA-Z0-9_]+:?[a-zA-Z0-9._-]{0,127})`)
)

func build_match_filter(matchInclude []string, matchExclude []string) func(a artifactspec.Descriptor) bool {
	var (
		includes map[string]*regexp.Regexp = make(map[string]*regexp.Regexp)
		excludes map[string]*regexp.Regexp = make(map[string]*regexp.Regexp)
	)

	for _, m := range matchInclude {
		args := strings.Split(m, " ")
		if len(args) > 0 {
			annotationTitle := args[0]
			annotationFilter := args[1]
			includes[annotationTitle] = regexp.MustCompile(strings.Trim(annotationFilter, "/"))
		}
	}

	for _, m := range matchExclude {
		args := strings.Split(m, " ")
		if len(args) > 0 {
			annotationTitle := args[0]
			annotationFilter := args[1]
			excludes[annotationTitle] = regexp.MustCompile(strings.Trim(annotationFilter, "/"))
		}
	}

	return func(a artifactspec.Descriptor) bool {
		if a.Annotations == nil {
			return len(includes) <= 0
		}

		result := true
		for k, v := range a.Annotations {
			matchFn, ok := includes[k]
			if ok {
				result = result && matchFn.MatchString(v)
			}

			matchFn, ok = excludes[k]
			if ok {
				result = result && !matchFn.MatchString(v)
			}

			// If it already should be filtered just return, otherwise continue to check all annotations
			if !result {
				return result
			}
		}

		return result
	}
}

func parse(parsing string) (reference string, host string, namespace string, locator string, err error) {
	matches := referenceRegex.FindAllStringSubmatch(parsing, -1)
	// Technically a namespace is allowed to have "/"'s, while a reference is not allowed to
	// That means if you string match the reference regex, then you should end up with basically the first segment being the host
	// the middle part being the namespace
	// and the last part should be the tag

	// This should be the case most of the time
	if len(matches[0]) == 4 {
		return matches[0][0], matches[0][1], matches[0][2], matches[0][3], nil
	}

	return "", "", "", "", errors.New("could not parse reference")
}
