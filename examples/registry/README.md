This example shows how to use oras go library to do some basic registry operations, including:

- Push and pull blobs and manifests (step 1, 2, 3, 4);
- Create a tag for the pushed manifest, and resolve the created tag (step 5);
- Push a manifest and create a tag for it at the same time (step 6);
- Copy a image manifest from Microsoft Container Registry (step 7);
- List repositries and tags in the registry.

# Prerequisites
To run this example, you will need

1) Go 1.17 install.
2) A local registry service running at port 5000. Use the below command to start a container one if you have docker installed. 
    ```
    docker run -d -p 5000:5000 registry
    ```