//go:build integration

// Integration tests: drive the real kx binary against the kind cluster set up
// by hack/kind-test.sh. They delete pods, so they refuse to run unless
// KX_TEST_KUBECONFIG points at a kind- context.
package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/j3yzz/kx/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	kctx   = "kind-kx-test"
	ns     = "kx-test"
	secret = "s3cr3t-integration"
)

var kx, kubeconfig string

func TestMain(m *testing.M) {
	kubeconfig = os.Getenv("KX_TEST_KUBECONFIG")
	if kubeconfig == "" {
		fmt.Fprintln(os.Stderr, "set KX_TEST_KUBECONFIG (use hack/kind-test.sh)")
		os.Exit(2)
	}
	c, err := kube.New(kube.Options{Kubeconfig: kubeconfig, Context: kctx})
	if err != nil || !strings.HasPrefix(c.Context, "kind-") {
		fmt.Fprintf(os.Stderr, "refusing to run: %s is not a kind- context (%v)\n", kctx, err)
		os.Exit(2)
	}

	dir, err := os.MkdirTemp("", "kx-it")
	if err != nil {
		panic(err)
	}
	kx = filepath.Join(dir, "kx")
	build := exec.Command("go", "build", "-o", kx, "github.com/j3yzz/kx")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n%s", err, out)
		os.Exit(2)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func kxCmd(env []string, args ...string) *exec.Cmd {
	full := append([]string{"--kubeconfig", kubeconfig, "--context", kctx, "-n", ns}, args...)
	cmd := exec.Command(kx, full...)
	cmd.Env = append(os.Environ(), env...)
	return cmd
}

// run executes kx to completion with stdin as given (so never a TTY).
func run(t *testing.T, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return runEnv(t, nil, stdin, args...)
}

func runEnv(t *testing.T, env []string, stdin string, args ...string) (string, string, int) {
	t.Helper()
	cmd := kxCmd(env, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errb.String(), code
}

func TestExecExitCodeAndOutput(t *testing.T) {
	if _, _, code := run(t, "", "exec", "alpine", "--", "sh", "-c", "exit 7"); code != 7 {
		t.Errorf("exit code %d, want 7", code)
	}
	out, _, code := run(t, "", "exec", "alpine", "--", "echo", "hi")
	if code != 0 || strings.TrimSpace(out) != "hi" {
		t.Errorf("got %q code %d", out, code)
	}
}

// Acceptance: opens a shell with no pod name, and falls back to sh without bash.
func TestExecShellFallback(t *testing.T) {
	for app, want := range map[string]string{"alpine": "sh", "bashy": "bash"} {
		out, _, code := run(t, "echo $0\n", "exec", app)
		if code != 0 || strings.TrimSpace(out) != want {
			t.Errorf("%s: shell %q code %d, want %q", app, out, code, want)
		}
	}
}

func TestAppNotFound(t *testing.T) {
	_, errOut, code := run(t, "", "exec", "does-not-exist")
	if code != 2 || !strings.Contains(errOut, "not found") {
		t.Errorf("code %d stderr %q", code, errOut)
	}
}

// Core security rule: no value without --reveal, in any format.
func TestSecretsMaskedUnlessRevealed(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte(secret))
	cases := [][]string{
		{"secrets", "alpine"},
		{"-o", "json", "secrets", "alpine"},
		{"-o", "env", "secrets", "alpine"},
		{"secrets", "get", "app-secret"},
	}
	for _, args := range cases {
		out, errOut, code := run(t, "", args...)
		all := out + errOut
		if code != 0 || strings.Contains(all, secret) || strings.Contains(all, b64) {
			t.Errorf("%v: leaked or failed (code %d):\n%s", args, code, all)
		}
		if !strings.Contains(out, "********") {
			t.Errorf("%v: no mask:\n%s", args, out)
		}
		revealed := append(append([]string{}, args...), "--reveal")
		if out, _, _ := run(t, "", revealed...); !strings.Contains(out, secret) {
			t.Errorf("%v: --reveal did not show value", args)
		}
	}
}

// Acceptance: tell which Secret and key an app uses, and where.
func TestSecretsShowConsumers(t *testing.T) {
	out, _, _ := run(t, "", "secrets", "alpine")
	for _, want := range []string{"app-secret", "password", "env DB_PASSWORD", "mount /etc/creds/password"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestProdGuard(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".config", "kx")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("prodContexts: [\"kind-*\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home}

	_, errOut, code := runEnv(t, env, "", "exec", "alpine", "--", "echo", "hi")
	if code == 0 || !strings.Contains(errOut, "--yes") {
		t.Errorf("prod exec not refused: code %d %q", code, errOut)
	}
	if _, errOut, code := runEnv(t, env, "", "secrets", "alpine", "--reveal"); code == 0 {
		t.Errorf("prod reveal not refused: %q", errOut)
	}
	if out, _, code := runEnv(t, env, "", "secrets", "alpine"); code != 0 || strings.Contains(out, secret) {
		t.Errorf("masked secrets should not need confirmation: code %d", code)
	}
	out, _, code := runEnv(t, env, "", "exec", "--yes", "alpine", "--", "echo", "hi")
	if code != 0 || strings.TrimSpace(out) != "hi" {
		t.Errorf("--yes: %q code %d", out, code)
	}
}

// startForward runs kx forward and streams its stdout/stderr lines.
func startForward(t *testing.T, args ...string) (lines <-chan string, stop func()) {
	t.Helper()
	cmd := kxCmd(nil, append([]string{"forward"}, args...)...)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ch := make(chan string, 100)
	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			t.Log("kx: " + sc.Text())
			ch <- sc.Text()
		}
		close(ch)
	}()
	return ch, func() {
		_ = cmd.Process.Signal(syscall.SIGINT)
		done := make(chan struct{})
		go func() { cmd.Wait(); pw.Close(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			cmd.Process.Kill()
		}
	}
}

var mapping = regexp.MustCompile(`^localhost:(\d+) -> web/(\S+):80$`)

func waitMapping(t *testing.T, lines <-chan string, notPod string, timeout time.Duration) (port, pod string) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatal("kx forward exited")
			}
			if m := mapping.FindStringSubmatch(l); m != nil && m[2] != notPod {
				return m[1], m[2]
			}
		case <-deadline:
			t.Fatalf("no tunnel line (not pod %q) within %s", notPod, timeout)
		}
	}
}

func httpOK(port string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://127.0.0.1:" + port + "/")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return true
			}
		}
		time.Sleep(time.Second)
	}
	return false
}

// Acceptance: deleting the pod during a session re-establishes the tunnel
// with no user action. Same local port, new pod.
func TestForwardRecoversFromPodDelete(t *testing.T) {
	lines, stop := startForward(t, "web", "0:80")
	defer stop()

	port, pod := waitMapping(t, lines, "", 90*time.Second)
	if !httpOK(port, 30*time.Second) {
		t.Fatal("tunnel never served 200")
	}

	c, err := kube.New(kube.Options{Kubeconfig: kubeconfig, Context: kctx, Namespace: ns})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CoreV1().Pods(ns).Delete(context.Background(), pod, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}

	port2, pod2 := waitMapping(t, lines, pod, 180*time.Second)
	if port2 != port {
		t.Errorf("local port changed %s -> %s", port, port2)
	}
	if !httpOK(port2, 60*time.Second) {
		t.Fatalf("tunnel to new pod %s never served 200", pod2)
	}
}

func TestForwardBusyLocalPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	busy := fmt.Sprint(l.Addr().(*net.TCPAddr).Port)

	lines, stop := startForward(t, "web", busy+":80")
	defer stop()
	port, _ := waitMapping(t, lines, "", 90*time.Second)
	if port == busy {
		t.Fatal("reused the busy port")
	}
	if !httpOK(port, 30*time.Second) {
		t.Fatal("replacement port did not serve 200")
	}
}

func TestForwardDefaultPort(t *testing.T) {
	lines, stop := startForward(t, "web") // only port 80 declared
	defer stop()
	port, _ := waitMapping(t, lines, "", 90*time.Second)
	if !httpOK(port, 30*time.Second) {
		t.Fatal("default port did not serve 200")
	}
}

// home returns an env with HOME pointing at a dir holding the given kx config.
func home(t *testing.T, cfg string) []string {
	t.Helper()
	h := t.TempDir()
	dir := filepath.Join(h, ".config", "kx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{"HOME=" + h}
}

// runNoNS is run without the default -n, to test namespace defaults.
func runNoNS(t *testing.T, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(kx, append([]string{"--kubeconfig", kubeconfig, "--context", kctx}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errb.String(), code
}

func TestAliasUsesAppAndNamespace(t *testing.T) {
	env := home(t, "aliases:\n  al:\n    app: alpine\n    namespace: kx-test\n")
	out, _, code := runNoNS(t, env, "exec", "al", "--", "echo", "hi")
	if code != 0 || strings.TrimSpace(out) != "hi" {
		t.Errorf("alias: %q code %d", out, code)
	}
	// explicit -n wins over the alias namespace
	if _, _, code := runNoNS(t, env, "-n", "default", "exec", "al", "--", "echo", "hi"); code != 2 {
		t.Errorf("-n should override alias namespace, code %d", code)
	}
}

func TestEnvSourcesAndMasking(t *testing.T) {
	out, _, code := run(t, "", "env", "alpine")
	if code != 0 || strings.Contains(out, secret) {
		t.Fatalf("code %d, secret leaked or failed:\n%s", code, out)
	}
	for _, want := range []string{"LOG_LEVEL", "debug", "literal", "REGION", "configmap app-config/REGION", "DB_PASSWORD", "secret app-secret/password", "********"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if out, _, _ := run(t, "", "env", "alpine", "--reveal", "-o", "env"); !strings.Contains(out, "DB_PASSWORD="+secret) {
		t.Errorf("--reveal missing value:\n%s", out)
	}
}

func TestLogs(t *testing.T) {
	out, _, code := run(t, "", "logs", "chatty", "--tail", "1")
	if code != 0 || !regexp.MustCompile(`(?m)^\[chatty-\S+\] hello-from-chatty$`).MatchString(out) {
		t.Errorf("code %d:\n%s", code, out)
	}
}

// Follow keeps going across a pod replacement: lines from the new pod appear.
func TestLogsFollowPicksUpNewPod(t *testing.T) {
	cmd := kxCmd(nil, "logs", "chatty", "-f", "--tail", "1")
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Signal(syscall.SIGINT); cmd.Wait(); pw.Close() }()

	pods := make(chan string, 100)
	go func() {
		re := regexp.MustCompile(`^\[(chatty-\S+)\] hello-from-chatty`)
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			if m := re.FindStringSubmatch(sc.Text()); m != nil {
				pods <- m[1]
			}
		}
		close(pods)
	}()
	next := func(not string) string {
		deadline := time.After(120 * time.Second)
		for {
			select {
			case p, ok := <-pods:
				if !ok {
					t.Fatal("kx logs exited")
				}
				if p != not {
					return p
				}
			case <-deadline:
				t.Fatalf("no log line from a pod other than %q", not)
			}
		}
	}
	first := next("")
	c, err := kube.New(kube.Options{Kubeconfig: kubeconfig, Context: kctx, Namespace: ns})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CoreV1().Pods(ns).Delete(context.Background(), first, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if second := next(first); second == first {
		t.Fatal("same pod")
	}
}
