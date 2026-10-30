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
// <NAP>:<UAP>:<LAP>, UAP from 00 to FF, NAP defaulting to 00:00.
func generateUAPCombinations(lap, nap string) ([]string, error) {
	nap, err := parseNAP(nap)
	if err != nil {
		return nil, err
	}
	napParts := strings.Split(nap, ":")
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
		out = append(out, fmt.Sprintf("%s:%s:%02x:%s:%s:%s", napParts[0], napParts[1], uap, normalized[0:2], normalized[2:4], normalized[4:6]))
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
func coordinateFuzzing(ctx context.Context, adapters []string, lapFile, outputFile, nap string, timeoutSec int, ping pinger) {
	logf("Initializing UAP Fuzz...")
	rawLines, err := readLines(lapFile)
	var laps []string
	for _, l := range rawLines {
		if s := strings.TrimSpace(l); s != "" {
			laps = append(laps, s)
		}
	}
	if err != nil {
		logf("Error: LAP file '%s' does not exist.", lapFile)
		return
	}
	if len(laps) == 0 {
		logf("Warning: LAP file '%s' is empty. No LAPs to process.", lapFile)
		return
	}
	type lapWork struct {
		lap   string
		addrs []string
	}
	var works []lapWork
	for _, lap := range laps {
		addrs, err := generateUAPCombinations(lap, nap)
		if err != nil {
			logf("Skipping invalid LAP: %s - %s", lap, err)
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
		stdout, _ := ping(attemptCtx, adapter, target, 1)
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
				logf("%s", r.msg)
				if r.found != "" {
					st.mu.Lock()
					st.foundSet[r.found] = struct{}{}
					st.mu.Unlock()
				}
			}
			logf("Next Batch Initializing...")
		}
	}

	logf("Newly discovered valid addresses in this run:")
	st.mu.Lock()
	n := len(st.foundSet)
	for addr := range st.foundSet {
		logf("%s", addr)
	}
	st.mu.Unlock()
	logf("Total new addresses found in this run: %d", n)
}
