# `Profile` CRD Reference

<!-- Generated from `profiles.codx.io` (group `codx.io`, version `v1`). Do not edit by hand; regenerate with `make docs-crd`. -->

## Overview

| Property | Value |
|----------|-------|
| **Group** | `codx.io` |
| **Version** | `v1` |
| **Scope** | `Namespaced` |
| **Kind** | `Profile` |
| **List kind** | `ProfileList` |
| **Singular** | `profile` |
| **Plural** | `profiles` |
| **Short names** | `profile`, `profiles` |
| **Served** | `True` |
| **Storage** | `True` |
| **Subresources** | `status` |

Profile represents a workspace template that admins can assign to users.

## Example

```yaml
apiVersion: codx.io/v1
kind: Profile
metadata:
  name: default
spec:
  title: Default
  description: Standard code-server workspace
  inactivityStopDelaySeconds: 1800
  oidcGroups:
    - doca:users
  podSpec:
    image: codercom/code-server:4.93.1
    resources:
      requests:
        cpu: 250m
        memory: 512Mi
  pvc:
    size: 10Gi
```

## `spec`

Spec describes the desired workspace configuration.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `description` | `string` | No | `-` | Description is a short summary shown to users in the profile picker. |
| `inactivityStopDelaySeconds` | `integer` | No | `1800` | InactivityStopDelaySeconds is the delay after which a workspace pod is stopped when no HTTPS connection is active. Zero means never stop. |
| `oidcGroups` | `[]string` | No | `-` | OIDCGroups is the list of OIDC group names allowed to use this profile. If empty, the profile is allowed for all authenticated users. |
| `podSpec` | `object` | Yes | `-` | PodSpec describes the workspace pod and its containers. |
| `pvc` | `object` | No | `-` | PVC defines the default settings for the user's home directory PVC. |
| `title` | `string` | Yes | `-` | Title is the human-readable name shown to users in the profile picker. |


### ProfilePodSpec

PodSpec describes the workspace pod and its containers.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `annotations` | `map[string]string` | No | `-` | Annotations adds extra annotations to the workspace pod. |
| `args` | `[]string` | No | `-` | Args overrides the container command arguments. |
| `command` | `[]string` | No | `-` | Command overrides the container entrypoint. |
| `enableServiceLinks` | `boolean` | No | `false` | EnableServiceLinks controls whether Kubernetes injects service-linked environment variables (e.g. *_SERVICE_HOST, *_SERVICE_PORT) into the code-server container. Defaults to false to keep the workspace environment clean and avoid leaking cluster service discovery into user workspaces. Set to true to enable the legacy injection behavior. |
| `env` | `[]object` | No | `-` | Env adds extra environment variables to the main container. See Kubernetes [`EnvVar`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) (corev1). |
| `image` | `string` | Yes | `-` | Image is the container image for the code-server main container. |
| `initContainers` | `[]object` | No | `-` | InitContainers adds extra init containers to the pod. See Kubernetes [`Container`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#container-v1-core) (corev1). |
| `labels` | `map[string]string` | No | `-` | Labels adds extra labels to the workspace pod. |
| `resources` | `object` | No | `-` | Resources sets resource requests and limits for the main container. See Kubernetes [`ResourceRequirements`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#resourcerequirements-v1-core) (corev1). |
| `securityContext` | `object` | No | `-` | SecurityContext sets the pod-level security context. See Kubernetes [`PodSecurityContext`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#podsecuritycontext-v1-core) (corev1). |
| `sidecars` | `[]object` | No | `-` | Sidecars adds extra sidecar containers to the pod (in addition to the mandatory Envoy TLS termination sidecar injected by CodX). See Kubernetes [`Container`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#container-v1-core) (corev1). |
| `volumeMounts` | `[]object` | No | `-` | VolumeMounts adds extra volume mounts to the main container. See Kubernetes [`VolumeMount`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#volumemount-v1-core) (corev1). |
| `volumes` | `[]object` | No | `-` | Volumes adds extra volumes to the pod. See Kubernetes [`Volume`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#volume-v1-core) (corev1). |


### ProfilePVC

PVC defines the default settings for the user's home directory PVC.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `annotations` | `map[string]string` | No | `-` | Annotations adds extra annotations to the PVC. |
| `labels` | `map[string]string` | No | `-` | Labels adds extra labels to the PVC. |
| `size` | `string` | No | `10Gi` | Size is the default size of the PVC (e.g. "10Gi"). |
| `storageClassName` | `string` | No | `-` | StorageClassName is the name of the StorageClass to use. If empty the cluster default storage class is used. |


## `status`

Status represents the observed state of the Profile.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `conditions` | `[]object` | No | `-` | Conditions represents the latest available observations of the Profile state. See Kubernetes [`Condition`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta) (metav1). |
| `observedGeneration` | `integer` | No | `-` | ObservedGeneration is the most recent generation observed for the Profile. |

## Notes on embedded Kubernetes types

Several `podSpec` fields embed upstream Kubernetes core types (`corev1`/`metav1`). Their full schemas are intentionally not duplicated here; refer to the linked Kubernetes API reference for the complete field list:

- **`env`** -> [`EnvVar`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core) (`corev1`)
- **`volumes`** -> [`Volume`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#volume-v1-core) (`corev1`)
- **`volumeMounts`** -> [`VolumeMount`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#volumemount-v1-core) (`corev1`)
- **`sidecars`** -> [`Container`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#container-v1-core) (`corev1`)
- **`initContainers`** -> [`Container`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#container-v1-core) (`corev1`)
- **`securityContext`** -> [`PodSecurityContext`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#podsecuritycontext-v1-core) (`corev1`)
- **`resources`** -> [`ResourceRequirements`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#resourcerequirements-v1-core) (`corev1`)
- **`conditions`** -> [`Condition`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta) (`metav1`)

## References

- Source types: [`api/profile/v1/profile_types.go`](../api/profile/v1/profile_types.go)
- CRD manifest: [`charts/codx/crds/codx.io_profiles.yaml`](../charts/codx/crds/codx.io_profiles.yaml)
- Helm template: [`charts/codx/templates/profiles.yaml`](../charts/codx/templates/profiles.yaml)
