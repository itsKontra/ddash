package agent

import (
	"bufio"
	"errors"
	"io"
	"os/exec"
	"time"
)

// waitDelay is how long RunLines waits for a command's output to end once
// the command itself has: a daemon a package's install script started may
// hold the pipe open for good.
var waitDelay = 10 * time.Second

// maxLine is the longest output line passed on whole; a longer one is cut.
const maxLine = 1 << 20

// RunLines runs cmd and passes each line of its output, stdout and stderr
// together, to out (which may be nil). It returns once cmd has ended,
// whatever cmd printed and whatever it left running.
func RunLines(cmd *exec.Cmd, out func(string)) error {
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	cmd.WaitDelay = waitDelay
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		r := bufio.NewReaderSize(pr, 64<<10)
		for {
			line, err := readLine(r)
			if line != "" || err == nil {
				if out != nil {
					out(line)
				}
			}
			if err != nil {
				return
			}
		}
	}()
	err := cmd.Wait()
	pw.Close()
	<-done
	// The command ended fine; only something it started kept the pipe.
	if errors.Is(err, exec.ErrWaitDelay) {
		return nil
	}
	return err
}

// readLine reads one line without its end, cut at maxLine; the rest of a
// longer line is read and dropped, so the writer never blocks on it.
func readLine(r *bufio.Reader) (string, error) {
	var line []byte
	for {
		part, err := r.ReadSlice('\n')
		if room := maxLine - len(line); room > 0 {
			line = append(line, part[:min(len(part), room)]...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
			line = line[:len(line)-1]
		}
		return string(line), err
	}
}
