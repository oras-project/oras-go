# Targets in ORAS Go v2

Prerequisite reading: [Modeling Artifact](./Artifacts-Model.md)

In ORAS Go v2, artifacts are modeled as [Directed Acyclic Graphs (DAGs)](https://en.wikipedia.org/wiki/Directed_acyclic_graph) stored in [Content-Addressable Storages (CASs)](https://en.wikipedia.org/wiki/Content-addressable_storage). Each node in the graph represents their [descriptors](https://github.com/opencontainers/image-spec/blob/v1.1.0/descriptor.md).

A descriptor should at least contains the following three required properties:

- `mediaType`: the media type of the referenced content
- `digest`: the digest of the targeted content
- `size`: the size, in bytes, of the raw content

Here is an example of the descriptor of an image manifest:

```json
{
  "mediaType": "application/vnd.oci.image.manifest.v1+json",
  "size": 7682,
  "digest": "sha256:5b0bcabd1ed22e9fb1310cf6c2dec7cdef19f0ad69efa1f392e94a4333501270"
}
```

## Interfaces

Based on the concepts of graph modeling and descriptors, the following mayjor interfaces are defined in ORAS Go v2.

### Storage

The `Storage` interface represents a content-addressable storage (CAS) where contents are accessed via their descriptors, it provides the following functions:

- `Fetch`: fetches the content identified by the descriptor from the CAS.
- `Exists`: checks if the described content exists in the CAS or not.
- `Push`: pushes the content matching the expected descriptor to the CAS.

Suppose there is such a graph stored in a `Storage`, where the name of each node is the alias of their descriptors:

```mermaid
graph TD;

M0["Manifest m0"]--config-->Blob0["Blob b0"]
M0--layers-->Blob1["Blob b1"]
M0--layers-->Blob2["Blob b2"]
```

The effects of the `Fetch` and `Exists` functions would be like this:

```
Fetch(m0) == content_m0

Exists(b0) == true
Exists(b3) == false
```

If a new blob `b3` is pushed to the storage, the graph would become:

```mermaid
graph TD;

M0["Manifest m0"]--config-->Blob0["Blob b0"]
M0--layers-->Blob1["Blob b1"]
M0--layers-->Blob2["Blob b2"]
Blob3["Blob b3"]
```

#### GraphStorage

The `GraphStorage` interface represents a CAS with support of predecessors finding. It provides the following functions:

- `Fetch`
- `Exists`
- `Push`
- **`Prdecessors`**: finds out the nodes directly pointing to a given node in the graph.

The effects of the `Predecessors` function called against the same graph would be like this:

```
Predecessors(b0) == [m0]
Predecessors(m0) == []
```

### Target

The `Target` interface represents a CAS with tagging capability. It provides the following functions:

- `Fetch`
- `Exists`
- `Push`
- **`Resolve`**: resolves a tag string to a descriptor.
- **`Tag`**: tags a descriptor with a tag string.

Suppose there is such a graph stored in a `Target`, where `m0` is associated with two tags `"foo"` and `"bar"`:

```mermaid
graph TD;

M0["Manifest m0"]--config-->Blob0["Blob b0"]
M0--layers-->Blob1["Blob b1"]
M0--layers-->Blob2["Blob b2"]

TagFoo>"Tag: foo"]-.->M0
TagBar>"Tag: bar"]-.->M0
```

The effects of the `Resolve` function would be like this:

```
Resolve("foo") == m0
Resolve("bar") == m0
Resolve("hello") == nil
```

If a new tag "v1" is tagged on `m0`, the graph would become:

```mermaid
graph TD;

M0["Manifest m0"]--config-->Blob0["Blob b0"]
M0--layers-->Blob1["Blob b1"]
M0--layers-->Blob2["Blob b2"]

TagFoo>"Tag: foo"]-.->M0
TagBar>"Tag: bar"]-.->M0
TagV1>"Tag: v1"]-.->M0
```

### GraphTarget

The `GraphTarget` interface represents a CAS with tagging capability and supports predecessors finding. It provides the following functions:

- `Fetch`
- `Exists`
- `Push`
- `Resolve`
- `Tag`
- `Predecessors`

## Content Stores

In ORAS Go v2, a content store is an implementation of `Target`, more specifically, `GraphTarget`.

There are four built-in content stores defined in the library, they are:

- Memory Store: An in-memory implementation
- OCI Store: Stores content in format of OCI-Image layout on file system
- File Store: Stores location-addressable content on file system
- Repository Store: Represents a remote artifact repository (e.g. `ghcr.io`, `docker.io`, etc.)

### Memory Store

The memory store is available at the `content/memory` package, it stores everything in memory where blob content are mapped to their descriptor.

One common scenario for using a memory store is to build and store an artifact in the memory store first, and then later copy it as a whole to other stores, such as remote repositories.

### OCI Store

The OCI store is available at the `content/oci` package, it follows the [`OCI image-spec v1.1.0`](https://github.com/opencontainers/image-spec/blob/v1.1.0/image-layout.md) to stores the blob contents on file system.

Suppose there is an artifact and its signature, it can be represented in the graph below:

```mermaid
graph TD;

SignatureManifest["Signature Manifest<br>(sha256:e5727b...)"]--subject-->Manifest
SignatureManifest--config-->Config
SignatureManifest--layers-->SignatureBlob["Signature blob<br>(sha256:37f884)"]

Manifest["Manifest<br>(sha256:314c7f...)"]--config-->Config["Config blob<br>(sha256:44136f...)"]
Manifest--layers-->Layer0["Layer blob 0<br>(sha256:b5bb9d...)"]
Manifest--layers-->Layer1["Layer blob 1<br>(sha256:7d865e...)"]
```

The directory structure for the graph on the file system would look like this:

```bash
$ tree repo
repo/
├── blobs
│   └── sha256
│       ├── 314c7f20dd44ee1cca06af399a67f7c463a9f586830d630802d9e365933da9fb
│       ├── 37f88486592fd90ace303ee38f8d1ff698193e76c76d3c1fef8627a39e677696
│       ├── 44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a
│       ├── 7d865e959b2466918c9863afca942d0fb89d7c9ac0c99bafc3749504ded97730
│       ├── b5bb9d8014a0f9b1d61e21e796d78dccdf1352f23cd32812f4850b878ae4944c
│       └── e5727bebbcbbd9996446c34622ca96af67a54219edd58d261112f1af06e2537c
├── index.json
├── ingest
└── oci-layout
```

In the layout,

- All content, no mater of manifests or layer blobs, are all placed in the `blobs` directory, where the path to the content is the digest of the content.
- `index.json` is an Image Index JSON object, it serves as an entry point of the graph and a tagging system.
- `ingest` is a tempoprary directory for ingesting the blobs during processing, it not defined in the spec and is ORAS-specific.
- `oci-layout` is a marker of the base of the OCI Layout.

The OCI Layout has several advantages:

- It is `OCI image-spec v1.1.0` compliant and is compatible with other tools besides ORAS
- Its clean structure makes it easy to be managed and replicated

Based on these advantages, the OCI Store can be used as a local copy of a remote repository.

### File Store

The file store is available at the `content/file` package, it not only provides Content-Addressable Storage(CAS) but also supports location-addressed capability.

The purpose of the file store is for packaging arbitary files. It supports adding blobs from the local file system. When a blob is added, a descriptor is generated for the blob with a `"org.opencontainers.image.title"` annotation indicating the name of the blob.

For example, suppose there are two files `foo.txt` and `bar.txt` on the disk:

```bash
$ ls
foo.txt bar.txt

$ cat foo.txt
foo
```

After being added to the file store, the descriptor for the `foo.txt` file blob looks like:

```json
{
  "mediaType": "application/vnd.custom.type",
  "digest": "sha256:b5bb9d8014a0f9b1d61e21e796d78dccdf1352f23cd32812f4850b878ae4944c",
  "size": 4,
  "annotations": {
    "org.opencontainers.image.title": "foo.txt"
  }
}
```

A manifest needs to be created to reference the two file blobs and will be stored in the file store as well. The manifest content looks like:

```json
{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.manifest.v1+json",
  "artifactType": "application/vnd.example+type",
  "config": {
    "mediaType": "application/vnd.oci.empty.v1+json",
    "digest": "sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
    "size": 2,
    "data": "e30="
  },
  "layers": [
    {
      "mediaType": "application/vnd.custom.type",
      "digest": "sha256:b5bb9d8014a0f9b1d61e21e796d78dccdf1352f23cd32812f4850b878ae4944c",
      "size": 4,
      "annotations": {
        "org.opencontainers.image.title": "foo.txt"
      }
    },
    {
      "mediaType": "application/vnd.custom.type",
      "digest": "sha256:7d865e959b2466918c9863afca942d0fb89d7c9ac0c99bafc3749504ded97730",
      "size": 4,
      "annotations": {
        "org.opencontainers.image.title": "bar.txt"
      }
    }
  ],
  "annotations": {
    "org.opencontainers.image.created": "2025-01-23T10:57:27Z"
  }
}
```

In the file store, the blobs described by names are location-addressed
by file paths. Other contents that are not described by names (usually manifests and config blobs) are stored in a fallback storage. The fallback storage can be any CAS implementing the `Storage` interface, such as OCI Layout storage. But the default fallback storage is a limited memory CAS.

For the above example, the graph stored in the file store would be like this:

```mermaid
graph TD;

Manifest["Manifest<br>(in memory)"]--config-->Config["Config blob<br>(in memory)"]
Manifest--layers-->Layer0["foo.txt<br>(on disk)"]
Manifest--layers-->Layer1["bar.txt<br>(on disk)"]
```

Unlike the OCI store, only named contents are persisted in the file store and all metadata are one-off. Once terminated, the file store cannot be restored to the original state from the file system.

### Repository Store

The repository store is available at the `registry/remote` package, it implements APIs defined in the [OCI distribution-spec v1.1.0](https://github.com/opencontainers/distribution-spec/blob/v1.1.0/spec.md) to communicate to the remote repositories.

Unlike other content stores mentioned above, the repository store handles manifests and non-manifest blobs seperately. This is because the URI paths for manifests and blobs go through `/v2/<name>/manifests/` and `/v2/<name>/blobs/`, respectively.

The repository store manages manifests via the `ManifestStore` sub-store and handles blobs via the `BlobStore` sub-store. It is able to automatically determine which sub-store to use based on the media type specified in the descriptor.

Here is a simplified list of mappings between repository store functions and registry APIs:

| Funciton Name | Registry API  |
| ------------- | ------------- |
| `Repository.Manifests().Resolve()` | HEAD `/v2/<name>/manifests/<reference>` |
| `Repository.Blobs().Resolve()` | HEAD `/v2/<name>/blobs/<reference>` |
| `Repository.Manifests().Fetch()` | GET `/v2/<name>/manifests/<reference>` |
| `Repository.Blobs().Fetch()` | GET `/v2/<name>/blobs/<reference>` |

### Summary

// TODO: refine

// TODO: add links
