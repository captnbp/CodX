package k8s

import (
	profilev1 "github.com/captnbp/CodX/api/profile/v1"
	"github.com/captnbp/CodX/internal/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testConfig() *config.Config {
	return &config.Config{
		InstanceName: "codx",
		Namespace:    "codx-system",
		CertManager: config.CertManagerConfig{
			IssuerType:  "Issuer",
			IssuerGroup: "cert-manager.io",
			IssuerName:  "codx-issuer",
			Renewal:     "720h",
			Validity:    "2160h",
		},
		Slug: config.SlugConfig{MaxLength: 63},
	}
}

func testProfile(name, title string, groups []string) *profilev1.Profile {
	return &profilev1.Profile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "codx-system"},
		Spec: profilev1.ProfileSpec{
			Title:      title,
			OIDCGroups: groups,
			PodSpec: profilev1.ProfilePodSpec{
				Image: "codercom/code-server:latest",
			},
			PVC: profilev1.ProfilePVC{Size: "10Gi"},
		},
	}
}
