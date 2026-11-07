package oomwatch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	tokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	caPath    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	pageSize  = "500"
)

// podList is the part of the Kubernetes PodList the watcher reads.
type podList struct {
	Metadata struct {
		Continue string `json:"continue"`
	} `json:"metadata"`
	Items []pod `json:"items"`
}

type pod struct {
	Metadata struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
		UID       string `json:"uid"`
	} `json:"metadata"`
	Spec struct {
		Containers     []containerSpec `json:"containers"`
		InitContainers []containerSpec `json:"initContainers"`
	} `json:"spec"`
	Status struct {
		ContainerStatuses     []containerStatus `json:"containerStatuses"`
		InitContainerStatuses []containerStatus `json:"initContainerStatuses"`
	} `json:"status"`
}

type containerSpec struct {
	Name      string `json:"name"`
	Resources struct {
		Limits map[string]string `json:"limits"`
	} `json:"resources"`
}

type containerStatus struct {
	Name         string         `json:"name"`
	RestartCount int            `json:"restartCount"`
	State        containerState `json:"state"`
	LastState    containerState `json:"lastState"`
}

type containerState struct {
	Terminated *terminated `json:"terminated"`
}

type terminated struct {
	Reason     string    `json:"reason"`
	ExitCode   int       `json:"exitCode"`
	FinishedAt time.Time `json:"finishedAt"`
}

// kubeClient lists pods through the Kubernetes API with the pod's service
// account. It needs get and list on pods across namespaces and nothing else.
type kubeClient struct {
	base  string
	token func() (string, error)
	http  *http.Client
}

// newInClusterClient builds a client from the service account files and
// https://kubernetes.default.svc, or from base when it is set (tests, proxies).
func newInClusterClient(base string) (*kubeClient, error) {
	if base != "" {
		return &kubeClient{base: strings.TrimRight(base, "/"), token: readToken, http: &http.Client{Timeout: 30 * time.Second}}, nil
	}
	ca, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read cluster CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("cluster CA holds no certificate")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	return &kubeClient{
		base:  "https://kubernetes.default.svc",
		token: readToken,
		http:  &http.Client{Timeout: 30 * time.Second, Transport: transport},
	}, nil
}

// readToken re-reads the projected token on every request: it rotates.
func readToken() (string, error) {
	raw, err := os.ReadFile(tokenPath)
	if err != nil {
		return "", fmt.Errorf("read service account token: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// listPods returns every pod in the cluster, following the continue token.
func (c *kubeClient) listPods(ctx context.Context) ([]pod, error) {
	var out []pod
	next := ""
	for {
		q := url.Values{"limit": {pageSize}}
		if next != "" {
			q.Set("continue", next)
		}
		page, err := c.getPage(ctx, "/api/v1/pods?"+q.Encode())
		if err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		if page.Metadata.Continue == "" {
			return out, nil
		}
		next = page.Metadata.Continue
	}
}

func (c *kubeClient) getPage(ctx context.Context, path string) (*podList, error) {
	tok, err := c.token()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, fmt.Errorf("list pods: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var list podList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode pod list: %w", err)
	}
	return &list, nil
}
