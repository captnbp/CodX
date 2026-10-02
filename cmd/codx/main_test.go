package main

import (
	"context"
	"testing"
	"time"

	profilev1 "github.com/captnbp/CodX/api/profile/v1"
	"github.com/captnbp/CodX/internal/config"
	"github.com/captnbp/CodX/internal/k8s"
	"github.com/captnbp/CodX/internal/k8s/fake"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testPod(name, slug, profile string, phase corev1.PodPhase, podIP string) *corev1.Pod {
	labels := map[string]string{
		k8s.LabelInstance:  slug,
		k8s.LabelManagedBy: "codx",
		k8s.LabelComponent: k8s.ComponentName,
	}
	if profile != "" {
		labels[k8s.LabelProfile] = profile
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "codx-system", Labels: labels},
		Status:     corev1.PodStatus{Phase: phase, PodIP: podIP},
	}
}

func testProfileCR(name string, delaySeconds int32) *profilev1.Profile {
	return &profilev1.Profile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "codx-system"},
		Spec: profilev1.ProfileSpec{
			Title:                      name,
			InactivityStopDelaySeconds: delaySeconds,
		},
	}
}

func TestWorkspacePodLister(t *testing.T) {
	cs := &k8s.Clientset{CoreV1: fake.NewCoreV1Client()}
	cfg := &config.Config{InstanceName: "codx", Namespace: "codx-system"}
	mgr := k8s.NewWorkspaceManager(cs, cfg)

	ctx := context.Background()
	pods := cs.CoreV1.Pods(cfg.Namespace)
	for _, p := range []*corev1.Pod{
		testPod("codx-john-doe", "john-doe", "default", corev1.PodRunning, "10.0.0.1"),
		testPod("codx-pending", "pending", "default", corev1.PodPending, ""),
		testPod("codx-noip", "noip", "default", corev1.PodRunning, ""),
	} {
		if _, err := pods.Create(ctx, p, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create pod %s: %v", p.Name, err)
		}
	}

	lister := workspacePodLister{wm: mgr}
	got, err := lister.ListWorkspacePods(ctx)
	if err != nil {
		t.Fatalf("ListWorkspacePods: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListWorkspacePods returned %d pods, want 1: %+v", len(got), got)
	}
	if got[0].Slug != "john-doe" || got[0].PodIP != "10.0.0.1" {
		t.Errorf("pod = %+v, want {Slug: john-doe, PodIP: 10.0.0.1}", got[0])
	}
}

func TestWorkspaceDelayLister(t *testing.T) {
	cs := &k8s.Clientset{CoreV1: fake.NewCoreV1Client()}
	cfg := &config.Config{InstanceName: "codx", Namespace: "codx-system"}
	mgr := k8s.NewWorkspaceManager(cs, cfg)

	store := k8s.NewProfileStore()
	store.Upsert(testProfileCR("default", 1800))
	store.Upsert(testProfileCR("never-stop", 0))

	ctx := context.Background()
	pods := cs.CoreV1.Pods(cfg.Namespace)
	for _, p := range []*corev1.Pod{
		testPod("codx-john-doe", "john-doe", "default", corev1.PodRunning, "10.0.0.1"),
		testPod("codx-jane", "jane", "never-stop", corev1.PodRunning, "10.0.0.2"),
		testPod("codx-legacy", "legacy", "", corev1.PodRunning, "10.0.0.3"),
		testPod("codx-unknown", "unknown-profile", "ghost", corev1.PodRunning, "10.0.0.4"),
		testPod("codx-bob", "bob", "default", corev1.PodPending, ""),
	} {
		if _, err := pods.Create(ctx, p, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create pod %s: %v", p.Name, err)
		}
	}

	lister := workspaceDelayLister{wm: mgr, profiles: store}
	got, err := lister.ListWorkspaceDelays(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaceDelays: %v", err)
	}

	want := map[string]time.Duration{
		"john-doe": 1800 * time.Second,
		"jane":     0, // zero delay: never stop
	}
	if len(got) != len(want) {
		t.Fatalf("ListWorkspaceDelays returned %d entries, want %d: %v", len(got), len(want), got)
	}
	for slug, wantDelay := range want {
		if got[slug] != wantDelay {
			t.Errorf("delay for %s = %v, want %v", slug, got[slug], wantDelay)
		}
	}
}
