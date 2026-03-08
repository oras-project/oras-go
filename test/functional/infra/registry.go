package infra

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

// gvkToGVR maps well-known GVKs to GVRs to avoid needing discovery.
var gvkToGVR = map[schema.GroupVersionKind]schema.GroupVersionResource{
	{Group: "", Version: "v1", Kind: "ConfigMap"}:                 {Group: "", Version: "v1", Resource: "configmaps"},
	{Group: "", Version: "v1", Kind: "Secret"}:                    {Group: "", Version: "v1", Resource: "secrets"},
	{Group: "", Version: "v1", Kind: "Service"}:                   {Group: "", Version: "v1", Resource: "services"},
	{Group: "", Version: "v1", Kind: "Namespace"}:                 {Group: "", Version: "v1", Resource: "namespaces"},
	{Group: "apps", Version: "v1", Kind: "Deployment"}:            {Group: "apps", Version: "v1", Resource: "deployments"},
	{Group: "apps", Version: "v1", Kind: "StatefulSet"}:           {Group: "apps", Version: "v1", Resource: "statefulsets"},
	{Group: "apps", Version: "v1", Kind: "DaemonSet"}:             {Group: "apps", Version: "v1", Resource: "daemonsets"},
	{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"}:  {Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
}

// ApplyManifests reads all YAML files from a directory and creates/updates resources
// using the dynamic client. Handles ConfigMap, Secret, Deployment, Service.
func ApplyManifests(ctx context.Context, config *rest.Config, namespace, dir string) error {
	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("creating dynamic client: %w", err)
	}

	// Build a REST mapper for any GVKs not in the static map.
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("creating kubernetes client: %w", err)
	}
	groupResources, err := restmapper.GetAPIGroupResources(client.Discovery())
	if err != nil {
		return fmt.Errorf("getting API group resources: %w", err)
	}
	mapper := restmapper.NewDiscoveryRESTMapper(groupResources)

	// Process files in a deterministic order: configmaps/secrets first, then deployments.
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return fmt.Errorf("globbing YAML files: %w", err)
	}

	// Sort so that configmap and secret files come before deployment files.
	sortedFiles := sortManifestFiles(files)

	for _, file := range sortedFiles {
		if err := applyFile(ctx, dynClient, mapper, namespace, file); err != nil {
			return fmt.Errorf("applying %s: %w", filepath.Base(file), err)
		}
	}
	return nil
}

// sortManifestFiles sorts files so that configmaps and secrets are applied before deployments.
func sortManifestFiles(files []string) []string {
	var configFiles, secretFiles, otherFiles []string
	for _, f := range files {
		base := filepath.Base(f)
		switch {
		case strings.Contains(base, "configmap"):
			configFiles = append(configFiles, f)
		case strings.Contains(base, "secret"):
			secretFiles = append(secretFiles, f)
		default:
			otherFiles = append(otherFiles, f)
		}
	}
	result := make([]string, 0, len(files))
	result = append(result, configFiles...)
	result = append(result, secretFiles...)
	result = append(result, otherFiles...)
	return result
}

func applyFile(ctx context.Context, dynClient dynamic.Interface, mapper meta.RESTMapper, namespace, file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	decoder := utilyaml.NewYAMLReader(reader)

	for {
		docBytes, err := decoder.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("reading YAML document: %w", err)
		}

		docBytes = []byte(strings.TrimSpace(string(docBytes)))
		if len(docBytes) == 0 || string(docBytes) == "---" {
			continue
		}

		obj := &unstructured.Unstructured{}
		dec := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(string(docBytes)), len(docBytes))
		if err := dec.Decode(obj); err != nil {
			return fmt.Errorf("decoding YAML: %w", err)
		}

		if obj.GetKind() == "" {
			continue
		}

		obj.SetNamespace(namespace)

		gvk := obj.GroupVersionKind()
		gvr, ok := gvkToGVR[gvk]
		if !ok {
			mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
			if err != nil {
				return fmt.Errorf("mapping GVK %v: %w", gvk, err)
			}
			gvr = mapping.Resource
		}

		_, err = dynClient.Resource(gvr).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("creating %s/%s: %w", gvk.Kind, obj.GetName(), err)
		}
	}
	return nil
}

// WaitForDeployment polls until the named Deployment has at least 1 ready replica.
func WaitForDeployment(ctx context.Context, client *kubernetes.Clientset, namespace, name string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		dep, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		for _, cond := range dep.Status.Conditions {
			if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
				return dep.Status.ReadyReplicas > 0, nil
			}
		}
		return false, nil
	})
}

// CreateNamespace creates the test namespace.
func CreateNamespace(ctx context.Context, client *kubernetes.Clientset, namespace string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}
	_, err := client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	return err
}

// DeleteNamespace deletes the namespace (and all resources within it).
func DeleteNamespace(ctx context.Context, client *kubernetes.Clientset, namespace string) error {
	return client.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{})
}

// dummyUsage prevents "imported and not used" errors for the runtime package.
var _ = runtime.DefaultUnstructuredConverter
