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

package infra

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// PortForward creates a port-forward from a local random port to podPort in the given pod.
// Returns the local address "127.0.0.1:<port>" and a stopCh to close when done.
func PortForward(config *rest.Config, namespace, podName string, podPort int) (localAddr string, stopCh chan struct{}, errOut error) {
	transport, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return "", nil, fmt.Errorf("creating round tripper: %w", err)
	}

	host := strings.TrimLeft(config.Host, "htps:/")
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", namespace, podName)
	serverURL := &url.URL{Scheme: "https", Host: host, Path: path}
	if config.TLSClientConfig.Insecure || strings.HasPrefix(config.Host, "http://") {
		serverURL.Scheme = "http"
	}
	// Reconstruct proper URL from config.Host
	serverURL, err = url.Parse(config.Host + path)
	if err != nil {
		return "", nil, fmt.Errorf("parsing server URL: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, serverURL)

	stopCh = make(chan struct{})
	readyCh := make(chan struct{})

	// Use port 0 to get a random local port.
	ports := []string{fmt.Sprintf("0:%d", podPort)}
	fw, err := portforward.New(dialer, ports, stopCh, readyCh, nil, nil)
	if err != nil {
		return "", nil, fmt.Errorf("creating port forwarder: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- fw.ForwardPorts()
	}()

	select {
	case <-readyCh:
	case err := <-errCh:
		return "", nil, fmt.Errorf("port forwarding failed: %w", err)
	}

	forwardedPorts, err := fw.GetPorts()
	if err != nil {
		close(stopCh)
		return "", nil, fmt.Errorf("getting forwarded ports: %w", err)
	}
	if len(forwardedPorts) == 0 {
		close(stopCh)
		return "", nil, fmt.Errorf("no forwarded ports")
	}

	localAddr = fmt.Sprintf("127.0.0.1:%d", forwardedPorts[0].Local)
	return localAddr, stopCh, nil
}

// FindPodByLabel finds the first pod matching the label selector that is Running.
func FindPodByLabel(ctx context.Context, client *kubernetes.Clientset, namespace, labelSelector string) (string, error) {
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("listing pods: %w", err)
	}
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			return pod.Name, nil
		}
	}
	return "", fmt.Errorf("no running pod found with selector %q in namespace %q", labelSelector, namespace)
}
