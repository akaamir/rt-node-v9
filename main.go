package main

// Kestrel: a mechfight/1 pilot. This file is only the line pump: read server messages from
// stdin, hand the freshest observation to the pilot, write exactly one order per wake-up.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
)

const buildStamp = "b11-force-rerun-20261122T155501Z"

var callsign = func() string {
	if n := os.Getenv("KNAME"); n != "" {
		return n
	}
	return "kestrel-8.3"
}()

func sniffTick(line []byte) (int64, bool) {
	i := bytes.Index(line, []byte(`"tick":`))
	if i < 0 {
		return 0, false
	}
	j := i + 7
	for j < len(line) && line[j] == ' ' {
		j++
	}
	k := j
	for k < len(line) && line[k] >= '0' && line[k] <= '9' {
		k++
	}
	if k == j {
		return 0, false
	}
	v, err := strconv.ParseInt(string(line[j:k]), 10, 64)
	return v, err == nil
}

func kindOf(line []byte) string {
	head := line
	if len(head) > 48 {
		head = head[:48]
	}
	switch {
	case bytes.Contains(head, []byte(`"obs"`)):
		return "obs"
	case bytes.Contains(head, []byte(`"hello"`)):
		return "hello"
	case bytes.Contains(head, []byte(`"end"`)):
		return "end"
	}
	var probe struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(line, &probe)
	return probe.Type
}

func main() {
	fmt.Fprintf(os.Stderr, "kestrel build %s\n", buildStamp)
	debug.SetGCPercent(600)
	debug.SetMemoryLimit(360 << 20)

	lines := make(chan []byte, 512)
	go func() {
		rd := bufio.NewReaderSize(os.Stdin, 1<<20)
		for {
			ln, err := rd.ReadBytes('\n')
			if len(bytes.TrimSpace(ln)) > 0 {
				lines <- ln
			}
			if err != nil {
				close(lines)
				return
			}
		}
	}()

	out := bufio.NewWriterSize(os.Stdout, 1<<16)
	var pilot *Pilot
	var fallback Order
	fallback.Pick, fallback.Take, fallback.Drop = -1, -1, -1
	buf := make([]byte, 0, 512)

	greet := func(line []byte) {
		var g Greeting
		if err := json.Unmarshal(line, &g); err != nil {
			fmt.Fprintln(os.Stderr, "hello decode:", err)
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintln(os.Stderr, "startup panic:", r)
					pilot = nil
				}
			}()
			pilot = NewPilot(&g)
		}()
		fmt.Fprintf(out, "{\"type\":\"ready\",\"name\":%q}\n", callsign)
		out.Flush()
	}

	answer := func(line []byte) {
		var f Frame
		if err := json.Unmarshal(line, &f); err != nil {
			if tick, ok := sniffTick(line); ok {
				fallback.Fire0, fallback.Fire1, fallback.Jump = false, false, false
				out.Write(fallback.Encode(buf[:0], tick))
				out.Flush()
			}
			return
		}
		ord := fallback
		ord.Fire0, ord.Fire1, ord.Jump, ord.Reload = false, false, false, false
		ord.Pick, ord.Take, ord.Drop = -1, -1, -1
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "decide panic: %v\n%s\n", r, debug.Stack())
				}
			}()
			if pilot != nil {
				ord = pilot.Decide(&f)
			} else {
				ord = blindOrder(&f)
			}
		}()
		fallback = ord
		out.Write(ord.Encode(buf[:0], int64(f.Tick)))
		out.Flush()
	}

	for ln := range lines {
		// drain: only the newest observation deserves an answer
		batch := [][]byte{ln}
	drain:
		for {
			select {
			case more, ok := <-lines:
				if !ok {
					break drain
				}
				batch = append(batch, more)
			default:
				break drain
			}
		}
		var newest []byte
		for _, b := range batch {
			switch kindOf(b) {
			case "hello":
				greet(b)
			case "end":
				out.Flush()
				os.Exit(0)
			case "obs":
				newest = b
			}
		}
		if newest != nil {
			answer(newest)
		}
	}
}
