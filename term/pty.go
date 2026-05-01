package term

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// PTY wraps a pseudoterminal master and the child shell process.
type PTY struct {
	cmd  *exec.Cmd
	file *os.File
}

// clampWinsize bounds rows/cols to the uint16 range expected by the
// kernel ioctl, with a sane lower bound so a degenerate caller can't
// hand the shell a 0-row terminal.
func clampWinsize(n int) uint16 {
	if n < 1 {
		return 1
	}
	if n > 0xFFFF {
		return 0xFFFF
	}
	return uint16(n)
}

// Start spawns $SHELL (fallback /bin/sh) attached to a new PTY sized
// rows×cols. TERM is forced to xterm-256color so apps emit standard
// SGR sequences.
func Start(rows, cols int) (*PTY, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: clampWinsize(rows),
		Cols: clampWinsize(cols),
	})
	if err != nil {
		return nil, err
	}
	return &PTY{cmd: cmd, file: f}, nil
}

// Read forwards from the PTY master.
func (p *PTY) Read(b []byte) (int, error) { return p.file.Read(b) }

// Write forwards to the PTY master.
func (p *PTY) Write(b []byte) (int, error) { return p.file.Write(b) }

// Resize updates the PTY winsize so child processes see the new
// rows/cols on their next stty/SIGWINCH.
func (p *PTY) Resize(rows, cols int) error {
	return pty.Setsize(p.file, &pty.Winsize{
		Rows: clampWinsize(rows),
		Cols: clampWinsize(cols),
	})
}

// Close releases the PTY master and reaps the child if still alive.
func (p *PTY) Close() error {
	err := p.file.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_, _ = p.cmd.Process.Wait()
	}
	return err
}
