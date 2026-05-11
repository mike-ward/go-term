// Command demo runs the go-term widget in a single window.
package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mike-ward/go-gui/gui"
	"github.com/mike-ward/go-gui/gui/backend"
	"github.com/mike-ward/go-term/term"
)

func main() {
	gui.SetTheme(gui.ThemeDarkBordered)

	var t *term.Term
	w := gui.NewWindow(gui.WindowCfg{
		Title:  "go-term",
		Width:  900,
		Height: 600,
		OnInit: func(w *gui.Window) {
			var err error
			t, err = term.New(w, term.Cfg{
				Themes: []term.NamedTheme{
					{Name: "Default", Theme: term.DefaultTheme},
					{Name: "Gruvbox", Theme: term.GruvboxTheme},
					{Name: "Nord", Theme: term.NordTheme},
					{Name: "Solarized Dark", Theme: term.SolarizedDarkTheme},
				},
			})
			if err != nil {
				log.Fatalf("term.New: %v", err)
			}
			w.UpdateView(t.View)
			if os.Getenv("GO_TERM_PROFILE") != "" {
				go profileRun(60 * time.Second)
			}
		},
	})
	defer func() {
		if t != nil {
			_ = t.Close()
		}
	}()
	backend.Run(w)
}

// profileRun captures a heap snapshot, waits d, then captures another
// snapshot. Both are written to the working directory as pprof files,
// and a memstats delta is logged to stderr. Triggers GC before each
// snapshot so reachable-vs-leaked is comparable.
//
// RSS is reported alongside HeapAlloc so off-Go-heap growth (CoreText
// font caches, Metal texture atlases, native shaper state) is visible.
// HeapAlloc flat + RSS climbing → off-heap. Both climbing → Go heap.
func profileRun(d time.Duration) {
	time.Sleep(2 * time.Second) // let startup settle
	var ms1, ms2 runtime.MemStats

	rss1, peak1 := readRSS()
	if err := snapshot("mem_start.pprof", &ms1); err != nil {
		log.Printf("profile: start snapshot: %v", err)
		return
	}
	fmt.Fprintf(os.Stderr,
		"PROFILE start: HeapAlloc=%s HeapInuse=%s HeapSys=%s RSS=%s PeakRSS=%s HeapObjects=%d NumGC=%d\n",
		human(ms1.HeapAlloc), human(ms1.HeapInuse), human(ms1.HeapSys),
		human(rss1), human(peak1), ms1.HeapObjects, ms1.NumGC)
	fmt.Fprintf(os.Stderr, "PROFILE: waiting %s — exercise the terminal now\n", d)

	time.Sleep(d)

	rss2, peak2 := readRSS()
	if err := snapshot("mem_end.pprof", &ms2); err != nil {
		log.Printf("profile: end snapshot: %v", err)
		return
	}
	fmt.Fprintf(os.Stderr,
		"PROFILE end:   HeapAlloc=%s HeapInuse=%s HeapSys=%s RSS=%s PeakRSS=%s HeapObjects=%d NumGC=%d\n",
		human(ms2.HeapAlloc), human(ms2.HeapInuse), human(ms2.HeapSys),
		human(rss2), human(peak2), ms2.HeapObjects, ms2.NumGC)
	fmt.Fprintf(os.Stderr,
		"PROFILE delta: HeapAlloc=%+d HeapInuse=%+d HeapSys=%+d RSS=%+d PeakRSS=%+d HeapObjects=%+d\n",
		int64(ms2.HeapAlloc)-int64(ms1.HeapAlloc),
		int64(ms2.HeapInuse)-int64(ms1.HeapInuse),
		int64(ms2.HeapSys)-int64(ms1.HeapSys),
		int64(rss2)-int64(rss1),
		int64(peak2)-int64(peak1),
		int64(ms2.HeapObjects)-int64(ms1.HeapObjects))
	fmt.Fprintln(os.Stderr,
		"PROFILE: diff with: go tool pprof -base mem_start.pprof mem_end.pprof")
}

// readRSS returns (currentRSS, peakRSS) in bytes. Peak comes from
// getrusage — bytes on darwin, KB on linux. Current is `ps -o rss=` so
// RSS shrinkage after a burst is visible (getrusage only reports peak).
// Returns 0 for any field whose source errors.
func readRSS() (current, peak uint64) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err == nil {
		peak = uint64(ru.Maxrss)
		if runtime.GOOS != "darwin" {
			peak *= 1024
		}
	}
	out, err := exec.Command("ps", "-o", "rss=", "-p",
		strconv.Itoa(os.Getpid())).Output()
	if err == nil {
		if kb, err := strconv.ParseUint(
			strings.TrimSpace(string(out)), 10, 64); err == nil {
			current = kb * 1024
		}
	}
	return current, peak
}

func snapshot(path string, ms *runtime.MemStats) error {
	runtime.GC()
	runtime.GC() // second pass to finalize and reclaim
	runtime.ReadMemStats(ms)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := pprof.WriteHeapProfile(f); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	return f.Close()
}

func human(b uint64) string {
	const (
		k = 1024
		m = k * 1024
		g = m * 1024
	)
	switch {
	case b >= g:
		return fmt.Sprintf("%.2fG", float64(b)/float64(g))
	case b >= m:
		return fmt.Sprintf("%.2fM", float64(b)/float64(m))
	case b >= k:
		return fmt.Sprintf("%.2fK", float64(b)/float64(k))
	default:
		return fmt.Sprintf("%dB", b)
	}
}
