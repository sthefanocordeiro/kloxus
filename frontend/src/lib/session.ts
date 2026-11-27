// Session marker (KLOX-194): the workspace cookie is HttpOnly, so the
// public site cannot sniff it — this marker records that this browser
// holds a session. Public pages (landing, /termos, /privacidade) read it
// to decide whether probing the API is worthwhile: without it they fire
// no session-touching request at all, so a first visit can never mint a
// workspace. The marker is set when a session is confirmed (workspace
// resolved or created) and cleared on logout.
const KEY = "kloxus.session";

export function markSession() {
  try {
    localStorage.setItem(KEY, "1");
  } catch {
    // private-mode storage denial: the marker is an optimization, not state
  }
}

export function clearSession() {
  try {
    localStorage.removeItem(KEY);
  } catch {
    // same as above
  }
}

export function hasSessionMarker() {
  try {
    return localStorage.getItem(KEY) === "1";
  } catch {
    return false;
  }
}
