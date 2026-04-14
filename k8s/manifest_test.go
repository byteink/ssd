package k8s

import (
	"strings"
	"testing"

	"github.com/byteink/ssd/config"
	"gopkg.in/yaml.v3"
)

// parseMultiDoc splits a multi-document YAML string and returns all parsed docs.
func parseMultiDoc(t *testing.T, raw string) []map[string]interface{} {
	t.Helper()
	parts := strings.Split(raw, "---\n")
	var docs []map[string]interface{}
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		var doc map[string]interface{}
		if err := yaml.Unmarshal([]byte(trimmed), &doc); err != nil {
			t.Fatalf("invalid YAML doc: %v\n%s", err, trimmed)
		}
		docs = append(docs, doc)
	}
	return docs
}

// findDoc returns the first document matching kind (and optionally name in metadata.name).
func findDoc(docs []map[string]interface{}, kind, name string) map[string]interface{} {
	for _, doc := range docs {
		if doc["kind"] != kind {
			continue
		}
		if name == "" {
			return doc
		}
		meta, _ := doc["metadata"].(map[string]interface{})
		if meta["name"] == name {
			return doc
		}
	}
	return nil
}

func TestGenerateManifests_SingleService(t *testing.T) {
	services := map[string]*config.Config{
		"web": {
			Name:   "web",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Port:   80,
		},
	}

	result, err := GenerateManifests(services, "/stacks/myapp", map[string]int{"web": 1})
	if err != nil {
		t.Fatalf("GenerateManifests failed: %v", err)
	}

	docs := parseMultiDoc(t, result)

	// Should have: Namespace, ConfigMap, Deployment, Service
	ns := findDoc(docs, "Namespace", "myapp")
	if ns == nil {
		t.Fatal("Namespace resource missing")
	}

	dep := findDoc(docs, "Deployment", "web")
	if dep == nil {
		t.Fatal("Deployment resource missing")
	}

	// Check metadata
	meta := dep["metadata"].(map[string]interface{})
	if meta["namespace"] != "myapp" {
		t.Errorf("namespace = %v, want myapp", meta["namespace"])
	}
	labels := meta["labels"].(map[string]interface{})
	if labels["app"] != "web" {
		t.Errorf("label app = %v, want web", labels["app"])
	}
	if labels["managed-by"] != "ssd" {
		t.Errorf("label managed-by = %v, want ssd", labels["managed-by"])
	}

	// Check spec
	spec := dep["spec"].(map[string]interface{})
	if spec["replicas"] != 1 {
		t.Errorf("replicas = %v, want 1", spec["replicas"])
	}

	// Check container
	podSpec := spec["template"].(map[string]interface{})["spec"].(map[string]interface{})
	containers := podSpec["containers"].([]interface{})
	if len(containers) != 1 {
		t.Fatalf("containers count = %d, want 1", len(containers))
	}
	container := containers[0].(map[string]interface{})
	if container["name"] != "web" {
		t.Errorf("container name = %v, want web", container["name"])
	}
	if container["image"] != "ssd-myapp-web:1" {
		t.Errorf("image = %v, want ssd-myapp-web:1", container["image"])
	}
	if container["imagePullPolicy"] != "Never" {
		t.Errorf("imagePullPolicy = %v, want Never", container["imagePullPolicy"])
	}

	// Check container port
	ports := container["ports"].([]interface{})
	if len(ports) != 1 {
		t.Fatalf("ports count = %d, want 1", len(ports))
	}
	port := ports[0].(map[string]interface{})
	if port["containerPort"] != 80 {
		t.Errorf("containerPort = %v, want 80", port["containerPort"])
	}

	// Check envFrom
	envFrom := container["envFrom"].([]interface{})
	if len(envFrom) != 1 {
		t.Fatalf("envFrom count = %d, want 1", len(envFrom))
	}
	cmRef := envFrom[0].(map[string]interface{})["configMapRef"].(map[string]interface{})
	if cmRef["name"] != "web-env" {
		t.Errorf("configMapRef name = %v, want web-env", cmRef["name"])
	}

	// Check ConfigMap exists (referenced by Deployment envFrom)
	cm := findDoc(docs, "ConfigMap", "web-env")
	if cm == nil {
		t.Fatal("ConfigMap resource missing")
	}
	cmMeta := cm["metadata"].(map[string]interface{})
	if cmMeta["namespace"] != "myapp" {
		t.Errorf("ConfigMap namespace = %v, want myapp", cmMeta["namespace"])
	}
	cmLabels := cmMeta["labels"].(map[string]interface{})
	if cmLabels["app"] != "web" {
		t.Errorf("ConfigMap label app = %v, want web", cmLabels["app"])
	}
	if cmLabels["managed-by"] != "ssd" {
		t.Errorf("ConfigMap label managed-by = %v, want ssd", cmLabels["managed-by"])
	}

	// Check Service exists
	svc := findDoc(docs, "Service", "web")
	if svc == nil {
		t.Fatal("Service resource missing")
	}
	svcSpec := svc["spec"].(map[string]interface{})
	selector := svcSpec["selector"].(map[string]interface{})
	if selector["app"] != "web" {
		t.Errorf("service selector app = %v, want web", selector["app"])
	}
	svcPorts := svcSpec["ports"].([]interface{})
	svcPort := svcPorts[0].(map[string]interface{})
	if svcPort["port"] != 80 {
		t.Errorf("service port = %v, want 80", svcPort["port"])
	}

	// No Ingress (no domain)
	ingress := findDoc(docs, "Ingress", "web")
	if ingress != nil {
		t.Error("Ingress should not exist when no domain is set")
	}
}

func TestGenerateManifests_WithDomain(t *testing.T) {
	services := map[string]*config.Config{
		"web": {
			Name:   "web",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Domain: "example.com",
			Port:   3000,
		},
	}

	result, err := GenerateManifests(services, "/stacks/myapp", map[string]int{"web": 1})
	if err != nil {
		t.Fatalf("GenerateManifests failed: %v", err)
	}

	docs := parseMultiDoc(t, result)

	ingress := findDoc(docs, "Ingress", "web")
	if ingress == nil {
		t.Fatal("Ingress resource missing when domain is set")
	}

	// Check apiVersion
	if ingress["apiVersion"] != "networking.k8s.io/v1" {
		t.Errorf("apiVersion = %v, want networking.k8s.io/v1", ingress["apiVersion"])
	}

	// Check annotations for Traefik
	meta := ingress["metadata"].(map[string]interface{})
	annotations := meta["annotations"].(map[string]interface{})
	if annotations["traefik.ingress.kubernetes.io/router.entrypoints"] != "websecure" {
		t.Errorf("entrypoints annotation = %v, want websecure", annotations["traefik.ingress.kubernetes.io/router.entrypoints"])
	}
	if annotations["traefik.ingress.kubernetes.io/router.tls"] != "true" {
		t.Errorf("tls annotation = %v, want true", annotations["traefik.ingress.kubernetes.io/router.tls"])
	}

	// Check rules
	spec := ingress["spec"].(map[string]interface{})
	rules := spec["rules"].([]interface{})
	if len(rules) != 1 {
		t.Fatalf("rules count = %d, want 1", len(rules))
	}
	rule := rules[0].(map[string]interface{})
	if rule["host"] != "example.com" {
		t.Errorf("host = %v, want example.com", rule["host"])
	}

	// Check path
	httpPaths := rule["http"].(map[string]interface{})["paths"].([]interface{})
	path := httpPaths[0].(map[string]interface{})
	if path["pathType"] != "Prefix" {
		t.Errorf("pathType = %v, want Prefix", path["pathType"])
	}
	if path["path"] != "/" {
		t.Errorf("path = %v, want /", path["path"])
	}
	backend := path["backend"].(map[string]interface{})["service"].(map[string]interface{})
	if backend["name"] != "web" {
		t.Errorf("backend service name = %v, want web", backend["name"])
	}
	backendPort := backend["port"].(map[string]interface{})
	if backendPort["number"] != 3000 {
		t.Errorf("backend port = %v, want 3000", backendPort["number"])
	}

	// Check TLS
	tls := spec["tls"].([]interface{})
	if len(tls) != 1 {
		t.Fatalf("tls count = %d, want 1", len(tls))
	}
	tlsEntry := tls[0].(map[string]interface{})
	hosts := tlsEntry["hosts"].([]interface{})
	if hosts[0] != "example.com" {
		t.Errorf("tls host = %v, want example.com", hosts[0])
	}
	if tlsEntry["secretName"] != "web-tls" {
		t.Errorf("secretName = %v, want web-tls", tlsEntry["secretName"])
	}
}

func TestGenerateManifests_WithoutDomain(t *testing.T) {
	services := map[string]*config.Config{
		"worker": {
			Name:   "worker",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Port:   80,
		},
	}

	result, err := GenerateManifests(services, "/stacks/myapp", map[string]int{"worker": 1})
	if err != nil {
		t.Fatalf("GenerateManifests failed: %v", err)
	}

	docs := parseMultiDoc(t, result)

	ingress := findDoc(docs, "Ingress", "worker")
	if ingress != nil {
		t.Error("Ingress should not exist when no domain is set")
	}

	// Deployment and Service should still exist
	dep := findDoc(docs, "Deployment", "worker")
	if dep == nil {
		t.Fatal("Deployment missing")
	}
	svc := findDoc(docs, "Service", "worker")
	if svc == nil {
		t.Fatal("Service missing")
	}
}

func TestGenerateManifests_WithHealthcheck(t *testing.T) {
	services := map[string]*config.Config{
		"web": {
			Name:   "web",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Port:   3000,
			HealthCheck: &config.HealthCheck{
				Cmd:      "curl -f http://localhost:3000/health || exit 1",
				Interval: "30s",
				Timeout:  "10s",
				Retries:  3,
			},
		},
	}

	result, err := GenerateManifests(services, "/stacks/myapp", map[string]int{"web": 1})
	if err != nil {
		t.Fatalf("GenerateManifests failed: %v", err)
	}

	docs := parseMultiDoc(t, result)
	dep := findDoc(docs, "Deployment", "web")
	if dep == nil {
		t.Fatal("Deployment missing")
	}

	spec := dep["spec"].(map[string]interface{})
	podSpec := spec["template"].(map[string]interface{})["spec"].(map[string]interface{})
	container := podSpec["containers"].([]interface{})[0].(map[string]interface{})

	// Check livenessProbe
	liveness := container["livenessProbe"].(map[string]interface{})
	execCmd := liveness["exec"].(map[string]interface{})["command"].([]interface{})
	if len(execCmd) != 3 {
		t.Fatalf("exec command length = %d, want 3", len(execCmd))
	}
	if execCmd[0] != "sh" || execCmd[1] != "-c" {
		t.Errorf("exec command prefix = %v %v, want sh -c", execCmd[0], execCmd[1])
	}
	if execCmd[2] != "curl -f http://localhost:3000/health || exit 1" {
		t.Errorf("exec command = %v", execCmd[2])
	}
	if liveness["periodSeconds"] != 30 {
		t.Errorf("periodSeconds = %v, want 30", liveness["periodSeconds"])
	}
	if liveness["timeoutSeconds"] != 10 {
		t.Errorf("timeoutSeconds = %v, want 10", liveness["timeoutSeconds"])
	}
	if liveness["failureThreshold"] != 3 {
		t.Errorf("failureThreshold = %v, want 3", liveness["failureThreshold"])
	}

	// Check readinessProbe (same as liveness)
	readiness := container["readinessProbe"].(map[string]interface{})
	readinessExec := readiness["exec"].(map[string]interface{})["command"].([]interface{})
	if readinessExec[2] != "curl -f http://localhost:3000/health || exit 1" {
		t.Errorf("readiness exec command = %v", readinessExec[2])
	}
	if readiness["periodSeconds"] != 30 {
		t.Errorf("readiness periodSeconds = %v, want 30", readiness["periodSeconds"])
	}
}

func TestGenerateManifests_WithVolumes(t *testing.T) {
	services := map[string]*config.Config{
		"postgres": {
			Name:   "postgres",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Image:  "postgres:16-alpine",
			Port:   5432,
			Volumes: map[string]string{
				"postgres-data": "/var/lib/postgresql/data",
			},
		},
	}

	result, err := GenerateManifests(services, "/stacks/myapp", map[string]int{"postgres": 1})
	if err != nil {
		t.Fatalf("GenerateManifests failed: %v", err)
	}

	docs := parseMultiDoc(t, result)

	// Check PVC exists
	pvc := findDoc(docs, "PersistentVolumeClaim", "postgres-data")
	if pvc == nil {
		t.Fatal("PersistentVolumeClaim missing")
	}
	pvcSpec := pvc["spec"].(map[string]interface{})
	if pvcSpec["storageClassName"] != "local-path" {
		t.Errorf("storageClassName = %v, want local-path", pvcSpec["storageClassName"])
	}
	accessModes := pvcSpec["accessModes"].([]interface{})
	if accessModes[0] != "ReadWriteOnce" {
		t.Errorf("accessMode = %v, want ReadWriteOnce", accessModes[0])
	}
	resources := pvcSpec["resources"].(map[string]interface{})
	requests := resources["requests"].(map[string]interface{})
	if requests["storage"] != "10Gi" {
		t.Errorf("storage = %v, want 10Gi", requests["storage"])
	}

	// Check volume mount in deployment
	dep := findDoc(docs, "Deployment", "postgres")
	if dep == nil {
		t.Fatal("Deployment missing")
	}
	spec := dep["spec"].(map[string]interface{})
	podSpec := spec["template"].(map[string]interface{})["spec"].(map[string]interface{})
	container := podSpec["containers"].([]interface{})[0].(map[string]interface{})

	volumeMounts := container["volumeMounts"].([]interface{})
	found := false
	for _, vm := range volumeMounts {
		mount := vm.(map[string]interface{})
		if mount["name"] == "postgres-data" && mount["mountPath"] == "/var/lib/postgresql/data" {
			found = true
		}
	}
	if !found {
		t.Error("volume mount for postgres-data not found")
	}

	// Check volumes in pod spec
	volumes := podSpec["volumes"].([]interface{})
	foundVol := false
	for _, v := range volumes {
		vol := v.(map[string]interface{})
		if vol["name"] == "postgres-data" {
			pvcClaim := vol["persistentVolumeClaim"].(map[string]interface{})
			if pvcClaim["claimName"] != "postgres-data" {
				t.Errorf("claimName = %v, want postgres-data", pvcClaim["claimName"])
			}
			foundVol = true
		}
	}
	if !foundVol {
		t.Error("volume definition for postgres-data not found in pod spec")
	}
}

func TestGenerateManifests_PrebuiltImage(t *testing.T) {
	services := map[string]*config.Config{
		"nginx": {
			Name:   "nginx",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Image:  "nginx:latest",
			Port:   80,
		},
	}

	result, err := GenerateManifests(services, "/stacks/myapp", map[string]int{"nginx": 1})
	if err != nil {
		t.Fatalf("GenerateManifests failed: %v", err)
	}

	docs := parseMultiDoc(t, result)
	dep := findDoc(docs, "Deployment", "nginx")
	if dep == nil {
		t.Fatal("Deployment missing")
	}

	spec := dep["spec"].(map[string]interface{})
	podSpec := spec["template"].(map[string]interface{})["spec"].(map[string]interface{})
	container := podSpec["containers"].([]interface{})[0].(map[string]interface{})

	if container["image"] != "nginx:latest" {
		t.Errorf("image = %v, want nginx:latest", container["image"])
	}
	if container["imagePullPolicy"] != "Always" {
		t.Errorf("imagePullPolicy = %v, want Always", container["imagePullPolicy"])
	}
}

func TestGenerateManifests_MultiService(t *testing.T) {
	services := map[string]*config.Config{
		"web": {
			Name:   "web",
			Server: "myserver",
			Stack:  "/stacks/myproject",
			Port:   80,
		},
		"api": {
			Name:   "api",
			Server: "myserver",
			Stack:  "/stacks/myproject",
			Port:   3000,
		},
	}

	result, err := GenerateManifests(services, "/stacks/myproject", map[string]int{"web": 5, "api": 3})
	if err != nil {
		t.Fatalf("GenerateManifests failed: %v", err)
	}

	docs := parseMultiDoc(t, result)

	// One namespace
	ns := findDoc(docs, "Namespace", "myproject")
	if ns == nil {
		t.Fatal("Namespace missing")
	}

	// Two deployments
	webDep := findDoc(docs, "Deployment", "web")
	if webDep == nil {
		t.Fatal("web Deployment missing")
	}
	apiDep := findDoc(docs, "Deployment", "api")
	if apiDep == nil {
		t.Fatal("api Deployment missing")
	}

	// Check images
	webContainer := webDep["spec"].(map[string]interface{})["template"].(map[string]interface{})["spec"].(map[string]interface{})["containers"].([]interface{})[0].(map[string]interface{})
	if webContainer["image"] != "ssd-myproject-web:5" {
		t.Errorf("web image = %v, want ssd-myproject-web:5", webContainer["image"])
	}
	apiContainer := apiDep["spec"].(map[string]interface{})["template"].(map[string]interface{})["spec"].(map[string]interface{})["containers"].([]interface{})[0].(map[string]interface{})
	if apiContainer["image"] != "ssd-myproject-api:3" {
		t.Errorf("api image = %v, want ssd-myproject-api:3", apiContainer["image"])
	}

	// Two services
	webSvc := findDoc(docs, "Service", "web")
	if webSvc == nil {
		t.Fatal("web Service missing")
	}
	apiSvc := findDoc(docs, "Service", "api")
	if apiSvc == nil {
		t.Fatal("api Service missing")
	}
}

func TestGenerateManifests_DeployStrategy(t *testing.T) {
	tests := []struct {
		name     string
		strategy string
		want     string
	}{
		{"rollout", "rollout", "RollingUpdate"},
		{"recreate", "recreate", "Recreate"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			services := map[string]*config.Config{
				"web": {
					Name:   "web",
					Server: "myserver",
					Stack:  "/stacks/myapp",
					Port:   80,
					Deploy: &config.DeployConfig{Strategy: tt.strategy},
				},
			}

			result, err := GenerateManifests(services, "/stacks/myapp", map[string]int{"web": 1})
			if err != nil {
				t.Fatalf("GenerateManifests failed: %v", err)
			}

			docs := parseMultiDoc(t, result)
			dep := findDoc(docs, "Deployment", "web")
			if dep == nil {
				t.Fatal("Deployment missing")
			}

			spec := dep["spec"].(map[string]interface{})
			strategy := spec["strategy"].(map[string]interface{})
			if strategy["type"] != tt.want {
				t.Errorf("strategy type = %v, want %v", strategy["type"], tt.want)
			}
		})
	}
}

func TestGenerateManifests_WithFiles(t *testing.T) {
	services := map[string]*config.Config{
		"api": {
			Name:   "api",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Port:   8080,
			Files: map[string]string{
				"./config.yaml": "/app/config.yaml",
			},
		},
	}

	result, err := GenerateManifests(services, "/stacks/myapp", map[string]int{"api": 1})
	if err != nil {
		t.Fatalf("GenerateManifests failed: %v", err)
	}

	docs := parseMultiDoc(t, result)
	dep := findDoc(docs, "Deployment", "api")
	if dep == nil {
		t.Fatal("Deployment missing")
	}

	spec := dep["spec"].(map[string]interface{})
	podSpec := spec["template"].(map[string]interface{})["spec"].(map[string]interface{})
	container := podSpec["containers"].([]interface{})[0].(map[string]interface{})

	// Check volume mount
	volumeMounts := container["volumeMounts"].([]interface{})
	found := false
	for _, vm := range volumeMounts {
		mount := vm.(map[string]interface{})
		if mount["name"] == "file-config-yaml" && mount["mountPath"] == "/app/config.yaml" {
			if mount["subPath"] != "config.yaml" {
				t.Errorf("subPath = %v, want config.yaml", mount["subPath"])
			}
			found = true
		}
	}
	if !found {
		t.Error("volume mount for file-config-yaml not found")
	}

	// Check hostPath volume
	volumes := podSpec["volumes"].([]interface{})
	foundVol := false
	for _, v := range volumes {
		vol := v.(map[string]interface{})
		if vol["name"] == "file-config-yaml" {
			hp := vol["hostPath"].(map[string]interface{})
			if hp["path"] != "/stacks/myapp/config.yaml" {
				t.Errorf("hostPath = %v, want /stacks/myapp/config.yaml", hp["path"])
			}
			if hp["type"] != "File" {
				t.Errorf("hostPath type = %v, want File", hp["type"])
			}
			foundVol = true
		}
	}
	if !foundVol {
		t.Error("hostPath volume for file-config-yaml not found")
	}
}

func TestGenerateManifests_WithPorts(t *testing.T) {
	services := map[string]*config.Config{
		"app": {
			Name:   "app",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Port:   80,
			Ports:  []string{"3000:3000", "8080:80"},
		},
	}

	result, err := GenerateManifests(services, "/stacks/myapp", map[string]int{"app": 1})
	if err != nil {
		t.Fatalf("GenerateManifests failed: %v", err)
	}

	docs := parseMultiDoc(t, result)
	dep := findDoc(docs, "Deployment", "app")
	if dep == nil {
		t.Fatal("Deployment missing")
	}

	spec := dep["spec"].(map[string]interface{})
	podSpec := spec["template"].(map[string]interface{})["spec"].(map[string]interface{})
	container := podSpec["containers"].([]interface{})[0].(map[string]interface{})

	ports := container["ports"].([]interface{})
	// Should have: containerPort 80 (from cfg.Port) + hostPort mappings
	// The cfg.Port is always present; hostPort entries add extra ports
	foundHostPort3000 := false
	foundHostPort8080 := false
	for _, p := range ports {
		port := p.(map[string]interface{})
		cp, _ := port["containerPort"].(int)
		hp, hasHP := port["hostPort"].(int)
		if hasHP && cp == 3000 && hp == 3000 {
			foundHostPort3000 = true
		}
		if hasHP && cp == 80 && hp == 8080 {
			foundHostPort8080 = true
		}
	}
	if !foundHostPort3000 {
		t.Error("hostPort mapping 3000:3000 not found")
	}
	if !foundHostPort8080 {
		t.Error("hostPort mapping 8080:80 not found")
	}
}

func TestGenerateManifests_MultiDomain(t *testing.T) {
	services := map[string]*config.Config{
		"web": {
			Name:   "web",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Domains: []string{
				"example.com",
				"www.example.com",
				"api.example.com",
			},
			Port: 3000,
		},
	}

	result, err := GenerateManifests(services, "/stacks/myapp", map[string]int{"web": 1})
	if err != nil {
		t.Fatalf("GenerateManifests failed: %v", err)
	}

	docs := parseMultiDoc(t, result)
	ingress := findDoc(docs, "Ingress", "web")
	if ingress == nil {
		t.Fatal("Ingress missing")
	}

	spec := ingress["spec"].(map[string]interface{})
	rules := spec["rules"].([]interface{})
	if len(rules) != 3 {
		t.Fatalf("rules count = %d, want 3", len(rules))
	}

	expectedHosts := []string{"example.com", "www.example.com", "api.example.com"}
	for i, r := range rules {
		rule := r.(map[string]interface{})
		if rule["host"] != expectedHosts[i] {
			t.Errorf("rule[%d] host = %v, want %v", i, rule["host"], expectedHosts[i])
		}
	}

	// Check TLS hosts
	tls := spec["tls"].([]interface{})
	tlsEntry := tls[0].(map[string]interface{})
	hosts := tlsEntry["hosts"].([]interface{})
	if len(hosts) != 3 {
		t.Fatalf("tls hosts count = %d, want 3", len(hosts))
	}
}

func TestGenerateManifests_WithRedirectTo(t *testing.T) {
	services := map[string]*config.Config{
		"web": {
			Name:   "web",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Domains: []string{
				"example.com",
				"www.example.com",
			},
			RedirectTo: "example.com",
			Port:       3000,
		},
	}

	result, err := GenerateManifests(services, "/stacks/myapp", map[string]int{"web": 1})
	if err != nil {
		t.Fatalf("GenerateManifests failed: %v", err)
	}

	docs := parseMultiDoc(t, result)
	ingress := findDoc(docs, "Ingress", "web")
	if ingress == nil {
		t.Fatal("Ingress missing")
	}

	meta := ingress["metadata"].(map[string]interface{})
	annotations := meta["annotations"].(map[string]interface{})

	// Should have redirect middleware annotation
	middlewareKey := "traefik.ingress.kubernetes.io/router.middlewares"
	middleware, ok := annotations[middlewareKey]
	if !ok {
		t.Fatal("redirect middleware annotation missing")
	}
	middlewareStr := middleware.(string)
	if !strings.Contains(middlewareStr, "redirect") {
		t.Errorf("middleware annotation = %v, want to contain 'redirect'", middlewareStr)
	}
}

func TestGenerateManifests_HTTPSDisabled(t *testing.T) {
	falseVal := false
	services := map[string]*config.Config{
		"web": {
			Name:   "web",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Domain: "example.com",
			HTTPS:  &falseVal,
			Port:   8080,
		},
	}

	result, err := GenerateManifests(services, "/stacks/myapp", map[string]int{"web": 1})
	if err != nil {
		t.Fatalf("GenerateManifests failed: %v", err)
	}

	docs := parseMultiDoc(t, result)
	ingress := findDoc(docs, "Ingress", "web")
	if ingress == nil {
		t.Fatal("Ingress missing")
	}

	meta := ingress["metadata"].(map[string]interface{})
	annotations := meta["annotations"].(map[string]interface{})

	// Should use web entrypoint, not websecure
	if annotations["traefik.ingress.kubernetes.io/router.entrypoints"] != "web" {
		t.Errorf("entrypoints = %v, want web", annotations["traefik.ingress.kubernetes.io/router.entrypoints"])
	}

	// Should NOT have tls annotation
	if _, ok := annotations["traefik.ingress.kubernetes.io/router.tls"]; ok {
		t.Error("tls annotation should not exist when HTTPS is disabled")
	}

	// Should NOT have tls section
	spec := ingress["spec"].(map[string]interface{})
	if _, ok := spec["tls"]; ok {
		t.Error("tls section should not exist when HTTPS is disabled")
	}
}

func TestGenerateManifests_EmptyServices(t *testing.T) {
	_, err := GenerateManifests(map[string]*config.Config{}, "/stacks/myapp", map[string]int{})
	if err == nil {
		t.Fatal("expected error for empty services")
	}
	if !strings.Contains(err.Error(), "at least one service") {
		t.Errorf("error = %v, want 'at least one service'", err)
	}
}

func TestParseDurationSeconds(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"30s", 30},
		{"10s", 10},
		{"1m", 60},
		{"5m", 300},
		{"1h", 3600},
		{"2h", 7200},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseDurationSeconds(tt.input)
			if err != nil {
				t.Fatalf("parseDurationSeconds(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("parseDurationSeconds(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseDurationSeconds_Invalid(t *testing.T) {
	invalid := []string{"", "30", "abc", "30x"}
	for _, input := range invalid {
		t.Run(input, func(t *testing.T) {
			_, err := parseDurationSeconds(input)
			if err == nil {
				t.Errorf("parseDurationSeconds(%q) expected error", input)
			}
		})
	}
}

func TestGenerateManifests_Replicas(t *testing.T) {
	n := 3
	services := map[string]*config.Config{
		"web": {
			Name:   "web",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Port:   80,
			Deploy: &config.DeployConfig{Strategy: "rollout", Replicas: &n},
		},
	}
	result, err := GenerateManifests(services, "/stacks/myapp", map[string]int{"web": 1})
	if err != nil {
		t.Fatalf("GenerateManifests failed: %v", err)
	}
	docs := parseMultiDoc(t, result)
	dep := findDoc(docs, "Deployment", "web")
	if dep == nil {
		t.Fatal("deployment missing")
	}
	spec := dep["spec"].(map[string]interface{})
	if spec["replicas"] != 3 {
		t.Errorf("replicas = %v, want 3", spec["replicas"])
	}
}

func TestGenerateManifests_ReplicasDefaultsToOne(t *testing.T) {
	services := map[string]*config.Config{
		"web": {Name: "web", Stack: "/stacks/myapp", Port: 80},
	}
	result, _ := GenerateManifests(services, "/stacks/myapp", map[string]int{"web": 1})
	docs := parseMultiDoc(t, result)
	dep := findDoc(docs, "Deployment", "web")
	spec := dep["spec"].(map[string]interface{})
	if spec["replicas"] != 1 {
		t.Errorf("replicas = %v, want 1", spec["replicas"])
	}
}
