// Ordering drill component. Markup (items in the CORRECT order; the script shuffles them):
//   <div class="order" data-why="Shown when the sequence is complete.">
//     <ol class="order-items"><li>First step</li><li>Second step</li>…</ol>
//   </div>
// The learner clicks steps in the order they happen. A right click locks the step in
// with its position number; a wrong one flashes and counts as a miss.
(function () {
  function shuffle(a) {
    for (var i = a.length - 1; i > 0; i--) {
      var j = Math.floor(Math.random() * (i + 1));
      var t = a[i]; a[i] = a[j]; a[j] = t;
    }
    return a;
  }
  function init() {
    document.querySelectorAll(".order").forEach(function (box) {
      var src = box.querySelector(".order-items");
      var steps = Array.prototype.map.call(src.children, function (li, i) { return { text: li.innerHTML, pos: i }; });
      src.remove();
      var pool = document.createElement("div");
      pool.className = "order-pool";
      var status = document.createElement("p");
      status.className = "score";
      var next = 0, misses = 0;
      var order = shuffle(steps.slice());
      if (order.every(function (s, i) { return s.pos === i; })) order.reverse();
      order.forEach(function (s) {
        var b = document.createElement("button");
        b.type = "button";
        b.className = "order-step";
        b.innerHTML = '<span class="n"></span><span class="t">' + s.text + "</span>";
        b.addEventListener("click", function () {
          if (b.disabled) return;
          if (s.pos === next) {
            b.disabled = true;
            b.classList.add("right");
            b.querySelector(".n").textContent = String(next + 1);
            next++;
            if (next === steps.length) {
              status.textContent = "Complete with " + misses + (misses === 1 ? " miss. " : " misses. ") + (box.dataset.why || "");
            } else {
              status.textContent = next + " of " + steps.length + " placed";
            }
          } else {
            misses++;
            b.classList.remove("wrong");
            void b.offsetWidth;
            b.classList.add("wrong");
            status.textContent = "Not step " + (next + 1) + ". " + next + " of " + steps.length + " placed, " + misses + (misses === 1 ? " miss" : " misses");
          }
        });
        pool.appendChild(b);
      });
      status.textContent = "Click the steps in the order they happen.";
      box.appendChild(pool);
      box.appendChild(status);
    });
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init);
  else init();
})();
