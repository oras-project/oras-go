/*
Copyright The ORAS Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

//go:build k8sfunctional

package functional

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oras-project/oras-go/v3/test/functional/infra"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	// registryEndpoint is the local address of the anonymous Zot registry (e.g. "127.0.0.1:54321").
	registryEndpoint string

	// authRegistryEndpoint is the local address of the auth-enabled Zot registry.
	authRegistryEndpoint string
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	ctx := context.Background()

	config, err := buildKubeConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to build kubeconfig: %v\n", err)
		return 1
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create Kubernetes client: %v\n", err)
		return 1
	}

	namespace := fmt.Sprintf("oras-functional-%s", randomString(5))
	skipDeploy := os.Getenv("ORAS_FUNCTIONAL_SKIP_DEPLOY") != ""

	if skipDeploy {
		registryEndpoint = os.Getenv("ORAS_FUNCTIONAL_REGISTRY")
		authRegistryEndpoint = os.Getenv("ORAS_FUNCTIONAL_AUTH_REGISTRY")
		if registryEndpoint == "" {
			fmt.Fprintln(os.Stderr, "ORAS_FUNCTIONAL_REGISTRY must be set when ORAS_FUNCTIONAL_SKIP_DEPLOY is set")
			return 1
		}
		if authRegistryEndpoint == "" {
			fmt.Fprintln(os.Stderr, "ORAS_FUNCTIONAL_AUTH_REGISTRY must be set when ORAS_FUNCTIONAL_SKIP_DEPLOY is set")
			return 1
		}
		return m.Run()
	}

	// Create namespace.
	if err := infra.CreateNamespace(ctx, client, namespace); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create namespace %s: %v\n", namespace, err)
		return 1
	}
	defer func() {
		fmt.Printf("Cleaning up namespace %s...\n", namespace)
		if err := infra.DeleteNamespace(ctx, client, namespace); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to delete namespace %s: %v\n", namespace, err)
		}
	}()

	// Apply manifests.
	manifestDir, err := filepath.Abs("testdata/k8s")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to resolve manifest directory: %v\n", err)
		return 1
	}
	if err := infra.ApplyManifests(ctx, config, namespace, manifestDir); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to apply manifests: %v\n", err)
		return 1
	}

	// Wait for deployments.
	deployTimeout := 3 * time.Minute
	fmt.Println("Waiting for zot deployment to be ready...")
	if err := infra.WaitForDeployment(ctx, client, namespace, "zot", deployTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "Zot deployment not ready: %v\n", err)
		return 1
	}
	fmt.Println("Waiting for zot-auth deployment to be ready...")
	if err := infra.WaitForDeployment(ctx, client, namespace, "zot-auth", deployTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "Zot-auth deployment not ready: %v\n", err)
		return 1
	}

	// Port-forward to zot.
	zotPod, err := infra.FindPodByLabel(ctx, client, namespace, "app=zot")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to find zot pod: %v\n", err)
		return 1
	}
	zotAddr, zotStop, err := infra.PortForward(config, namespace, zotPod, 5000)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to port-forward to zot: %v\n", err)
		return 1
	}
	defer close(zotStop)
	registryEndpoint = zotAddr
	fmt.Printf("Zot registry available at %s\n", registryEndpoint)

	// Port-forward to zot-auth.
	authPod, err := infra.FindPodByLabel(ctx, client, namespace, "app=zot-auth")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to find zot-auth pod: %v\n", err)
		return 1
	}
	authAddr, authStop, err := infra.PortForward(config, namespace, authPod, 5001)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to port-forward to zot-auth: %v\n", err)
		return 1
	}
	defer close(authStop)
	authRegistryEndpoint = authAddr
	fmt.Printf("Zot-auth registry available at %s\n", authRegistryEndpoint)

	return m.Run()
}

func buildKubeConfig() (*rest.Config, error) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("getting home directory: %w", err)
		}
		kubeconfig = filepath.Join(home, ".kube", "config")
	}
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}
