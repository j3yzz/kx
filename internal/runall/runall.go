// Package runall runs one command in several pods at once and prefixes every
// output line with the pod it came from.
package runall

import (
	"bytes"
	"context"
	"io"
	"sync"
)

// Job is one pod to run in; Run executes the command with the given streams.
type Job struct {
	Name string
	Run  func(ctx context.Context, out, errOut io.Writer) error
}

// Result is the outcome of one job.
type Result struct {
	Name string
	Err  error
}

// Do runs all jobs concurrently. Lines on out/errOut are prefixed "name | ",
// never interleaved mid-line. Results come back in job order.
func Do(ctx context.Context, jobs []Job, out, errOut io.Writer) []Result {
	var mu sync.Mutex
	res := make([]Result, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, e := &prefixer{w: out, mu: &mu, p: j.Name + " | "}, &prefixer{w: errOut, mu: &mu, p: j.Name + " | "}
			err := j.Run(ctx, o, e)
			o.flush()
			e.flush()
			res[i] = Result{j.Name, err}
		}()
	}
	wg.Wait()
	return res
}

type prefixer struct {
	w   io.Writer
	mu  *sync.Mutex
	p   string
	buf bytes.Buffer
}

func (p *prefixer) Write(b []byte) (int, error) {
	p.buf.Write(b)
	for {
		i := bytes.IndexByte(p.buf.Bytes(), '\n')
		if i < 0 {
			return len(b), nil
		}
		line := p.buf.Next(i + 1)
		p.emit(line)
	}
}

func (p *prefixer) flush() {
	if p.buf.Len() > 0 {
		p.emit(append(p.buf.Next(p.buf.Len()), '\n'))
	}
}

func (p *prefixer) emit(line []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	io.WriteString(p.w, p.p)
	p.w.Write(line)
}
