package configmaps

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func ref(n string) corev1.LocalObjectReference { return corev1.LocalObjectReference{Name: n} }

func TestWalkCollectRender(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{
		Volumes: []corev1.Volume{
			{Name: "conf", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: ref("files")}}},
			{Name: "one", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: ref("cfg"),
				Items: []corev1.KeyToPath{{Key: "LEVEL", Path: "lvl.txt"}}}}},
			{Name: "unused", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: ref("ghost")}}},
		},
		Containers: []corev1.Container{{
			Name: "app",
			Env: []corev1.EnvVar{
				{Name: "LOG", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: ref("cfg"), Key: "LEVEL"}}},
				{Name: "BAD", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: ref("cfg"), Key: "nokey"}}},
			},
			EnvFrom:      []corev1.EnvFromSource{{Prefix: "X_", ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: ref("cfg")}}},
			VolumeMounts: []corev1.VolumeMount{{Name: "conf", MountPath: "/etc/app"}, {Name: "one", MountPath: "/etc/one"}},
		}},
	}}
	cs := fake.NewSimpleClientset(
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "cfg"}, Data: map[string]string{"LEVEL": "debug"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "files"}, Data: map[string]string{"app.yaml": "a: 1\nb: 2\n"}},
	)
	es, err := Collect(context.Background(), cs, "n", Walk(pod))
	if err != nil {
		t.Fatal(err)
	}
	find := func(cm, key, consumer string) *Entry {
		for i := range es {
			if es[i].ConfigMap == cm && es[i].Key == key && es[i].Consumer == consumer {
				return &es[i]
			}
		}
		return nil
	}
	for _, w := range [][3]string{
		{"cfg", "LEVEL", "env LOG"}, {"cfg", "LEVEL", "env X_LEVEL"},
		{"cfg", "LEVEL", "mount /etc/one/lvl.txt"}, {"files", "app.yaml", "mount /etc/app/app.yaml"},
	} {
		if e := find(w[0], w[1], w[2]); e == nil || e.Missing {
			t.Errorf("missing %v", w)
		}
	}
	if e := find("cfg", "nokey", "env BAD"); e == nil || !e.Missing {
		t.Error("absent key should be missing")
	}
	for _, e := range es {
		if e.ConfigMap == "ghost" {
			t.Error("unmounted volume reported")
		}
	}

	var b bytes.Buffer
	if err := Render(&b, es, "table"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "debug") || !strings.Contains(b.String(), "a: 1 …") || strings.Contains(b.String(), "b: 2") {
		t.Errorf("values shown unmasked, multi-line collapsed:\n%s", b.String())
	}
}
