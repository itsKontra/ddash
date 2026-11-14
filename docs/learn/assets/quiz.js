// Quiz component. Markup:
//   <div class="quiz">
//     <div class="q" data-answer="2" data-why="Explanation shown after answering.">
//       <p>Question?</p>
//       <div class="opts"><button class="opt">A</button><button class="opt">B</button><button class="opt">C</button></div>
//     </div>
//   </div>
//   <p class="score" data-for-quiz></p>
// data-answer is the zero-based index of the right option. First click counts toward the score.
(function () {
  function init() {
    document.querySelectorAll(".quiz").forEach(function (quiz) {
      var qs = quiz.querySelectorAll(".q");
      var score = quiz.parentElement.querySelector(".score");
      var answered = 0, right = 0;
      qs.forEach(function (q) {
        var answer = Number(q.dataset.answer);
        var opts = q.querySelectorAll("button.opt");
        var first = true;
        opts.forEach(function (btn, i) {
          btn.type = "button";
          btn.addEventListener("click", function () {
            if (i === answer) {
              btn.classList.add("right");
              opts.forEach(function (b) { b.disabled = true; });
            } else {
              btn.classList.add("wrong");
            }
            if (first) {
              first = false; answered++; if (i === answer) right++;
              var why = document.createElement("p");
              why.className = "why";
              why.textContent = (i === answer ? "Right. " : "Not quite. ") + (q.dataset.why || "");
              q.appendChild(why);
              if (score) score.textContent = right + " / " + answered + " right on first try (" + qs.length + " questions)";
            }
          });
        });
      });
    });
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init);
  else init();
})();
