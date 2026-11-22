package main

// Tunable numbers. Defaults live at the call sites; the KTUNE environment variable
// ("name=value,name=value") overrides them for local A/B runs and is unset in matches.

import (
	"os"
	"strconv"
	"strings"
)

var knobOverride = func() map[string]float64 {
	m := map[string]float64{}
	for _, kv := range strings.Split(os.Getenv("KTUNE"), ",") {
		if i := strings.IndexByte(kv, '='); i > 0 {
			if v, err := strconv.ParseFloat(kv[i+1:], 64); err == nil {
				m[strings.TrimSpace(kv[:i])] = v
			}
		}
	}
	return m
}()

func knob(name string, def float64) float64 {
	if v, ok := knobOverride[name]; ok {
		return v
	}
	return def
}
