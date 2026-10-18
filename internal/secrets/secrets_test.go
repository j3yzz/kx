package secrets

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const secretValue = "s3cr3t-value"

func testPod() *corev1.Pod {
	return &corev1.Pod{
		Spec: corev1.PodSpec{
			ImagePullSecrets: []corev1.LocalObjectReference{{Name: "regcred"}},
			Volumes: []corev1.Volume{
				{Name: "certs", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "tls"}}},
				{Name: "one", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "db",
					Items: []corev1.KeyToPath{{Key: "password", Path: "pw.txt"}}}}},
				{Name: "proj", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
					Sources: []corev1.VolumeProjection{{Secret: &corev1.SecretProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "tls"}}}}}}},
				{Name: "unmounted", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "ghost"}}},
			},
			Containers: []corev1.Container{{
				Name: "app",
				Env: []corev1.EnvVar{
					{Name: "DB_PASSWORD", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "db"}, Key: "password"}}},
					{Name: "PLAIN", Value: "x"},
					{Name: "NOPE", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "db"}, Key: "nokey"}}},
				},
				EnvFrom: []corev1.EnvFromSource{{Prefix: "API_", SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "api"}}}},
				VolumeMounts: []corev1.VolumeMount{
					{Name: "certs", MountPath: "/etc/tls"},
					{Name: "one", MountPath: "/etc/db"},
					{Name: "proj", MountPath: "/etc/proj"},
				},
			}},
		},
	}
}

func testEntries(t *testing.T) []Entry {
	secret := func(name string, data map[string]string) *corev1.Secret {
		d := map[string][]byte{}
		for k, v := range data {
			d[k] = []byte(v)
		}
		return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: name}, Data: d}
	}
	cs := fake.NewSimpleClientset(
		secret("db", map[string]string{"password": secretValue, "user": "admin"}),
		secret("api", map[string]string{"token": "tok"}),
		secret("tls", map[string]string{"tls.crt": "crt"}),
		secret("regcred", map[string]string{".dockerconfigjson": "{}"}),
	)
	e, err := Collect(context.Background(), cs, "ns", Walk(testPod()))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func find(es []Entry, secret, key, consumer string) *Entry {
	for i := range es {
		if es[i].Secret == secret && es[i].Key == key && es[i].Consumer == consumer {
			return &es[i]
		}
	}
	return nil
}

func TestWalkCollect(t *testing.T) {
	es := testEntries(t)
	for _, w := range [][3]string{
		{"db", "password", "env DB_PASSWORD"},
		{"api", "token", "env API_token"},             // envFrom with prefix
		{"tls", "tls.crt", "mount /etc/tls/tls.crt"},  // secret volume, all keys
		{"tls", "tls.crt", "mount /etc/proj/tls.crt"}, // projected
		{"db", "password", "mount /etc/db/pw.txt"},    // items path
		{"regcred", ".dockerconfigjson", "imagePullSecret"},
	} {
		if e := find(es, w[0], w[1], w[2]); e == nil || e.Missing {
			t.Errorf("missing entry %v", w)
		}
	}
	if e := find(es, "db", "nokey", "env NOPE"); e == nil || !e.Missing {
		t.Error("absent key should be marked missing")
	}
	for _, e := range es {
		if e.Secret == "ghost" {
			t.Error("unmounted volume should not be reported")
		}
	}
}

func TestMissingSecret(t *testing.T) {
	es, err := Collect(context.Background(), fake.NewSimpleClientset(), "ns",
		[]Ref{{Secret: "gone", Consumer: "env {key}"}})
	if err != nil || len(es) != 1 || !es[0].Missing {
		t.Fatalf("got %+v %v", es, err)
	}
}

// The core security rule: no value in any format unless revealed.
func TestNoLeakWithoutReveal(t *testing.T) {
	es := testEntries(t)
	b64 := base64.StdEncoding.EncodeToString([]byte(secretValue))
	for _, f := range []string{"table", "json", "env"} {
		var buf bytes.Buffer
		if err := Render(&buf, es, f, false); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		if strings.Contains(out, secretValue) || strings.Contains(out, b64) {
			t.Errorf("%s leaked value:\n%s", f, out)
		}
		if !strings.Contains(out, Mask) {
			t.Errorf("%s has no mask:\n%s", f, out)
		}
		buf.Reset()
		Render(&buf, es, f, true)
		if !strings.Contains(buf.String(), secretValue) {
			t.Errorf("%s --reveal missing value", f)
		}
	}
}

func TestEnvQuoting(t *testing.T) {
	var buf bytes.Buffer
	Render(&buf, []Entry{{Key: "K", Value: []byte("a b\n")}}, "env", true)
	if buf.String() != "K=\"a b\\n\"\n" {
		t.Fatalf("got %q", buf.String())
	}
}

func TestFilterAndSingle(t *testing.T) {
	es := testEntries(t)
	pw := FilterKey(es, "password")
	if len(pw) == 0 {
		t.Fatal("no password entries")
	}
	e, err := Single(pw)
	if err != nil || e.Key != "password" || string(e.Value) != secretValue {
		t.Fatalf("single: %v", err)
	}
	if _, err := Single(es); err == nil || strings.Contains(err.Error(), secretValue) {
		t.Fatalf("expected ambiguity error without value, got %v", err)
	}
	if _, err := Single(FilterKey(es, "zzz")); err == nil {
		t.Fatal("expected error for no match")
	}
}

func TestDiff(t *testing.T) {
	fp, err := NewFingerprinter()
	if err != nil {
		t.Fatal(err)
	}
	e := func(secret, key, val string) Entry { return Entry{Secret: secret, Key: key, Value: []byte(val)} }
	left := []Entry{e("db", "password", "same"), e("db", "user", "alice"), e("db", "host", "only-here"),
		e("db", "password", "same"), // consumed twice: compared once
		{Secret: "db", Key: "gone", Missing: true}}
	right := []Entry{e("db", "password", "same"), e("db", "user", "bob"), e("db", "extra", "x")}

	got := map[string]string{}
	for _, r := range Diff(left, right, fp) {
		got[r.Key] = r.Status
	}
	want := map[string]string{"password": Same, "user": Differs, "host": OnlyLeft, "extra": OnlyRight}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s want %s", k, got[k], v)
		}
	}
}

// Values must never reach the output, in any format.
func TestDiffNeverPrintsValues(t *testing.T) {
	fp, _ := NewFingerprinter()
	left := []Entry{{Secret: "db", Key: "k", Value: []byte(secretValue)}}
	right := []Entry{{Secret: "db", Key: "k", Value: []byte("other-" + secretValue)}}
	rows := Diff(left, right, fp)
	for _, f := range []string{"table", "json"} {
		var b bytes.Buffer
		if err := RenderDiff(&b, rows, f, "a", "b"); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(b.String(), secretValue) {
			t.Errorf("%s leaked a value:\n%s", f, b.String())
		}
	}
	if rows[0].Status != Differs || len(rows[0].Left) != 8 {
		t.Errorf("unexpected row %+v", rows[0])
	}
	// Fingerprints are per-run: another fingerprinter gives other values.
	fp2, _ := NewFingerprinter()
	if fp([]byte("x")) == fp2([]byte("x")) {
		t.Error("fingerprints should differ between runs")
	}
}

func TestDiffByKeyIgnoresSecretNames(t *testing.T) {
	fp, _ := NewFingerprinter()
	l := []Entry{{Secret: "app-env-stg", Key: "TOKEN", Value: []byte("v")}}
	r := []Entry{{Secret: "app-env-prd", Key: "TOKEN", Value: []byte("v")}}
	if rows := Diff(l, r, fp); len(rows) != 2 {
		t.Fatalf("different names should not match: %+v", rows)
	}
	l[0].Secret, r[0].Secret = "", ""
	if rows := Diff(l, r, fp); len(rows) != 1 || rows[0].Status != Same {
		t.Fatalf("by key: %+v", rows)
	}
}
