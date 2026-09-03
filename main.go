// probe-sim: a Kubernetes probe simulator.
//
// Serves three health endpoints (/healthz for liveness, /ready for readiness,
// /startupz for startup) whose behaviour is controllable at runtime through a
// small web UI served at /. This lets you demonstrate exactly how the kubelet
// reacts when liveness fails (restart) versus when readiness fails (removal
// from Service endpoints), and how a configurable startup delay interacts with
// the startupProbe.
//
// Behaviour is driven by an in-memory state struct guarded by a mutex. The web
// UI reads and writes that state through a tiny JSON control API (/api/*),
// while the probe endpoints report status derived from it.
//
// Configuration (all optional):
//   STARTUP_DELAY_SECONDS  seconds before startup completes    (default 15)
//   PORT                   port to listen on                   (default 8080)
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// state holds all runtime-mutable simulator state.
//
// The startup countdown is modelled as an accumulator rather than a fixed
// start time so it can be paused: elapsedBanked holds run-time accumulated up
// to the last pause, and runningSince marks when the current running stretch
// began. Effective elapsed is elapsedBanked plus, when not held, the time
// since runningSince. Holding freezes the countdown; releasing resumes it from
// the same position.
//
// Fields:
//   startupDelay   configured seconds until startup completes
//   elapsedBanked  run-time accumulated up to the last pause
//   runningSince   start of the current running stretch (valid when not held)
//   liveLocked     when true, /healthz hangs forever (simulates a wedged process)
//   liveFail       when true, /healthz returns 500 (simulates a crash-worthy fault)
//   readyFail      when true, /ready returns 503 (simulates "cannot serve traffic")
//   startupHeld    when true, the startup countdown is paused and /startupz is 503
type state struct {
	mu            sync.Mutex
	startupDelay  time.Duration
	elapsedBanked time.Duration
	runningSince  time.Time
	liveLocked    bool
	liveFail      bool
	readyFail     bool
	startupHeld   bool
}

// effectiveElapsed returns total startup run-time, counting the current running
// stretch only when not held. Callers must hold s.mu.
//
// Input:  none (reads receiver state; caller holds the lock)
// Output: accumulated run-time toward the startup delay
func (s *state) effectiveElapsed() time.Duration {
	if s.startupHeld {
		return s.elapsedBanked
	}
	return s.elapsedBanked + time.Since(s.runningSince)
}

// setStartupHeld pauses or resumes the startup countdown, banking elapsed
// run-time at the transition so the countdown freezes in place and later
// resumes from the same position. Callers must hold s.mu.
//
// Input:  held (true to pause the countdown, false to resume it)
// Output: none (mutates receiver state)
func (s *state) setStartupHeld(held bool) {
	if held == s.startupHeld {
		return
	}
	if held {
		// Pausing: bank the run-time accrued during this running stretch.
		s.elapsedBanked += time.Since(s.runningSince)
	} else {
		// Resuming: start a fresh running stretch from now.
		s.runningSince = time.Now()
	}
	s.startupHeld = held
}

// startupDone reports whether startup is currently considered complete.
//
// Startup is complete once accumulated run-time reaches the configured delay.
// While held the countdown is frozen, so it can only complete when running.
//
// Input:  none (reads receiver state under lock)
// Output: true when not held and effective elapsed >= startupDelay
func (s *state) startupDone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.startupHeld && s.effectiveElapsed() >= s.startupDelay
}

// snapshot returns a copy of the user-visible state as a plain map for JSON.
//
// Input:  none
// Output: map describing current flags and startup progress
func (s *state) snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	elapsed := s.effectiveElapsed()
	remaining := s.startupDelay.Seconds() - elapsed.Seconds()
	if remaining < 0 {
		remaining = 0
	}
	// Startup counts as complete only when the accumulated run-time has reached
	// the delay and it is not currently held (paused).
	complete := !s.startupHeld && elapsed >= s.startupDelay
	return map[string]any{
		"startupDelaySeconds":     s.startupDelay.Seconds(),
		"startupRemainingSeconds": remaining,
		"startupComplete":         complete,
		"startupHeld":             s.startupHeld,
		"liveLocked":              s.liveLocked,
		"liveFail":                s.liveFail,
		"readyFail":               s.readyFail,
	}
}

// envInt reads an integer environment variable, returning def when unset/invalid.
//
// Input:  key (env var name), def (fallback value)
// Output: parsed integer, or def
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func main() {
	st := &state{
		startupDelay: time.Duration(envInt("STARTUP_DELAY_SECONDS", 15)) * time.Second,
		runningSince: time.Now(),
	}
	port := envInt("PORT", 8080)

	mux := http.NewServeMux()

	// Liveness endpoint. When locked, the handler blocks forever so the probe
	// times out; when liveFail is set, it returns 500. Either drives the
	// kubelet to restart the container after failureThreshold consecutive hits.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		locked, failed := st.liveLocked, st.liveFail
		st.mu.Unlock()

		if locked {
			// Block until the client (kubelet) gives up: a wedged event loop.
			select {}
		}
		if failed {
			http.Error(w, "liveness: unhealthy", http.StatusInternalServerError)
			return
		}
		fmt.Fprintln(w, "ok")
	})

	// Readiness endpoint. Returns 503 while startup is still in progress, or
	// when readyFail is set. Readiness failure removes the pod from Service
	// endpoints without restarting it.
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		if !st.startupDone() {
			http.Error(w, "not ready: starting up", http.StatusServiceUnavailable)
			return
		}
		st.mu.Lock()
		failed := st.readyFail
		st.mu.Unlock()
		if failed {
			http.Error(w, "not ready: serving disabled", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ready")
	})

	// Startup endpoint. Returns 503 until the configured delay elapses, then
	// 200 thereafter. The startupProbe gates the other two probes until this
	// succeeds.
	mux.HandleFunc("/startupz", func(w http.ResponseWriter, r *http.Request) {
		if !st.startupDone() {
			http.Error(w, "starting up", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "started")
	})

	// Control API: returns current state as JSON.
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(st.snapshot())
	})

	// Control API: toggles a named flag. Query param ?flag=<name>&value=<bool>.
	// Recognised flags: liveLocked, liveFail, readyFail, startupHeld.
	mux.HandleFunc("/api/set", func(w http.ResponseWriter, r *http.Request) {
		flag := r.URL.Query().Get("flag")
		value := r.URL.Query().Get("value") == "true"
		st.mu.Lock()
		switch flag {
		case "liveLocked":
			st.liveLocked = value
		case "liveFail":
			st.liveFail = value
		case "readyFail":
			st.readyFail = value
		case "startupHeld":
			// Route through the helper so elapsed run-time is banked at the
			// transition, freezing the countdown in place while held.
			st.setStartupHeld(value)
		}
		st.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(st.snapshot())
	})

	// Web UI.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, uiHTML)
	})

	addr := fmt.Sprintf(":%d", port)
	log.Printf("probe-sim listening on %s (startup delay %s)", addr, st.startupDelay)
	log.Fatal(http.ListenAndServe(addr, mux))
}