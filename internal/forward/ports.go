package forward

import (
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// PortSpec maps a local port to a pod port. Local 0 means "pick a free one".
type PortSpec struct{ Local, Remote int }

// ParsePorts parses "remote" (same local port) or "local:remote" arguments.
func ParsePorts(args []string) ([]PortSpec, error) {
	specs := make([]PortSpec, 0, len(args))
	for _, a := range args {
		l, r := a, a
		if before, after, ok := strings.Cut(a, ":"); ok {
			l, r = before, after
		}
		local, err := port(l, true)
		if err != nil {
			return nil, fmt.Errorf("bad port %q: %w", a, err)
		}
		remote, err := port(r, false)
		if err != nil {
			return nil, fmt.Errorf("bad port %q: %w", a, err)
		}
		specs = append(specs, PortSpec{local, remote})
	}
	return specs, nil
}

func port(s string, allowZero bool) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 65535 || (n == 0 && !allowZero) {
		return 0, fmt.Errorf("want a port number 1-65535")
	}
	return n, nil
}

// DefaultPorts returns the pod's only declared TCP container port, for when
// the user gave none. More or fewer than one is an error asking to be explicit.
func DefaultPorts(pod *corev1.Pod) ([]PortSpec, error) {
	set := map[int]bool{}
	for _, c := range pod.Spec.Containers {
		for _, p := range c.Ports {
			if p.Protocol == "" || p.Protocol == corev1.ProtocolTCP {
				set[int(p.ContainerPort)] = true
			}
		}
	}
	ports := make([]int, 0, len(set))
	for p := range set {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	switch len(ports) {
	case 1:
		return []PortSpec{{ports[0], ports[0]}}, nil
	case 0:
		return nil, fmt.Errorf("pod %s declares no container ports; give one, e.g. kx forward <app> 8080", pod.Name)
	}
	return nil, fmt.Errorf("pod %s exposes several ports %v; choose with e.g. kx forward <app> %d", pod.Name, ports, ports[0])
}

// AssignLocal fills in local ports that are 0 or already in use on addr with
// free ones, noting each change on note. It is done once, so the same local
// ports survive every reconnect.
func AssignLocal(addr string, specs []PortSpec, note io.Writer) ([]PortSpec, error) {
	out := make([]PortSpec, len(specs))
	used := map[int]bool{}
	for i, s := range specs {
		if s.Local != 0 && !used[s.Local] && free(addr, s.Local) {
			used[s.Local] = true
			out[i] = s
			continue
		}
		p, err := freePort(addr)
		for err == nil && used[p] {
			p, err = freePort(addr)
		}
		if err != nil {
			return nil, fmt.Errorf("no free local port on %s: %w", addr, err)
		}
		if s.Local != 0 {
			fmt.Fprintf(note, "local port %d is busy, using %d\n", s.Local, p)
		}
		used[p] = true
		out[i] = PortSpec{p, s.Remote}
	}
	return out, nil
}

func free(addr string, port int) bool {
	l, err := net.Listen("tcp", net.JoinHostPort(addr, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

func freePort(addr string) (int, error) {
	l, err := net.Listen("tcp", net.JoinHostPort(addr, "0"))
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
