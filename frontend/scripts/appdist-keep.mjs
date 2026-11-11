import { writeFileSync } from "node:fs";

// vite's emptyOutDir wipes the tracked placeholder that keeps
// internal/web/appdist present on clean checkouts (go:embed needs the
// directory to exist even before the frontend is built)
writeFileSync(new URL("../../internal/web/appdist/.keep", import.meta.url), "");
