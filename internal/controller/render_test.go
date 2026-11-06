package controller

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scmv1alpha1 "github.com/jalet/scm-metrics-exporter/api/v1alpha1"
)

func TestGithubEnvLifecycle(t *testing.T) {
	cr := &scmv1alpha1.GitHubMetricsExporter{
		ObjectMeta: metav1.ObjectMeta{Name: "gh", Namespace: "ns"},
		Spec: scmv1alpha1.GitHubMetricsExporterSpec{
			ExporterSpec: scmv1alpha1.ExporterSpec{
				Export:            scmv1alpha1.ExportConfig{OTLPEndpoint: "http://otel:4318"},
				CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
				CollectLifecycle:  true,
				ResolutionWindow:  metav1.Duration{Duration: 720 * time.Hour},
				Valkey:            &scmv1alpha1.ValkeyConfig{Endpoint: "valkey:6379"},
			},
			Org: "acme", AuthMode: "token", TokenKey: "token",
		},
	}
	env := githubEnv(cr)
	if e, ok := getEnv(env, "SCM_COLLECT_LIFECYCLE"); !ok || e.Value != "true" {
		t.Fatalf("SCM_COLLECT_LIFECYCLE = %+v, ok=%v", e, ok)
	}
	if e, ok := getEnv(env, "SCM_RESOLUTION_WINDOW"); !ok || e.Value != "720h0m0s" {
		t.Fatalf("SCM_RESOLUTION_WINDOW = %+v, ok=%v", e, ok)
	}
	if e, ok := getEnv(env, "VALKEY_ADDR"); !ok || e.Value != "valkey:6379" {
		t.Fatalf("VALKEY_ADDR = %+v, ok=%v", e, ok)
	}
}

// TestGithubEnvLifecycleValkeySecretRef covers the VALKEY_PASSWORD secretRef render path: a
// Valkey block with a SecretRef and no PasswordKey set must render VALKEY_PASSWORD sourced
// from that Secret, defaulting the key to "password".
func TestGithubEnvLifecycleValkeySecretRef(t *testing.T) {
	cr := &scmv1alpha1.GitHubMetricsExporter{
		ObjectMeta: metav1.ObjectMeta{Name: "gh", Namespace: "ns"},
		Spec: scmv1alpha1.GitHubMetricsExporterSpec{
			ExporterSpec: scmv1alpha1.ExporterSpec{
				Export:            scmv1alpha1.ExportConfig{OTLPEndpoint: "http://otel:4318"},
				CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
				CollectLifecycle:  true,
				ResolutionWindow:  metav1.Duration{Duration: 720 * time.Hour},
				Valkey: &scmv1alpha1.ValkeyConfig{
					Endpoint:  "valkey:6379",
					SecretRef: &corev1.LocalObjectReference{Name: "valkey-secret"},
				},
			},
			Org: "acme", AuthMode: "token", TokenKey: "token",
		},
	}
	env := githubEnv(cr)
	e, ok := getEnv(env, "VALKEY_PASSWORD")
	if !ok {
		t.Fatalf("VALKEY_PASSWORD not present in env: %+v", env)
	}
	if e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("VALKEY_PASSWORD.ValueFrom.SecretKeyRef = nil, want a secretKeyRef")
	}
	if e.ValueFrom.SecretKeyRef.Name != "valkey-secret" {
		t.Fatalf("VALKEY_PASSWORD secretKeyRef.Name = %q, want valkey-secret", e.ValueFrom.SecretKeyRef.Name)
	}
	if e.ValueFrom.SecretKeyRef.Key != "password" {
		t.Fatalf("VALKEY_PASSWORD secretKeyRef.Key = %q, want password (default)", e.ValueFrom.SecretKeyRef.Key)
	}
}

// TestGitlabJobExtraVolumesAndEnv covers the private-CA case: the CR's volumes, mounts and
// env reach the collection Job, and the CR's env follows the operator's so it can override.
func TestGitlabJobExtraVolumesAndEnv(t *testing.T) {
	caVolume := corev1.Volume{
		Name:         "private-ca",
		VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "root-ca"}},
	}
	caMount := corev1.VolumeMount{Name: "private-ca", MountPath: "/etc/scm/ca", ReadOnly: true}
	cr := &scmv1alpha1.GitLabMetricsExporter{
		ObjectMeta: metav1.ObjectMeta{Name: "gl", Namespace: "ns"},
		Spec: scmv1alpha1.GitLabMetricsExporterSpec{
			ExporterSpec: scmv1alpha1.ExporterSpec{
				Export:            scmv1alpha1.ExportConfig{OTLPEndpoint: "http://otel:4318"},
				CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
				Volumes:           []corev1.Volume{caVolume},
				VolumeMounts:      []corev1.VolumeMount{caMount},
				Env: []corev1.EnvVar{
					{Name: "SSL_CERT_DIR", Value: "/etc/ssl/certs:/etc/scm/ca"},
					{Name: "OTEL_METRICS_EXPORTER", Value: "none"},
				},
			},
			TargetType: "group", Group: "acme", TokenKey: "token",
		},
	}

	job := gitlabJob(cr, "img", "acme/repo")
	pod := job.Spec.Template.Spec
	if len(pod.Volumes) != 1 || pod.Volumes[0].Name != "private-ca" {
		t.Fatalf("pod volumes = %+v, want [private-ca]", pod.Volumes)
	}
	container := pod.Containers[0]
	if len(container.VolumeMounts) != 1 || container.VolumeMounts[0].MountPath != "/etc/scm/ca" {
		t.Fatalf("container mounts = %+v, want one at /etc/scm/ca", container.VolumeMounts)
	}
	if e, ok := getEnv(container.Env, "SSL_CERT_DIR"); !ok || e.Value != "/etc/ssl/certs:/etc/scm/ca" {
		t.Fatalf("SSL_CERT_DIR = %+v, ok=%v", e, ok)
	}
	if _, ok := getEnv(container.Env, "GITLAB_TOKEN"); !ok {
		t.Fatalf("GITLAB_TOKEN missing from env: %+v", container.Env)
	}
	if last := container.Env[len(container.Env)-1]; last.Name != "OTEL_METRICS_EXPORTER" || last.Value != "none" {
		t.Fatalf("last env = %+v, want the CR's OTEL_METRICS_EXPORTER override", last)
	}
}

// TestGithubJobAppModeKeepsKeyVolume checks the CR's volumes sit beside the App key volume.
func TestGithubJobAppModeKeepsKeyVolume(t *testing.T) {
	cr := &scmv1alpha1.GitHubMetricsExporter{
		ObjectMeta: metav1.ObjectMeta{Name: "gh", Namespace: "ns"},
		Spec: scmv1alpha1.GitHubMetricsExporterSpec{
			ExporterSpec: scmv1alpha1.ExporterSpec{
				Export:            scmv1alpha1.ExportConfig{OTLPEndpoint: "http://otel:4318"},
				CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
				Volumes: []corev1.Volume{{
					Name:         "private-ca",
					VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "root-ca"}}},
				}},
				VolumeMounts: []corev1.VolumeMount{{Name: "private-ca", MountPath: "/etc/scm/ca", ReadOnly: true}},
			},
			TargetType: "org", Org: "acme", AuthMode: "app",
			AppID: 1, AppInstallationID: 2, AppPrivateKeyKey: "key",
		},
	}

	pod := githubJob(cr, "img", "acme/repo").Spec.Template.Spec
	var volumes []string
	for _, v := range pod.Volumes {
		volumes = append(volumes, v.Name)
	}
	if len(volumes) != 2 || volumes[0] != appKeyVolume || volumes[1] != "private-ca" {
		t.Fatalf("pod volumes = %v, want [%s private-ca]", volumes, appKeyVolume)
	}
	if mounts := pod.Containers[0].VolumeMounts; len(mounts) != 2 {
		t.Fatalf("container mounts = %+v, want the App key and private-ca", mounts)
	}
	if len(cr.Spec.Volumes) != 1 {
		t.Fatalf("rendering mutated the CR: spec.volumes = %+v", cr.Spec.Volumes)
	}
}
