// Block iOS pinch-zoom. iOS Safari ignores user-scalable=no in the viewport
// meta for accessibility, so we suppress the gesture* events directly.
(function () {
  "use strict";
  function block(e) { e.preventDefault(); }
  document.addEventListener("gesturestart",  block);
  document.addEventListener("gesturechange", block);
  document.addEventListener("gestureend",    block);
})();

// History row expand/collapse. Lives on the home page; tolerant of missing
// elements so it's safe to load on any page.
(function () {
  "use strict";
  document.body.addEventListener("click", function (e) {
    const btn = e.target.closest(".js-history-toggle");
    if (!btn) return;
    const row = btn.closest(".history-row");
    if (!row) return;
    const expanded = row.classList.toggle("history-row--expanded");
    btn.setAttribute("aria-expanded", expanded ? "true" : "false");
  });
})();

// Theme radio: auto-submit on change so the user doesn't need a Save button.
(function () {
  "use strict";
  const form = document.querySelector(".theme-toggle");
  if (!form) return;
  form.addEventListener("change", function (e) {
    if (e.target && e.target.name === "mode") form.submit();
  });
})();

(function () {
  "use strict";

  const REST_SECONDS = 90;
  const el = {
    box:   document.getElementById("rest-timer"),
    time:  document.getElementById("rest-timer-time"),
    label: document.getElementById("rest-timer-label"),
    fill:  document.getElementById("rest-timer-fill"),
    close: document.getElementById("rest-timer-close"),
  };
  if (!el.box) return;

  let startedAt = 0;
  let endsAt = 0;
  let raf = 0;
  let beeped = false;

  // iOS Safari requires the AudioContext be created or resumed inside a user
  // gesture; once unlocked it stays usable for the page lifetime. We lazily
  // create it on the first pointerdown so the timer can play freely later.
  let audioCtx = null;
  function unlockAudio() {
    if (audioCtx) return;
    const Ctor = window.AudioContext || window.webkitAudioContext;
    if (!Ctor) return;
    try {
      audioCtx = new Ctor();
      if (audioCtx.state === "suspended") audioCtx.resume();
    } catch (_) { audioCtx = null; }
  }
  document.addEventListener("pointerdown", unlockAudio, { once: false, passive: true });

  function soundEnabled() {
    // Cookie set by POST /settings/rest-sound/toggle. Default = on.
    return !/(?:^|;\s*)rest_sound=off(?:;|$)/.test(document.cookie);
  }

  function beep() {
    if (!soundEnabled() || !audioCtx) return;
    // Two short sine pips (~880 Hz then ~660 Hz), gain-ramped to avoid clicks.
    const now = audioCtx.currentTime;
    [[880, 0], [660, 0.18]].forEach(function (p) {
      const osc = audioCtx.createOscillator();
      const gain = audioCtx.createGain();
      osc.type = "sine";
      osc.frequency.value = p[0];
      const t0 = now + p[1];
      gain.gain.setValueAtTime(0.0001, t0);
      gain.gain.exponentialRampToValueAtTime(0.35, t0 + 0.02);
      gain.gain.exponentialRampToValueAtTime(0.0001, t0 + 0.16);
      osc.connect(gain).connect(audioCtx.destination);
      osc.start(t0);
      osc.stop(t0 + 0.18);
    });
  }

  function fmt(sec, mode) {
    sec = Math.max(0, mode === "up" ? Math.floor(sec) : Math.ceil(sec));
    const m = Math.floor(sec / 60);
    const s = sec % 60;
    return m + ":" + (s < 10 ? "0" + s : s);
  }

  function tick() {
    const now = Date.now();
    const remaining = (endsAt - now) / 1000;
    if (remaining > 0) {
      el.time.textContent = fmt(remaining);
      const pct = Math.max(0, Math.min(100, (remaining / REST_SECONDS) * 100));
      el.fill.style.width = pct + "%";
      raf = requestAnimationFrame(tick);
      return;
    }
    // Crossed zero: switch to count-up mode. Display total elapsed since the
    // timer started, so the number flows continuously from 1:30 (the rest
    // duration) upward.
    if (!beeped) {
      beeped = true;
      el.box.classList.add("rest-timer--done");
      el.box.classList.remove("rest-timer--running");
      el.label.textContent = "Lift now!";
      beep();
    }
    el.time.textContent = fmt((now - startedAt) / 1000, "up");
    raf = requestAnimationFrame(tick);
  }

  function start() {
    startedAt = Date.now();
    endsAt = startedAt + REST_SECONDS * 1000;
    beeped = false;
    el.box.hidden = false;
    el.label.textContent = "Rest";
    // Paint the initial countdown frame eagerly so a re-trigger from the
    // "Lift now!" count-up state snaps cleanly to 1:30 / full bar instead of
    // briefly showing the stale count-up value before the next rAF runs.
    el.time.textContent = fmt(REST_SECONDS);
    el.fill.style.width = "100%";
    el.box.classList.add("rest-timer--running");
    el.box.classList.remove("rest-timer--done");
    if (raf) cancelAnimationFrame(raf);
    raf = requestAnimationFrame(tick);
  }

  function stop() {
    cancelAnimationFrame(raf);
    raf = 0;
    el.box.hidden = true;
    el.box.classList.remove("rest-timer--running", "rest-timer--done");
  }

  el.close.addEventListener("click", stop);

  // HTMX fires this when /sets/{id}/tap returns its first-tap response with
  // the HX-Trigger: startRestTimer header.
  document.body.addEventListener("startRestTimer", start);

  // Last set of the workout (or walking-done when walking is last) sends
  // stopRestTimer instead, so any running countdown / "Lift now!" is killed
  // and the bar disappears - nothing left to rest for.
  document.body.addEventListener("stopRestTimer", stop);

  // Lock-violation toast: server returns 409 + HX-Trigger: workoutLocked
  // when the user tries to mutate a finished workout.
  const toast = document.getElementById("toast");
  let toastTimer = 0;
  function showToast(msg) {
    if (!toast) return;
    toast.textContent = msg;
    toast.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { toast.hidden = true; }, 2400);
  }
  document.body.addEventListener("workoutLocked", function () {
    showToast("Workout finished — reopen to edit");
  });
})();

// Drag-to-reorder for the settings exercise list. Uses Pointer Events so a
// single code path handles touch (iPhone) and mouse. The dragged row tracks
// the finger via translateY; siblings physically swap in the DOM as the
// row's midpoint crosses theirs, and on release the new order is POSTed.
(function () {
  "use strict";
  const list = document.querySelector(".js-sortable");
  if (!list) return;

  let drag = null; // { row, pointerId, baselineY }

  function rows() {
    return Array.from(list.querySelectorAll(".settings-row"));
  }

  function onDown(e) {
    const handle = e.target.closest(".js-drag-handle");
    if (!handle) return;
    const row = handle.closest(".settings-row");
    if (!row) return;
    e.preventDefault();
    drag = { row: row, pointerId: e.pointerId, baselineY: e.clientY };
    try { handle.setPointerCapture(e.pointerId); } catch (_) {}
    row.classList.add("settings-row--dragging");
  }

  function onMove(e) {
    if (!drag || e.pointerId !== drag.pointerId) return;
    e.preventDefault();
    const row = drag.row;
    let delta = e.clientY - drag.baselineY;
    row.style.transform = "translateY(" + delta + "px)";

    // Walk the natural siblings (those without the active transform) and
    // swap whenever the dragged row's visual midpoint crosses their middle.
    // Re-check after each swap because a single move can cross several rows.
    let swapped = true;
    while (swapped) {
      swapped = false;
      const all = rows();
      const idx = all.indexOf(row);
      const rect = row.getBoundingClientRect();
      const mid = rect.top + rect.height / 2;
      if (idx < all.length - 1) {
        const next = all[idx + 1];
        const nr = next.getBoundingClientRect();
        if (mid > nr.top + nr.height / 2) {
          list.insertBefore(next, row);
          drag.baselineY += nr.height;
          delta = e.clientY - drag.baselineY;
          row.style.transform = "translateY(" + delta + "px)";
          swapped = true;
          continue;
        }
      }
      if (idx > 0) {
        const prev = all[idx - 1];
        const pr = prev.getBoundingClientRect();
        if (mid < pr.top + pr.height / 2) {
          list.insertBefore(row, prev);
          drag.baselineY -= pr.height;
          delta = e.clientY - drag.baselineY;
          row.style.transform = "translateY(" + delta + "px)";
          swapped = true;
        }
      }
    }
  }

  function onUp(e) {
    if (!drag || e.pointerId !== drag.pointerId) return;
    const row = drag.row;
    row.classList.remove("settings-row--dragging");
    row.style.transform = "";
    drag = null;

    const ids = rows().map(function (r) { return r.dataset.id; }).join(",");
    // URLSearchParams (not FormData) so fetch sends application/x-www-form-
    // urlencoded - that's what Go's r.ParseForm reads. FormData would send
    // multipart/form-data and leave r.PostForm empty -> 400 missing order.
    const body = new URLSearchParams();
    body.append("order", ids);
    fetch("/settings/reorder", {
      method: "POST",
      body: body,
      credentials: "same-origin",
    });
  }

  list.addEventListener("pointerdown", onDown);
  list.addEventListener("pointermove", onMove);
  list.addEventListener("pointerup", onUp);
  list.addEventListener("pointercancel", onUp);
})();
