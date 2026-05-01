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
    fill:  document.getElementById("rest-timer-fill"),
    close: document.getElementById("rest-timer-close"),
  };
  if (!el.box) return;

  let endsAt = 0;
  let raf = 0;

  function fmt(sec) {
    sec = Math.max(0, Math.ceil(sec));
    const m = Math.floor(sec / 60);
    const s = sec % 60;
    return m + ":" + (s < 10 ? "0" + s : s);
  }

  function tick() {
    const remaining = (endsAt - Date.now()) / 1000;
    el.time.textContent = fmt(remaining);
    const pct = Math.max(0, Math.min(100, (remaining / REST_SECONDS) * 100));
    el.fill.style.width = pct + "%";
    if (remaining <= 0) {
      el.box.classList.add("rest-timer--done");
      el.box.classList.remove("rest-timer--running");
      cancelAnimationFrame(raf);
      raf = 0;
      return;
    }
    raf = requestAnimationFrame(tick);
  }

  function start() {
    endsAt = Date.now() + REST_SECONDS * 1000;
    el.box.hidden = false;
    el.box.classList.add("rest-timer--running");
    el.box.classList.remove("rest-timer--done");
    if (!raf) raf = requestAnimationFrame(tick);
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
    const fd = new FormData();
    fd.append("order", ids);
    fetch("/settings/reorder", {
      method: "POST",
      body: fd,
      credentials: "same-origin",
    });
  }

  list.addEventListener("pointerdown", onDown);
  list.addEventListener("pointermove", onMove);
  list.addEventListener("pointerup", onUp);
  list.addEventListener("pointercancel", onUp);
})();
