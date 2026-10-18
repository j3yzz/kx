package top

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func container(cpuReq, cpuLim, memReq, memLim string) corev1.Container {
	rl := func(c, m string) corev1.ResourceList {
		l := corev1.ResourceList{}
		if c != "" {
			l[corev1.ResourceCPU] = resource.MustParse(c)
		}
		if m != "" {
			l[corev1.ResourceMemory] = resource.MustParse(m)
		}
		return l
	}
	return corev1.Container{Resources: corev1.ResourceRequirements{Requests: rl(cpuReq, memReq), Limits: rl(cpuLim, memLim)}}
}

func metrics(t *testing.T, js string) []PodMetrics {
	var l struct{ Items []PodMetrics }
	if err := json.Unmarshal([]byte(js), &l); err != nil {
		t.Fatal(err)
	}
	return l.Items
}

func TestBuildAndRender(t *testing.T) {
	pods := []corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "web-b"}, Spec: corev1.PodSpec{Containers: []corev1.Container{
			container("100m", "500m", "128Mi", "256Mi"), container("50m", "", "64Mi", "")}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "web-new"}, Spec: corev1.PodSpec{Containers: []corev1.Container{container("", "", "", "")}}},
	}
	ms := metrics(t, `{"items":[{"metadata":{"name":"web-b"},"containers":[
		{"name":"a","usage":{"cpu":"250m","memory":"100Mi"}},
		{"name":"b","usage":{"cpu":"2500000n","memory":"20971520"}}]}]}`)
	rows, err := Build(pods, ms)
	if err != nil {
		t.Fatal(err)
	}
	b := rows[0]
	if b.Pod != "web-b" || b.CPU != 253 || b.Memory != 120<<20 || b.CPURequest != 150 || b.CPULimit != 500 ||
		b.MemRequest != 192<<20 || b.MemLimit != 256<<20 {
		t.Errorf("web-b: %+v", b)
	}
	if !rows[1].NoMetrics {
		t.Error("pod without metrics should be flagged")
	}

	var out bytes.Buffer
	if err := Render(&out, rows, "table"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"253m (50%)", "120Mi (46%)", "150m", "n/a"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table lacks %q:\n%s", want, out.String())
		}
	}
}

func TestBadQuantity(t *testing.T) {
	pods := []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "p"}}}
	ms := metrics(t, `{"items":[{"metadata":{"name":"p"},"containers":[{"name":"a","usage":{"cpu":"zz","memory":"1"}}]}]}`)
	if _, err := Build(pods, ms); err == nil {
		t.Fatal("bad quantity accepted")
	}
}
