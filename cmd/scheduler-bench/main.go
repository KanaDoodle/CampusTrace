// scheduler-bench compares the former run-wide FIFO permit with the production
// per-quantum fair permit. All provider work is synthetic; no network is used.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/internal/scheduling"
	"os"
	"runtime"
	"sort"
	"sync"
	"time"
)

type distribution struct{ P50, P95, P99, Max float64 }

func dist(v []float64) distribution {
	sort.Float64s(v)
	if len(v) == 0 {
		return distribution{}
	}
	at := func(p float64) float64 {
		n := int(float64(len(v)-1)*p + .999999)
		if n >= len(v) {
			n = len(v) - 1
		}
		return v[n]
	}
	return distribution{at(.5), at(.95), at(.99), v[len(v)-1]}
}

type result struct {
	Mode                                   string       `json:"mode"`
	ElapsedMS                              float64      `json:"elapsed_ms"`
	QuantaPerSecond                        float64      `json:"quanta_per_second"`
	SmallWaitMS                            distribution `json:"small_wait_ms"`
	SmallLatencyMS                         distribution `json:"small_latency_ms"`
	AllQuantumWaitMS                       distribution `json:"all_quantum_wait_ms"`
	FirstStartMS                           distribution `json:"first_start_ms"`
	MaxRunning, MaxWaiting, PeakGoroutines int
	PeakHeapBytes                          uint64
	CompletedQuanta                        int
}

func run(mode string, big, small, quanta, concurrency int, delay time.Duration) result {
	runtime.GC()
	p := scheduling.New(concurrency, 64, 1)
	slots := make(chan struct{}, concurrency)
	var mu sync.Mutex
	var wg sync.WaitGroup
	output := result{Mode: mode}
	smallWait, smallLatency, allWait, first := []float64{}, []float64{}, []float64{}, []float64{}
	peakRunning, peakWaiting := 0, 0
	running, waiting := 0, 0
	start := time.Now()
	started := make(chan struct{}, big)
	acquire := func(owner string) func() {
		if mode == "fair_quantum" {
			r, e := p.Acquire(context.Background(), owner)
			if e != nil {
				panic(e)
			}
			return r
		}
		mu.Lock()
		waiting++
		if waiting > peakWaiting {
			peakWaiting = waiting
		}
		mu.Unlock()
		slots <- struct{}{}
		mu.Lock()
		waiting--
		running++
		if running > peakRunning {
			peakRunning = running
		}
		mu.Unlock()
		return func() { mu.Lock(); running--; mu.Unlock(); <-slots }
	}
	work := func(owner string, count int, isSmall bool) {
		defer wg.Done()
		arrived := time.Now()
		waitTotal := 0.
		var hold func()
		if !isSmall {
			started <- struct{}{}
		}
		for i := 0; i < count; i++ {
			queued := time.Now()
			release := func() {}
			if mode == "fair_quantum" {
				release = acquire(owner)
			} else if i == 0 {
				hold = acquire(owner)
			}
			wait := float64(time.Since(queued).Microseconds()) / 1000
			waitTotal += wait
			mu.Lock()
			allWait = append(allWait, wait)
			if i == 0 {
				first = append(first, float64(time.Since(start).Microseconds())/1000)
			}
			mu.Unlock()
			time.Sleep(delay)
			release()
			mu.Lock()
			output.CompletedQuanta++
			mu.Unlock()
		}
		if hold != nil {
			hold()
		}
		if isSmall {
			mu.Lock()
			smallWait = append(smallWait, waitTotal)
			smallLatency = append(smallLatency, float64(time.Since(arrived).Microseconds())/1000)
			mu.Unlock()
		}
	}
	stopped := make(chan struct{})
	samplerDone := make(chan struct{})
	go func() {
		defer close(samplerDone)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-ticker.C:
				if mode == "fair_quantum" {
					if n := p.Stats().Running; n > output.MaxRunning {
						output.MaxRunning = n
					}
				}
				var mem runtime.MemStats
				runtime.ReadMemStats(&mem)
				if mem.HeapAlloc > output.PeakHeapBytes {
					output.PeakHeapBytes = mem.HeapAlloc
				}
				if n := runtime.NumGoroutine(); n > output.PeakGoroutines {
					output.PeakGoroutines = n
				}
			}
		}
	}()
	for i := 0; i < big; i++ {
		wg.Add(1)
		go work(fmt.Sprintf("batch-%02d", i), quanta, false)
	}
	for i := 0; i < big; i++ {
		<-started
	}
	// Let the initial bulk workload reach the scheduler before short requests.
	time.Sleep(delay / 2)
	for i := 0; i < small; i++ {
		wg.Add(1)
		go work(fmt.Sprintf("small-%02d", i), 1, true)
	}
	wg.Wait()
	close(stopped)
	<-samplerDone
	output.ElapsedMS = float64(time.Since(start).Microseconds()) / 1000
	output.QuantaPerSecond = float64(output.CompletedQuanta) / (output.ElapsedMS / 1000)
	output.SmallWaitMS = dist(smallWait)
	output.SmallLatencyMS = dist(smallLatency)
	output.AllQuantumWaitMS = dist(allWait)
	output.FirstStartMS = dist(first)
	if mode == "fair_quantum" {
		stats := p.Stats()
		output.MaxWaiting = stats.PeakWaiting
	} else {
		output.MaxRunning = peakRunning
		output.MaxWaiting = peakWaiting
	}
	return output
}
func main() {
	big := flag.Int("batch-users", 8, "bulk owners")
	small := flag.Int("small-users", 8, "short owners")
	quanta := flag.Int("quanta", 20, "model batches per bulk owner")
	concurrency := flag.Int("concurrency", 2, "execution permits")
	ms := flag.Int("delay-ms", 20, "synthetic provider duration per quantum")
	repeats := flag.Int("repeats", 3, "repetitions per design")
	flag.Parse()
	if *big < 1 || *small < 1 || *big+*small > 48 || *quanta < 1 || *quanta > 100 || *ms < 1 || *ms > 1000 || *concurrency < 1 || *concurrency > 16 || *repeats < 1 || *repeats > 10 {
		fmt.Fprintln(os.Stderr, "bounded parameters required")
		os.Exit(2)
	}
	results := []result{}
	for i := 0; i < *repeats; i++ {
		for _, mode := range []string{"legacy_run_fifo", "fair_quantum"} {
			results = append(results, run(mode, *big, *small, *quanta, *concurrency, time.Duration(*ms)*time.Millisecond))
		}
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "batch_users": *big, "small_users": *small, "bulk_quanta": *quanta, "concurrency": *concurrency, "quantum_ms": *ms, "queue_limit": 64, "scope": "single process scheduler, synthetic timers, no SQL/Redis/provider I/O; memory includes Go runtime", "results": results})
}
