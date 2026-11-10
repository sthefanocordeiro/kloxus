// Kloxus UI helpers — no dependencies, everything is served locally.
(function () {
  var prog = document.getElementById("progress");
  if (prog) {
    var pub = prog.getAttribute("data-pub");
    var items = document.querySelectorAll("#steps li");
    var msg = document.getElementById("runmsg");
    var t = setInterval(function () {
      fetch("/pub/" + pub + "/run/status").then(function (r) { return r.json(); }).then(function (s) {
        var seen = false;
        items.forEach(function (li) {
          li.classList.remove("active");
          if (!seen && li.getAttribute("data-step") === s.step && !s.done) {
            li.classList.add("active"); seen = true;
          } else if (!seen && !s.done) { /* pending */ }
        });
        if (s.done) {
          clearInterval(t);
          items.forEach(function (li) { li.classList.add("done"); });
          if (s.error) {
            msg.textContent = "Error: " + s.error;
          } else if (s.issue_id) {
            msg.innerHTML = 'Done — <a href="/issue/' + s.issue_id + '">review the story list</a>';
          } else {
            msg.textContent = "Ready.";
          }
        } else {
          msg.textContent = "Running: " + s.step + "…";
        }
      }).catch(function () {});
    }, 800);
  }
})();
