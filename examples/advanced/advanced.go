package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"oras.land/oras-go/pkg/content"
	"oras.land/oras-go/pkg/oras"
	"oras.land/oras-go/pkg/target"
)

func main() {
	var verbose int
	cmd := &cobra.Command{
		Use:          fmt.Sprintf("%s [command]", os.Args[0]),
		SilenceUsage: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			log.SetLevel(log.InfoLevel)
			if verbose > 1 {
				log.SetLevel(log.DebugLevel)
			}
		},
	}
	cmd.AddCommand(copyCmd())
	cmd.PersistentFlags().IntVarP(&verbose, "verbose", "v", 1, "set log level")
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func copyCmd() *cobra.Command {
	var (
		fromStr, toStr string
		manifestConfig string
		opts           content.RegistryOptions
	)
	cmd := &cobra.Command{
		Use:   "copy <name:tag|name@digest>",
		Short: "Copy artifacts from one location to another",
		Long: `Copy artifacts from one location to another
Example - Copy artifacts from local files to local files:
  oras copy foo/bar:v1 --from files --to files:path/to/save file1 file2 ... filen
Example - Copy artifacts from registry to local files:
  oras copy foo/bar:v1 --from registry --to files:path/to/save
Example - Copy artifacts from registry to oci:
  oras copy foo/bar:v1 --from registry --to oci:path/to/oci
Example - Copy artifacts from local files to registry:
  oras copy foo/bar:v1 --from files --to registry file1 file2 ... filen

When the source (--from) is "files", the config by default will be "{}" and of media type
application/vnd.unknown.config.v1+json. You can override it by setting the path, for example:

  oras copy foo/bar:v1 --from files --manifest-config path/to/config:application/vnd.oci.image.config.v1+json --to files:path/to/save file1 file2 ... filen


`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				ref      = args[0]
				err      error
				from, to target.Target
			)
			// get the fromStr; it might also have a ':' to add options
			fromParts := strings.SplitN(fromStr, ":", 2)
			toParts := strings.SplitN(toStr, ":", 2)
			switch fromParts[0] {
			case "files":
				fromFile := content.NewFile("")
				descs, err := loadFiles(fromFile, args[1:]...)
				if err != nil {
					return fmt.Errorf("unable to load files: %v", err)
				}
				// parse the manifest config
				manifestConfigParts := strings.SplitN(manifestConfig, ":", 2)
				var manifestConfigPath, manifestConfigMediaType string
				switch len(manifestConfigParts) {
				case 1:
					manifestConfigPath = manifestConfigParts[0]
				case 2:
					manifestConfigPath = manifestConfigParts[0]
					manifestConfigMediaType = manifestConfigParts[1]
				}
				if err != nil {
					return fmt.Errorf("error reading manifest config at %s: %v", manifestConfigPath, err)
				}
				configDesc, err := fromFile.Add("", manifestConfigMediaType, manifestConfigPath)
				if err != nil {
					return fmt.Errorf("unable to load manifest config: %v", err)
				}
				if _, err := fromFile.GenerateManifest(ref, &configDesc, descs...); err != nil {
					return fmt.Errorf("unable to generate root manifest: %s", err)
				}
				rootDesc, rootManifest, err := fromFile.Ref(ref)
				if err != nil {
					return err
				}
				log.Debugf("root manifest: %s %v %s", ref, rootDesc, rootManifest)
				from = fromFile
			case "registry":
				from, err = content.NewRegistry(opts)
				if err != nil {
					return fmt.Errorf("could not create registry target: %v", err)
				}
			case "oci":
				from, err = content.NewOCI(fromParts[1])
				if err != nil {
					return fmt.Errorf("could not read OCI layout at %s: %v", fromParts[1], err)
				}
			default:
				return fmt.Errorf("unknown from argyment: %s", from)
			}

			switch toParts[0] {
			case "files":
				to = content.NewFile(toParts[1])
			case "registry":
				to, err = content.NewRegistry(opts)
				if err != nil {
					return fmt.Errorf("could not create registry target: %v", err)
				}
			case "oci":
				to, err = content.NewOCI(toParts[1])
				if err != nil {
					return fmt.Errorf("could not read OCI layout at %s: %v", toParts[1], err)
				}
			default:
				return fmt.Errorf("unknown from argyment: %s", from)
			}

			if manifestConfig != "" && fromParts[0] != "files" {
				return fmt.Errorf("only specify --manifest-config when using --from files")
			}
			return runCopy(ref, from, to)
		},
	}
	cmd.Flags().StringVar(&fromStr, "from", "", "source type and possible options")
	cmd.MarkFlagRequired("from")
	cmd.Flags().StringVar(&toStr, "to", "", "destination type and possible options")
	cmd.MarkFlagRequired("to")
	cmd.Flags().StringArrayVarP(&opts.Configs, "config", "c", nil, "auth config path")
	cmd.Flags().StringVarP(&opts.Username, "username", "u", "", "registry username")
	cmd.Flags().StringVarP(&opts.Password, "password", "p", "", "registry password")
	cmd.Flags().BoolVarP(&opts.Insecure, "insecure", "", false, "allow connections to SSL registry without certs")
	cmd.Flags().BoolVarP(&opts.PlainHTTP, "plain-http", "", false, "use plain http and not https")
	cmd.Flags().StringVar(&manifestConfig, "manifest-config", "", "path to manifest config and its media type, e.g. path/to/file.json:application/vnd.oci.image.config.v1+json")
	return cmd
}

func runCopy(ref string, from, to target.Target) error {
	desc, err := oras.Copy(context.Background(), from, ref, to, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v", err)
		os.Exit(1)
	}
	fmt.Printf("%#v\n", desc)
	return nil
}

func loadFiles(store *content.File, files ...string) ([]ocispec.Descriptor, error) {
	var descs []ocispec.Descriptor
	for _, fileRef := range files {
		filename, mediaType := parseFileRef(fileRef, "")
		name := filepath.Clean(filename)
		if !filepath.IsAbs(name) {
			// convert to slash-separated path unless it is absolute path
			name = filepath.ToSlash(name)
		}
		desc, err := store.Add(name, mediaType, filename)
		if err != nil {
			return nil, err
		}
		descs = append(descs, desc)
	}
	return descs, nil
}
