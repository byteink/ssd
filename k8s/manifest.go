package k8s

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/byteink/ssd/config"
	"gopkg.in/yaml.v3"
)

// GenerateManifests generates K8s manifests as a single multi-document YAML string.
// services: map of service name to config
// stack: full path to stack directory (namespace derived from basename)
// versions: map of service name to version number
func GenerateManifests(services map[string]*config.Config, stack string, versions map[string]int) (string, error) {
	if len(services) == 0 {
		return "", fmt.Errorf("at least one service is required")
	}

	namespace := filepath.Base(stack)
	project := namespace

	var docs []string

	// Namespace resource
	nsDoc, err := marshalResource(namespaceResource(namespace))
	if err != nil {
		return "", err
	}
	docs = append(docs, nsDoc)

	// Collect service names in sorted order for deterministic output is not needed;
	// tests use findDoc which handles any order. Iterate map directly.
	for name, cfg := range services {
		version := versions[name]

		// ConfigMap (empty — ssd manages env vars via .env files on disk,
		// kubectl create configmap --from-env-file is used at deploy time;
		// this resource ensures the ConfigMap exists so pods don't crash)
		cmDoc, err := marshalResource(configMapResource(name, namespace))
		if err != nil {
			return "", err
		}
		docs = append(docs, cmDoc)

		// Deployment
		dep, err := deploymentResource(name, namespace, project, cfg, version)
		if err != nil {
			return "", fmt.Errorf("service %q: %w", name, err)
		}
		depDoc, err := marshalResource(dep)
		if err != nil {
			return "", err
		}
		docs = append(docs, depDoc)

		// Service (always generated for DNS discovery)
		svcDoc, err := marshalResource(serviceResource(name, namespace, cfg))
		if err != nil {
			return "", err
		}
		docs = append(docs, svcDoc)

		// PVCs for volumes
		for volName := range cfg.Volumes {
			pvcDoc, err := marshalResource(pvcResource(volName, namespace))
			if err != nil {
				return "", err
			}
			docs = append(docs, pvcDoc)
		}

		// Ingress (only when domain is set)
		if cfg.PrimaryDomain() != "" {
			ingressDoc, err := marshalResource(ingressResource(name, namespace, cfg))
			if err != nil {
				return "", err
			}
			docs = append(docs, ingressDoc)
		}
	}

	return strings.Join(docs, "---\n"), nil
}

func marshalResource(resource map[string]interface{}) (string, error) {
	data, err := yaml.Marshal(resource)
	if err != nil {
		return "", fmt.Errorf("failed to marshal resource: %w", err)
	}
	return string(data), nil
}

func namespaceResource(name string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]interface{}{
			"name": name,
			"labels": map[string]interface{}{
				"managed-by": "ssd",
			},
		},
	}
}

func configMapResource(name, namespace string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]interface{}{
			"name":      name + "-env",
			"namespace": namespace,
			"labels": map[string]interface{}{
				"app":        name,
				"managed-by": "ssd",
			},
		},
	}
}

func deploymentResource(name, namespace, project string, cfg *config.Config, version int) (map[string]interface{}, error) {
	// Image
	var image, pullPolicy string
	if cfg.IsPrebuilt() {
		image = cfg.Image
		pullPolicy = "Always"
	} else {
		image = fmt.Sprintf("ssd-%s-%s:%d", project, name, version)
		pullPolicy = "Never"
	}

	// Container ports
	containerPorts := []map[string]interface{}{
		{"containerPort": cfg.Port},
	}

	// Host port mappings from cfg.Ports
	for _, mapping := range cfg.Ports {
		parts := strings.SplitN(mapping, ":", 2)
		if len(parts) != 2 {
			continue
		}
		hostPort, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		containerPort, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		containerPorts = append(containerPorts, map[string]interface{}{
			"containerPort": containerPort,
			"hostPort":      hostPort,
		})
	}

	container := map[string]interface{}{
		"name":            name,
		"image":           image,
		"imagePullPolicy": pullPolicy,
		"ports":           containerPorts,
		"envFrom": []map[string]interface{}{
			{
				"configMapRef": map[string]interface{}{
					"name": name + "-env",
				},
			},
		},
	}

	// Healthcheck probes
	if cfg.HealthCheck != nil {
		probe, err := buildProbe(cfg.HealthCheck)
		if err != nil {
			return nil, err
		}
		container["livenessProbe"] = probe
		container["readinessProbe"] = probe
	}

	// Volume mounts
	var volumeMounts []map[string]interface{}
	for volName, mountPath := range cfg.Volumes {
		volumeMounts = append(volumeMounts, map[string]interface{}{
			"name":      volName,
			"mountPath": mountPath,
		})
	}
	for localPath, containerPath := range cfg.Files {
		base := filepath.Base(localPath)
		volName := "file-" + sanitizeVolumeName(base)
		volumeMounts = append(volumeMounts, map[string]interface{}{
			"name":      volName,
			"mountPath": containerPath,
			"subPath":   base,
		})
	}
	if len(volumeMounts) > 0 {
		container["volumeMounts"] = volumeMounts
	}

	// Pod volumes
	var podVolumes []map[string]interface{}
	for volName := range cfg.Volumes {
		podVolumes = append(podVolumes, map[string]interface{}{
			"name": volName,
			"persistentVolumeClaim": map[string]interface{}{
				"claimName": volName,
			},
		})
	}
	for localPath := range cfg.Files {
		base := filepath.Base(localPath)
		volName := "file-" + sanitizeVolumeName(base)
		podVolumes = append(podVolumes, map[string]interface{}{
			"name": volName,
			"hostPath": map[string]interface{}{
				"path": filepath.Join(cfg.Stack, base),
				"type": "File",
			},
		})
	}

	podSpec := map[string]interface{}{
		"containers": []interface{}{container},
	}
	if len(podVolumes) > 0 {
		podSpec["volumes"] = podVolumes
	}

	// Strategy
	strategyType := "RollingUpdate"
	if cfg.DeployStrategy() == "recreate" {
		strategyType = "Recreate"
	}

	return map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]interface{}{
				"app":        name,
				"managed-by": "ssd",
			},
		},
		"spec": map[string]interface{}{
			"replicas": 1,
			"strategy": map[string]interface{}{
				"type": strategyType,
			},
			"selector": map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"app": name,
				},
			},
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"app": name,
					},
				},
				"spec": podSpec,
			},
		},
	}, nil
}

func serviceResource(name, namespace string, cfg *config.Config) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]interface{}{
				"app":        name,
				"managed-by": "ssd",
			},
		},
		"spec": map[string]interface{}{
			"selector": map[string]interface{}{
				"app": name,
			},
			"ports": []map[string]interface{}{
				{
					"port":       cfg.Port,
					"targetPort": cfg.Port,
					"protocol":   "TCP",
				},
			},
		},
	}
}

func pvcResource(name, namespace string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "PersistentVolumeClaim",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]interface{}{
				"managed-by": "ssd",
			},
		},
		"spec": map[string]interface{}{
			"storageClassName": "local-path",
			"accessModes":      []string{"ReadWriteOnce"},
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{
					"storage": "10Gi",
				},
			},
		},
	}
}

func ingressResource(name, namespace string, cfg *config.Config) map[string]interface{} {
	annotations := map[string]interface{}{}

	if cfg.UseHTTPS() {
		annotations["traefik.ingress.kubernetes.io/router.entrypoints"] = "websecure"
		annotations["traefik.ingress.kubernetes.io/router.tls"] = "true"
	} else {
		annotations["traefik.ingress.kubernetes.io/router.entrypoints"] = "web"
	}

	// Redirect middleware for redirect_to
	if cfg.RedirectTo != "" {
		middlewareName := namespace + "-" + name + "-redirect"
		annotations["traefik.ingress.kubernetes.io/router.middlewares"] = namespace + "-" + middlewareName + "@kubernetescrd"
	}

	// Build rules
	domains := allDomains(cfg)
	pathStr := cfg.Path
	if pathStr == "" || pathStr == "/" {
		pathStr = "/"
	}

	var rules []map[string]interface{}
	for _, domain := range domains {
		rules = append(rules, map[string]interface{}{
			"host": domain,
			"http": map[string]interface{}{
				"paths": []map[string]interface{}{
					{
						"path":     pathStr,
						"pathType": "Prefix",
						"backend": map[string]interface{}{
							"service": map[string]interface{}{
								"name": name,
								"port": map[string]interface{}{
									"number": cfg.Port,
								},
							},
						},
					},
				},
			},
		})
	}

	spec := map[string]interface{}{
		"rules": rules,
	}

	// TLS section only when HTTPS is enabled
	if cfg.UseHTTPS() {
		tlsHosts := make([]string, len(domains))
		copy(tlsHosts, domains)
		spec["tls"] = []map[string]interface{}{
			{
				"hosts":      tlsHosts,
				"secretName": name + "-tls",
			},
		}
	}

	return map[string]interface{}{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata": map[string]interface{}{
			"name":        name,
			"namespace":   namespace,
			"annotations": annotations,
			"labels": map[string]interface{}{
				"app":        name,
				"managed-by": "ssd",
			},
		},
		"spec": spec,
	}
}

// allDomains returns all domains for a config in order.
func allDomains(cfg *config.Config) []string {
	if cfg.Domain != "" {
		return []string{cfg.Domain}
	}
	if len(cfg.Domains) > 0 {
		return cfg.Domains
	}
	return nil
}

// buildProbe creates a K8s probe map from a healthcheck config.
func buildProbe(hc *config.HealthCheck) (map[string]interface{}, error) {
	probe := map[string]interface{}{
		"exec": map[string]interface{}{
			"command": []string{"sh", "-c", hc.Cmd},
		},
	}

	if hc.Interval != "" {
		seconds, err := parseDurationSeconds(hc.Interval)
		if err != nil {
			return nil, fmt.Errorf("invalid interval: %w", err)
		}
		probe["periodSeconds"] = seconds
	}

	if hc.Timeout != "" {
		seconds, err := parseDurationSeconds(hc.Timeout)
		if err != nil {
			return nil, fmt.Errorf("invalid timeout: %w", err)
		}
		probe["timeoutSeconds"] = seconds
	}

	if hc.Retries > 0 {
		probe["failureThreshold"] = hc.Retries
	}

	return probe, nil
}

// parseDurationSeconds converts a duration string like "30s", "5m", "1h" to integer seconds.
func parseDurationSeconds(d string) (int, error) {
	if len(d) < 2 {
		return 0, fmt.Errorf("invalid duration: %q", d)
	}

	unit := d[len(d)-1]
	numStr := d[:len(d)-1]

	num, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("invalid duration number: %q", d)
	}

	switch unit {
	case 's':
		return num, nil
	case 'm':
		return num * 60, nil
	case 'h':
		return num * 3600, nil
	default:
		return 0, fmt.Errorf("invalid duration unit %q in %q", string(unit), d)
	}
}

// sanitizeVolumeName converts a filename to a valid K8s volume name.
// Replaces dots and underscores with hyphens.
func sanitizeVolumeName(name string) string {
	name = strings.ReplaceAll(name, ".", "-")
	name = strings.ReplaceAll(name, "_", "-")
	return name
}
