# Kubernetes Deployment Guide

Guide to deploy Goilerplate to Google Kubernetes Engine (GKE) or other Kubernetes clusters.

---

## 📋 Prerequisites

- Kubernetes cluster (GKE, EKS, AKS, or local)
- `kubectl` CLI installed and configured
- Docker image already pushed to container registry
- ConfigMap and Secret already set up

---

## 🔧 Creating ConfigMap & Secret

Before deployment, set up ConfigMap for non-sensitive values and Secret for sensitive values.

### ConfigMap

**Create ConfigMap from file:**

```bash
kubectl create configmap poc-smmf-board-config -n <namespace> \
  --from-file=config.yaml=./config/config.example.yaml \
  --dry-run=client -o yaml | kubectl apply -f -
```

**Or create YAML file first:**

```yaml
# configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: poc-smmf-board-config
  namespace: default
data:
  config.yaml: |
    app:
      env: production
      name: Goilerplate
      version: 1.0.0
    server:
      host: 0.0.0.0
      port: 3000
    db:
      driver: postgres
      host: your-db-host
      port: 5432
    redis:
      enabled: true
      host: your-redis-host:6379
```

Apply dengan:
```bash
kubectl apply -f configmap.yaml
```

---

### Secret (from .env file)

**Create Secret from env file:**

```bash
kubectl create secret generic poc-smmf-board-secret -n <namespace> \
  --from-env-file=./config/.env \
  --dry-run=client -o yaml | kubectl apply -f -
```

---

### Secret (from literal values)

If there's no `.env` file, create from literal values:

```bash
kubectl create secret generic poc-smmf-board-secret -n <namespace> \
  --from-literal=DB_HOST=your-db-host \
  --from-literal=DB_PORT=5432 \
  --from-literal=DB_NAME=poc-smmf-board \
  --from-literal=DB_USERNAME=postgres \
  --from-literal=DB_PASSWORD=your-secret-password \
  --from-literal=REDIS_HOST=your-redis-host:6379 \
  --from-literal=JWT_SECRET_KEY=your-secret-key \
  --dry-run=client -o yaml | kubectl apply -f -
```

---

## 📦 Deployment Files

Project includes deployment files for various environments:

- `deploy/k8s/deployment.dev.yaml` - Development config
- `deploy/k8s/deployment.stag.yaml` - Staging config
- `deploy/k8s/deployment.prod.yaml` - Production config

---

## 🚀 Deploy to Kubernetes

### 1. Update Image Reference

Edit deployment file and update container image:

```yaml
containers:
  - name: poc-smmf-board
    image: your-registry/poc-smmf-board:latest  # Update this
    ports:
      - containerPort: 3000
    envFrom:
      - secretRef:
          name: poc-smmf-board-secret
    volumeMounts:
      - name: config
        mountPath: /app/config
  volumes:
    - name: config
      configMap:
        name: poc-smmf-board-config
```

### 2. Apply Deployment

**Development:**
```bash
kubectl apply -f deploy/k8s/deployment.dev.yaml -n development
```

**Staging:**
```bash
kubectl apply -f deploy/k8s/deployment.stag.yaml -n staging
```

**Production:**
```bash
kubectl apply -f deploy/k8s/deployment.prod.yaml -n production
```

---

## ✅ Verify Deployment

### Check Pod Status
```bash
kubectl get pods -n <namespace>
kubectl describe pod <pod-name> -n <namespace>
kubectl logs <pod-name> -n <namespace>
```

### Port Forward (Testing)
```bash
kubectl port-forward svc/poc-smmf-board 3000:3000 -n <namespace>
curl http://localhost:3000/health
```

### Check ConfigMap & Secret
```bash
kubectl get configmap -n <namespace>
kubectl get secret -n <namespace>
kubectl describe configmap poc-smmf-board-config -n <namespace>
```

---

## 🔄 Updating Deployment

### Update Image
```bash
kubectl set image deployment/poc-smmf-board \
  poc-smmf-board=your-registry/poc-smmf-board:v1.1.0 \
  -n <namespace>
```

### Update ConfigMap
Edit and reapply:
```bash
kubectl apply -f configmap.yaml
# Restart pods to load new config
kubectl rollout restart deployment/poc-smmf-board -n <namespace>
```

### Rollback
```bash
kubectl rollout history deployment/poc-smmf-board -n <namespace>
kubectl rollout undo deployment/poc-smmf-board -n <namespace>
```

---

## 📊 Environment-Specific Configs

### Development
- Replicas: 1
- Resource requests: Low
- Image pull policy: Always (for testing)

### Staging
- Replicas: 2
- Resource requests: Medium
- Image pull policy: IfNotPresent

### Production
- Replicas: 3+
- Resource requests: High
- Image pull policy: IfNotPresent
- Health checks: Enabled
- Rolling updates: Configured

---

## 🔐 Best Practices

✅ **DO:**
- Use separate namespaces for each environment
- Store secrets in Secret, not in ConfigMap
- Use health checks (liveness & readiness probes)
- Set resource limits and requests
- Use rolling updates for zero downtime
- Monitor logs and metrics

❌ **DON'T:**
- Store secrets in ConfigMap
- Hardcode values in YAML
- Use `latest` tag in production
- Skip health checks
- Deploy without rolling strategy

---

## 🔗 Related

- [Configuration Guide](./configuration.md) - Setup environment variables
- [CI/CD Pipeline](./ci-cd.md) - Automated deployment with GitHub Actions
- [Main Deployment Directory](../../deploy/k8s)
