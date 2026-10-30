package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// generateUAPCombinations returns all 256 candidate BD_ADDRs for a LAP:
// 00:00:<UAP>:<LAP>, UAP from 00 to FF.
func generateUAPCombinations(lap string) ([]string, error) {
	normalized := strings.ToLower(strings.ReplaceAll(lap, ":", ""))
	if len(normalized) != 6 {
		return nil, fmt.Errorf("invalid LAP format: %s", normalized)
	}
	for _, c := range normalized {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return nil, fmt.Errorf("invalid LAP format: %s", normalized)
		}
	}
	out := make([]string, 0, 256)
	for uap := 0; uap <= 0xFF; uap++ {
		out = append(out, fmt.Sprintf("00:00:%02x:%s:%s:%s", uap, normalized[0:2], normalized[2:4], normalized[4:6]))
	}
	return out, nil
}

type fuzzState struct {
	mu       sync.Mutex
	attempts int
	found    int
	total    int
	foundSet map[string]struct{}
}

type fuzzResult struct {
	seq   int
	msg   string
	found string
}

// coordinateFuzzing brute-forces the UAP for every LAP in lapFile,
// distributing batches of 4-per-adapter across adapters,
// printing each batch's results in attempt order.
func coordinateFuzzing(ctx context.Context, adapters []string, lapFile, outputFile string, timeoutSec int, ping pinger) {
	fmt.Println("Initializing UAP Fuzz...")
	laps, err := loadLines(lapFile)
	if err != nil {
		fmt.Printf("Error: LAP file '%s' does not exist.\n", lapFile)
		return
	}
	if len(laps) == 0 {
		fmt.Printf("Warning: LAP file '%s' is empty. No LAPs to process.\n", lapFile)
		return
	}
	type lapWork struct {
		lap   string
		addrs []string
	}
	var works []lapWork
	for _, lap := range laps {
		addrs, err := generateUAPCombinations(lap)
		if err != nil {
			fmt.Printf("Skipping invalid LAP: %s - %s\n", lap, err)
			continue
		}
		works = append(works, lapWork{lap, addrs})
	}

	st := &fuzzState{foundSet: map[string]struct{}{}}
	st.total = len(works) * 256

	var outMu sync.Mutex
	appendFound := func(addr string) {
		outMu.Lock()
		defer outMu.Unlock()
		if f, err := os.OpenFile(outputFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(f, addr)
			f.Close()
		}
	}

	runOne := func(adapter, target string) fuzzResult {
		st.mu.Lock()
		st.attempts++
		seq := st.attempts
		st.mu.Unlock()
		attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		stdout, _ := ping(attemptCtx, adapter, target, 3)
		cancel()
		if ctx.Err() != nil {
			return fuzzResult{seq: seq}
		}
		st.mu.Lock()
		if pingSucceeded(stdout) {
			st.found++
			found := st.found
			st.mu.Unlock()
			appendFound(target)
			return fuzzResult{seq: seq, found: target,
				msg: fmt.Sprintf("[%s] Found %s. Saving to output file... (%d of %d laps completed; %d total found)", adapter, target, seq, st.total, found)}
		}
		found := st.found
		st.mu.Unlock()
		return fuzzResult{seq: seq,
			msg: fmt.Sprintf("[%s] No response: %s (%d of %d laps completed; %d total found)", adapter, target, seq, st.total, found)}
	}

	for _, w := range works {
		batchSize := 4 * len(adapters)
		for start := 0; start < len(w.addrs); start += batchSize {
			if ctx.Err() != nil {
				return
			}
			end := start + batchSize
			if end > len(w.addrs) {
				end = len(w.addrs)
			}
			batch := w.addrs[start:end]
			results := make([]fuzzResult, len(batch))
			var bwg sync.WaitGroup
			for i, target := range batch {
				bwg.Add(1)
				go func(i int, target string) {
					defer bwg.Done()
					results[i] = runOne(adapters[i%len(adapters)], target)
				}(i, target)
			}
			bwg.Wait()
			sort.Slice(results, func(i, j int) bool { return results[i].seq < results[j].seq })
			for _, r := range results {
				if r.msg == "" {
					continue
				}
				fmt.Println(r.msg)
				if r.found != "" {
					st.mu.Lock()
					st.foundSet[r.found] = struct{}{}
					st.mu.Unlock()
				}
			}
			fmt.Println()
			fmt.Println("Next Batch Initializing...")
		}
	}

	fmt.Println()
	fmt.Println("Newly discovered valid addresses in this run:")
	st.mu.Lock()
	n := len(st.foundSet)
	for addr := range st.foundSet {
		fmt.Println(addr)
	}
	st.mu.Unlock()
	fmt.Printf("\nTotal new addresses found in this run: %d\n", n)
}
