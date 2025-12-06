# Kubernetes Controllers

## 📌 Architecture Overview
This project contains two Kubernetes controllers, both built using Kubebuilder:

### 1. DeploymentWatcher Controller
Responsible for monitoring Kubernetes Deployment objects and detecting:
- Creation
- Update
- Deletion

When any of these events occur, it creates a SlackMessage CR containing the formatted message describing the event.

**Automatic Namespace Creation**
If the namespace specified in the environment variable `SLACK_MESSAGES_NAMESPACE` does not exist, the controller automatically creates it before creating any SlackMessage resources.

### 2. SlackMessage Controller
This controller watches SlackMessage custom resources.

For each resource:
- Reads Slack token and channel from environment variables
- Sends the message to Slack using `slack-go/slack`
- Updates `.status` (sent, error, sentAt) after completion

## 📌 Custom Resource Definition: SlackMessage
Example of the SlackMessage CRD YAML:

```yaml
apiVersion: notifications.aymenkoched/v1alpha1
kind: SlackMessage
metadata:
  labels:
    app.kubernetes.io/name: kubernetes-controller-interview
    app.kubernetes.io/managed-by: kustomize
  name: slackmessage-sample
spec:
  text: "Hello, World!"
  deploymentRef:
    name: frontend
    namespace: production
```

## 📌 Event Types Handled
The DeploymentWatcher controller detects several types of Deployment changes and produces appropriate Slack messages.

### 1. Deployment Created
`"🟢 Deployment *%s* created in namespace *%s*\nReplicas: %d"`

### 2. Deployment Deleted
`"🚨 Deployment *%s* in namespace *%s* was *deleted*."`

### 3. Deployment Updated
Update cases include:

**Replica count changed**
`"📏 Scaled *%s* in namespace *%s*:\n%d → %d Replicas"`

**Image updated**
`"🖼️ Image updated for *%s* in namespace *%s*: %s → %s"`

**Labels updated**
`"🏷️ Labels updated for *%s* in namespace *%s*"`

**Annotations updated**
`"📝 Annotations updated for *%s* in namespace *%s*"`

**Fallback**
`"📦 Deployment *%s* updated in namespace *%s*"`

## 📌 Internal Caching Logic
The DeploymentWatcher controller maintains an internal in-memory cache to compare past Deployment states with current ones.

This cache enables the controller to determine what changed between reconciliations.

### Controller Structure
```go
type DeploymentWatcherReconciler struct {
    client.Client
    Scheme *runtime.Scheme
    Cache  map[string]*appsv1.Deployment
}
```

### Custom Constructor
```go
func NewDeploymentWatcherReconciler(c client.Client, s *runtime.Scheme) *DeploymentWatcherReconciler {
    return &DeploymentWatcherReconciler{
        Client: c,
        Scheme: s,
        Cache:  map[string]*appsv1.Deployment{},
    }
}
```

### Why this is needed
- Kubernetes does not keep historical Deployment specs.
- Each reconciliation only provides the current state.
- To detect meaningful differences (scaling, image updates, etc), you need the previous state.

The internal cache stores Deployments by key:
`"<namespace>/<name>" → previous Deployment object"`

### Where it is used
In `main.go`:
```go
if err := controller.NewDeploymentWatcherReconciler(
    mgr.GetClient(),
    mgr.GetScheme(),
).SetupWithManager(mgr); err != nil {
    os.Exit(1)
}
```

This ensures the controller starts with its own cache instance.

## 📌 Security & Credentials Management
Security was a core requirement:
- No Slack credentials are hardcoded in source code.

### 1. .env File for Local/Dev Configuration
Located in `config/default/.env`:
```
slack-token=your-slack-token
slack-channel=your-slack-channel
slack-messages-namespace=your-namespace
```

### 2. Automatically creating a Secret with Kustomize
Using `secretGenerator` in `kustomization.yaml`:
```yaml
secretGenerator:
  - name: slack-config
    options:
      disableNameSuffixHash: true
    envs:
      - .env
```

This generates a Kubernetes Secret:
```yaml
kind: Secret
name: slack-config
data:
  slack-token: <base64>
  slack-channel: <base64>
  slack-messages-namespace: <base64>
```

### 3. Injecting secrets into controller manager pod
In the `/manager` deployment manifest:
```yaml
env:
  - name: SLACK_TOKEN
    valueFrom:
      secretKeyRef:
        name: kci-slack-config
        key: slack-token

  - name: SLACK_CHANNEL
    valueFrom:
      secretKeyRef:
        name: kci-slack-config
        key: slack-channel

  - name: SLACK_MESSAGES_NAMESPACE
    valueFrom:
      secretKeyRef:
        name: kci-slack-config
        key: slack-messages-namespace
```

### 4. Reading them inside controllers
**DeploymentWatcher controller**:
```go
slackMsgNamespace := os.Getenv("SLACK_MESSAGES_NAMESPACE")
```

**SlackMessage controller**:
```go
token := os.Getenv("SLACK_TOKEN")
channel := os.Getenv("SLACK_CHANNEL")
```

> ➡️ This guarantees no credentials ever touch the Go source code.

## 📌 Roles & Permissions (RBAC)
To allow controllers to:
- Watch Deployments
- Create SlackMessage CRs
- Update their status
- Create namespaces if missing

The following RBAC rules were added:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: manager-role
rules:
- apiGroups:
  - notifications.aymenkoched
  resources:
  - slackmessages
  verbs: [create, delete, get, list, patch, update, watch]

- apiGroups:
  - notifications.aymenkoched
  resources:
  - slackmessages/finalizers
  verbs: [update]

- apiGroups:
  - notifications.aymenkoched
  resources:
  - slackmessages/status
  verbs: [get, patch, update]

- apiGroups:
  - apps
  resources:
    - deployments
  verbs: [get, list, watch]

- apiGroups: [""]
  resources:
    - namespaces
  verbs: [get, list, watch, create]
```