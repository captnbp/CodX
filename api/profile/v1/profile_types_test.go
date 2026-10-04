package profilev1

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestProfileRoundTrip(t *testing.T) {
	storageClass := "fast-ssd"
	in := &Profile{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Profile",
			APIVersion: GroupName + "/" + Version,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "python-dev",
			Namespace: "codx-system",
		},
		Spec: ProfileSpec{
			Title:                       "Python Developer",
			Description:                 "A workspace with Python 3.12 and common tools.",
			OIDCGroups:                  []string{"developers", "students"},
			InactivityStopDelaySeconds:  3600,
			PodSpec: ProfilePodSpec{
				Image:   "ghcr.io/codx/python:3.12",
				Command: []string{"/usr/bin/code-server"},
				Args:    []string{"--auth", "none"},
				Env: []corev1.EnvVar{
					{Name: "PYTHONUNBUFFERED", Value: "1"},
				},
				ImagePullSecrets: []corev1.LocalObjectReference{
					{Name: "private-registry-pull"},
				},
				Volumes: []corev1.Volume{
					{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
				},
				VolumeMounts: []corev1.VolumeMount{
					{Name: "tmp", MountPath: "/tmp"},
				},
				SecurityContext: &corev1.PodSecurityContext{
					RunAsNonRoot: boolPtr(true),
					RunAsUser:    int64Ptr(1000),
				},
				Labels:      map[string]string{"tier": "dev"},
				Annotations: map[string]string{"codx.io/default": "true"},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					},
				},
			},
			PVC: ProfilePVC{
				Size:            "20Gi",
				StorageClassName: &storageClass,
				Labels:          map[string]string{"codx.io/pvc": "home"},
			},
		},
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out Profile
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if diff := cmp.Diff(in.Spec, out.Spec); diff != "" {
		t.Errorf("Spec round-trip mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(in.ObjectMeta, out.ObjectMeta); diff != "" {
		t.Errorf("ObjectMeta round-trip mismatch (-want +got):\n%s", diff)
	}
}

func TestProfileEmptyOIDCGroupsMeansAllUsers(t *testing.T) {
	p := &Profile{
		Spec: ProfileSpec{
			Title:  "Default",
			PodSpec: ProfilePodSpec{Image: "ghcr.io/codx/base:latest"},
		},
	}
	if len(p.Spec.OIDCGroups) != 0 {
		t.Fatalf("expected empty OIDCGroups, got %v", p.Spec.OIDCGroups)
	}
}

func TestDeepCopy(t *testing.T) {
	original := &Profile{
		Spec: ProfileSpec{
			Title:      "Test",
			OIDCGroups: []string{"a", "b"},
			PodSpec: ProfilePodSpec{
				Image:   "img",
				Labels:  map[string]string{"k": "v"},
				Sidecars: []corev1.Container{
					{Name: "helper", Image: "helper:1.0"},
				},
			},
		},
	}

	copy := original.DeepCopy()
	copy.Spec.Title = "Changed"
	copy.Spec.OIDCGroups[0] = "z"
	copy.Spec.PodSpec.Labels["k"] = "changed"
	copy.Spec.PodSpec.Sidecars[0].Image = "helper:2.0"

	if original.Spec.Title != "Test" {
		t.Errorf("original Title mutated: %q", original.Spec.Title)
	}
	if original.Spec.OIDCGroups[0] != "a" {
		t.Errorf("original OIDCGroups mutated: %v", original.Spec.OIDCGroups)
	}
	if original.Spec.PodSpec.Labels["k"] != "v" {
		t.Errorf("original PodSpec.Labels mutated: %v", original.Spec.PodSpec.Labels)
	}
	if original.Spec.PodSpec.Sidecars[0].Image != "helper:1.0" {
		t.Errorf("original Sidecar mutated: %v", original.Spec.PodSpec.Sidecars[0].Image)
	}
}

func TestProfileListDeepCopy(t *testing.T) {
	list := &ProfileList{
		Items: []Profile{
			{Spec: ProfileSpec{Title: "A", PodSpec: ProfilePodSpec{Image: "a"}}},
			{Spec: ProfileSpec{Title: "B", PodSpec: ProfilePodSpec{Image: "b"}}},
		},
	}
	copy := list.DeepCopy()
	copy.Items[0].Spec.Title = "X"
	if list.Items[0].Spec.Title != "A" {
		t.Errorf("original list item mutated: %q", list.Items[0].Spec.Title)
	}
}

func boolPtr(b bool) *bool       { return &b }
func int64Ptr(i int64) *int64     { return &i }
