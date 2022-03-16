// This example shows how to use oras go library to do some
// basic registry operations
//
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	_ "crypto/sha256"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func main() {

	env := os.Getenv("LOCAL_REGISTRY_HOSTNAME")
	if env == "" {
		env = "localhost"
	}
	var localRegistryUri = fmt.Sprintf("%s:5000", env) // Prepare URI for local registry
	var localRepoName = "example/registry"             // Prepare local repository name
	ctx := context.Background()

	// 1. Push the below oci image to registry
	layer1Desc, err := PushBlob(ctx, ocispec.MediaTypeImageLayer, []byte("Hello layer"), localRegistryUri, localRepoName, true) // push layer blob
	CheckError(err)
	configDesc, err := PushBlob(ctx, ocispec.MediaTypeImageLayer, []byte("Hello config"), localRegistryUri, localRepoName, true) // push config blob
	CheckError(err)
	manifest1Blob, err := GenerateManifestContent(configDesc, layer1Desc) // generate a image manifest
	CheckError(err)
	manifest1Desc, err := PushBlob(ctx, ocispec.MediaTypeImageManifest, manifest1Blob, localRegistryUri, localRepoName, true) // push manifest blob
	// ^ this should be done after all the layer and config blobs are pushed, otherwise will be reject by the registry
	CheckError(err)
	// After this step the local registry will look like below:
	//   +----------------------------------------+
	//   |                     +----------------+ |
	//   |                  +--> "Hello Config" | |
	//   | +-------------+  |  +--+  Config  +--+ |
	//   | |     ...     +--+                     |
	//   | ++ Manifest1 ++  |  +----------------+ |
	//   |                  +--> "Hello Layer"  | |
	//   |                     +--+  Layer1  +--+ |
	//   |                                        |
	//   +--+ localhost:5000/example/registry +---+

	// 2. Pull the pushed layer blob
	reader, err := PullBlob(ctx, layer1Desc, localRegistryUri, localRepoName, true) // Digest and media type are required for querying a blob
	CheckError(err)
	defer reader.Close() // don't forget to close io stream
	pulledBlob, err := io.ReadAll(reader)
	CheckError(err)
	fmt.Printf("Pulled layer1 => \"%v\"\n", string(pulledBlob))

	// 3. Manifest is also a blob, we can use pull blob to pull it
	reader, err = PullBlob(ctx, manifest1Desc, localRegistryUri, localRepoName, true)
	CheckError(err)
	defer reader.Close() // don't forget to close io stream
	pulledBlob, err = io.ReadAll(reader)
	CheckError(err)
	fmt.Printf("Pulled manifest => \"%v\"\n", string(pulledBlob))

	// 4. Pull a manifest blob with validation
	// TOTRY: Uncomment the below line then the call is expected to fail on size check
	// manifest1Desc.Size = 1234321
	pulledBlob, err = PullBlobSafely(ctx, manifest1Desc, localRegistryUri, localRepoName, true) // Another way to pull, will get blob directly
	CheckError(err)
	fmt.Printf("Safely pulled manifest => \"%v\"\n", string(pulledBlob))

	// 5. Give the manifest a tag and resolve it
	tagName := "tag1"
	err = TagManifest(ctx, manifest1Desc, tagName, localRegistryUri, localRepoName, true) // tag manifest1
	// get the descriptor associated with the tag
	CheckError(err)
	resolved, err := ResolveTag(ctx, tagName, localRegistryUri, localRepoName, true)
	CheckError(err)
	fmt.Printf("Resolved descriptor for tag '%s' => \"%v\"\n", tagName, resolved) // will show the manifest of tag1
	// After this step the local registry will look like below:
	//   +---------------------------------------------------+
	//   |                                +----------------+ |
	//   |                             +--> "Hello Config" | |
	//   |            +-------------+  |  +---+ Config +---+ |
	//   | (tag1) +--->     ...     +--+                     |
	//   |            ++ Manifest1 ++  |  +----------------+ |
	//   |                             +--> "Hello Layer"  | |
	//   |                                +---+ Layer1 +---+ |
	//   |                                                   |
	//   +--------+ localhost:5000/example/registry +--------+

	// // 6. Push a manifest and tag it at the same time, and resolve it
	tagName = "tag2"
	layer2Desc, err := PushBlob(ctx, ocispec.MediaTypeImageLayer, []byte("Hello layer2"), localRegistryUri, localRepoName, true) // push a new layer2 blob
	CheckError(err)
	manifest2Blob, err := GenerateManifestContent(configDesc, layer2Desc) // generate a new image manifest
	CheckError(err)
	err = PushTagManifest(ctx, tagName, manifest2Blob, localRegistryUri, localRepoName, true) // this operation will do both push and tag for the manifest
	CheckError(err)
	resolved, err = ResolveTag(ctx, tagName, localRegistryUri, localRepoName, true)
	CheckError(err)
	fmt.Printf("Resolved descriptor for tag '%s' => \"%v\"\n", tagName, resolved) // will show the manifest of tag2
	// After this step the local registry will look like below:
	//   +---------------------------------------------------+
	//   |                                +----------------+ |
	//   |                             +--> "Hello Layer2" | |
	//   |            +-------------+  |  +---+ Layer1 +---+ |
	//   | (tag2) +--->     ...     +--+                     |
	//   |            ++ Manifest2 ++  +----------+          |
	//   |                                        |          |
	//   |                                +-------v--------+ |
	//   |                             +--> "Hello Config" | |
	//   |            +-------------+  |  +---+ Config +---+ |
	//   | (tag1) +--->     ...     +--+                     |
	//   |            ++ Manifest1 ++  |  +----------------+ |
	//   |                             +--> "Hello Layer"  | |
	//   |                                +---+ Layer1 +---+ |
	//   |                                                   |
	//   +------+ localhost:5000/example/registry +----------+

	// 7. Copy a manifest from MCR to local registry
	srcRegistryUri := "mcr.microsoft.com"
	srcRepoName := "hello-world"
	srcTagName := "latest"
	targetTagName := "latest-copied"
	_, err = CopyManifest(ctx,
		srcRegistryUri, srcRepoName, srcTagName, false, // MCR uses HTTPS
		localRegistryUri, localRepoName, targetTagName, true)
	CheckError(err)
	// After this step the local registry will look like below:
	//  +----------------------------------------------------+
	//  |                                                    |
	//  |  (latest) +-> blobs copied from MCR...             |
	//  |                                                    |
	//  |                                 +----------------+ |
	//  |                              +--> "Hello Layer2" | |
	//  |             +-------------+  |  +---+ Layer1 +---+ |
	//  |  (tag2) +--->     ...     +--+                     |
	//  |             ++ Manifest2 ++  +----------+          |
	//  |                                         |          |
	//  |                                 +-------v--------+ |
	//  |                              +--> "Hello Config" | |
	//  |             +-------------+  |  +---+ Config +---+ |
	//  |  (tag1) +--->     ...     +--+                     |
	//  |             ++ Manifest1 ++  |  +----------------+ |
	//  |                              +--> "Hello Layer"  | |
	//  |                                 +---+ Layer1 +---+ |
	//  |                                                    |
	//  +-------+ localhost:5000/example/registry +----------+

	// 8. Iterate all repositories and tags in the registry
	repos, err := GetRegistryCatalog(ctx, localRegistryUri, true) // get all repositories
	CheckError(err)
	fmt.Printf("--- What's in our local registry so far ---\n")
	fmt.Printf("%s (Registry)\n", localRegistryUri)
	for _, repoName := range repos {
		fmt.Printf("  +--%s (Repository)\n", repoName)
		err = GetRepositoryTagList(ctx, localRegistryUri, localRepoName, true, func(tags []string) error { // list all the tags in the current repository
			for _, tag := range tags {
				CheckError(err)
				fmt.Printf("      +-%s\n", tag)
			}
			return nil
		})
		CheckError(err)
	}
}

func CheckError(e error) {
	if e != nil {
		panic(e)
	}
}
