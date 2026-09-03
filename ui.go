// ui.go: the embedded web UI for probe-sim.
//
// uiHTML is a single self-contained page (no external assets) that polls
// /api/state and drives the /api/set controls. It is intentionally one file so
// the server stays a single static binary with no asset bundling step.
package main

// uiHTML is the full control-panel page served at /.
const uiHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>probe-sim // health console</title>
<style>
  :root {
    --bg: #0e1116;
    --panel: #151a22;
    --edge: #232c39;
    --ink: #e6edf3;
    --dim: #7d8794;
    --up: #3ad07a;
    --warn: #f2b53c;
    --down: #ff5c5c;
    --lock: #b57bff;
    --mono: "IBM Plex Mono", ui-monospace, "SF Mono", Menlo, monospace;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; background: var(--bg); color: var(--ink);
    font-family: var(--mono); line-height: 1.5;
    padding: 32px 20px 64px;
  }
  .wrap { max-width: 760px; margin: 0 auto; }
  header { border-bottom: 1px solid var(--edge); padding-bottom: 16px; margin-bottom: 28px; }
  h1 { font-size: 18px; letter-spacing: 2px; margin: 0; text-transform: uppercase; }
  .sub { color: var(--dim); font-size: 12px; letter-spacing: 1px; margin-top: 6px; }
  .grid { display: grid; gap: 16px; }
  .channel {
    background: var(--panel); border: 1px solid var(--edge);
    border-radius: 4px; padding: 18px 20px;
    display: grid; grid-template-columns: 14px 1fr auto; gap: 16px; align-items: center;
  }
  .lamp {
    width: 14px; height: 14px; border-radius: 50%;
    background: var(--dim); align-self: start; margin-top: 5px;
    box-shadow: 0 0 0 0 transparent; transition: background .2s, box-shadow .2s;
  }
  .lamp.up   { background: var(--up);   box-shadow: 0 0 12px 1px var(--up); }
  .lamp.warn { background: var(--warn); box-shadow: 0 0 12px 1px var(--warn); }
  .lamp.down { background: var(--down); box-shadow: 0 0 12px 1px var(--down); }
  .lamp.lock { background: var(--lock); box-shadow: 0 0 12px 1px var(--lock); }
  .meta .name { font-size: 14px; letter-spacing: 1px; text-transform: uppercase; }
  .meta .path { color: var(--dim); font-size: 12px; }
  .meta .status { font-size: 12px; margin-top: 4px; }
  .meta .effect { color: var(--dim); font-size: 11px; margin-top: 2px; }
  .controls { display: flex; flex-direction: column; gap: 8px; align-items: flex-end; }
  button {
    font-family: var(--mono); font-size: 11px; letter-spacing: 1px;
    text-transform: uppercase; cursor: pointer;
    background: transparent; color: var(--ink);
    border: 1px solid var(--edge); border-radius: 3px;
    padding: 7px 12px; transition: border-color .15s, color .15s;
    white-space: nowrap;
  }
  button:hover { border-color: var(--ink); }
  button.armed { border-color: var(--down); color: var(--down); }
  button:focus-visible { outline: 2px solid var(--ink); outline-offset: 2px; }
  .bar { height: 4px; background: var(--edge); border-radius: 2px; overflow: hidden; margin-top: 8px; }
  .bar > span { display: block; height: 100%; background: var(--warn); width: 0; transition: width .3s linear, background .2s; }
  .bar > span.done { background: var(--up); }
  footer { margin-top: 28px; color: var(--dim); font-size: 11px; letter-spacing: .5px; }
  .credit { margin-top: 40px; text-align: center; color: var(--dim); font-size: 11px; letter-spacing: 1px; opacity: .6; }
  .credit a { color: var(--ink); text-decoration: none; border-bottom: 1px solid var(--edge); }
  .credit a:hover { border-color: var(--ink); }
  code { color: var(--ink); }
  @media (prefers-reduced-motion: reduce) { * { transition: none !important; } }
</style>
</head>
<body>
<div class="wrap">
  <header>
    <h1>probe-sim &middot; health console</h1>
    <div class="sub">flip a channel and watch kubectl. liveness restarts. readiness delists.</div>
  </header>

  <div class="grid">
    <div class="channel">
      <div class="lamp" id="lamp-startup"></div>
      <div class="meta">
        <div class="name">startup</div>
        <div class="path">GET /startupz</div>
        <div class="status" id="st-startup">-</div>
        <div class="effect">gates the other probes until first success</div>
        <div class="bar"><span id="bar-startup"></span></div>
      </div>
      <div class="controls">
        <button id="btn-startupHeld" onclick="toggle('startupHeld')">pause countdown</button>
      </div>
    </div>

    <div class="channel">
      <div class="lamp" id="lamp-live"></div>
      <div class="meta">
        <div class="name">liveness</div>
        <div class="path">GET /healthz</div>
        <div class="status" id="st-live">-</div>
        <div class="effect">failure &rarr; kubelet restarts the container</div>
      </div>
      <div class="controls">
        <button id="btn-liveFail" onclick="toggle('liveFail')">return 500</button>
        <button id="btn-liveLocked" onclick="toggle('liveLocked')">lock up</button>
      </div>
    </div>

    <div class="channel">
      <div class="lamp" id="lamp-ready"></div>
      <div class="meta">
        <div class="name">readiness</div>
        <div class="path">GET /ready</div>
        <div class="status" id="st-ready">-</div>
        <div class="effect">failure &rarr; removed from Service endpoints, no restart</div>
      </div>
      <div class="controls">
        <button id="btn-readyFail" onclick="toggle('readyFail')">return 503</button>
      </div>
    </div>
  </div>

  <footer>
    watch it: <code>kubectl get pod -w</code> for restarts &middot;
    <code>kubectl get endpoints -w</code> for delisting
  </footer>
  <div class="credit">Demo implemented using <a href="https://claude.ai" target="_blank" rel="noopener">claude.ai</a></div>
</div>

<script>
// render paints the UI from a state snapshot returned by the control API.
// Input:  s (object from /api/state)  Output: none (mutates the DOM)
function render(s) {
  // startup channel
  var suLamp = document.getElementById('lamp-startup');
  var suBar = document.getElementById('bar-startup');
  var suSt = document.getElementById('st-startup');
  if (s.startupHeld) {
    // Paused: the countdown is frozen at its current position and /startupz is 503.
    suLamp.className = 'lamp down';
    suSt.textContent = 'paused / 503 / ' + Math.ceil(s.startupRemainingSeconds) + 's frozen';
  } else if (s.startupComplete) {
    // Complete: startup has done its job, so the lamp goes dark (unlit default).
    suLamp.className = 'lamp';
    suSt.textContent = 'started / 200';
  } else {
    suLamp.className = 'lamp warn';
    suSt.textContent = 'starting / 503 / ' + Math.ceil(s.startupRemainingSeconds) + 's left';
  }
  // Bar tracks delay progress from remaining time, so a paused countdown holds
  // its current fill rather than snapping to empty. It goes green once complete.
  var pct = s.startupDelaySeconds > 0
    ? 100 * (1 - s.startupRemainingSeconds / s.startupDelaySeconds) : 100;
  suBar.style.width = Math.max(0, Math.min(100, pct)) + '%';
  suBar.classList.toggle('done', !!s.startupComplete);
  arm('btn-startupHeld', s.startupHeld);

  // liveness channel
  var liveLamp = document.getElementById('lamp-live');
  var liveSt = document.getElementById('st-live');
  if (s.liveLocked) {
    // A tripped control shows red: the probe will fail and force a restart.
    liveLamp.className = 'lamp down';
    liveSt.textContent = 'locked / probe hangs / will restart';
  } else if (s.liveFail) {
    liveLamp.className = 'lamp down';
    liveSt.textContent = 'failing / 500 / will restart';
  } else if (!s.startupComplete) {
    // Kubelet suppresses liveness probing until startup succeeds.
    liveLamp.className = 'lamp warn';
    liveSt.textContent = 'gated by startup';
  } else {
    liveLamp.className = 'lamp up';
    liveSt.textContent = 'ok / 200';
  }
  arm('btn-liveFail', s.liveFail);
  arm('btn-liveLocked', s.liveLocked);

  // readiness channel
  var readyLamp = document.getElementById('lamp-ready');
  var readySt = document.getElementById('st-ready');
  if (!s.startupComplete) {
    readyLamp.className = 'lamp warn';
    readySt.textContent = 'waiting on startup / 503';
  } else if (s.readyFail) {
    readyLamp.className = 'lamp down';
    readySt.textContent = 'not ready / 503 / delisted';
  } else {
    readyLamp.className = 'lamp up';
    readySt.textContent = 'ready / 200';
  }
  arm('btn-readyFail', s.readyFail);
}

// arm toggles the "armed" style on a control button.
// Input: id (button id), on (bool)  Output: none
function arm(id, on) {
  document.getElementById(id).classList.toggle('armed', !!on);
}

// toggle flips a boolean flag on the server, reading its current value first.
// Input: flag (string)  Output: none (triggers a re-render via poll)
function toggle(flag) {
  fetch('/api/state').then(function (r) { return r.json(); }).then(function (s) {
    var next = !s[flag];
    fetch('/api/set?flag=' + flag + '&value=' + next).then(function (r) { return r.json(); }).then(render);
  });
}

// poll refreshes state on a fixed interval so the startup countdown animates
// and external kubectl-driven changes stay reflected.
function poll() {
  fetch('/api/state').then(function (r) { return r.json(); }).then(render).catch(function () {});
}
setInterval(poll, 1000);
poll();
</script>
</body>
</html>
`