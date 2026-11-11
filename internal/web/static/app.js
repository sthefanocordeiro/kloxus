// Kloxus UI helpers — no dependencies, everything is served locally.
(function () {
  var prog = document.getElementById("progress");
  if (prog) {
    var pub = prog.getAttribute("data-pub");
    var items = document.querySelectorAll("#steps li");
    var msg = document.getElementById("runmsg");
    var poll = function () {
      fetch("/pub/" + pub + "/run/status").then(function (r) { return r.json(); }).then(function (s) {
        var seen = false;
        items.forEach(function (li) {
          li.classList.remove("active");
          if (!seen && li.getAttribute("data-step") === s.step && !s.done) {
            li.classList.add("active"); seen = true;
          }
        });
        if (s.done) {
          items.forEach(function (li) { li.classList.add("done"); });
          if (s.error) {
            msg.textContent = "Error: " + s.error;
          } else if (s.no_new_items) {
            msg.textContent = "No new items since the last issue.";
          } else if (s.issue_id) {
            msg.innerHTML = 'Done — <a href="/issue/' + s.issue_id + '">review the story list</a>';
          } else {
            msg.textContent = "Ready.";
          }
          return; // stop polling
        }
        msg.textContent = "Running: " + s.step + "…";
        setTimeout(poll, 800);
      }).catch(function () { setTimeout(poll, 2000); });
    };
    poll();
  }

  // Story-list decisions update the card in place — no reload, no scroll
  // jump. The pending counter is refreshed from the issue page.
  function wireDecisions(scope) {
    (scope || document).querySelectorAll("form.decision-form").forEach(function (f) {
      if (f.dataset.wired) return;
      f.dataset.wired = "1";
      f.addEventListener("submit", function (ev) {
        ev.preventDefault();
        var btn = ev.submitter || f.querySelector("button");
        var data = new FormData(f);
        if (btn && btn.name) data.set(btn.name, btn.value);
        fetch(f.action, { method: "POST", body: data, headers: { "X-Requested-With": "fetch" } })
          .then(function (r) { return r.text(); })
          .then(function (html) {
            var card = f.closest(".card");
            if (card && html.indexOf('class="card"') >= 0) {
              var tmp = document.createElement("div");
              tmp.innerHTML = html;
              var nc = tmp.firstElementChild;
              card.replaceWith(nc);
              wireDecisions(nc);
            }
            var issueURL = f.action.replace(/\/item\/.*$/, "");
            return fetch(issueURL);
          })
          .then(function (r) { return r ? r.text() : ""; })
          .then(function (html) {
            var doc = new DOMParser().parseFromString(html, "text/html");
            var fresh = doc.querySelector("#draft-action");
            var cur = document.querySelector("#draft-action");
            if (fresh && cur) cur.replaceWith(fresh);
          })
          .catch(function () { location.reload(); });
      });
    });
  }
  wireDecisions(document);
})();
