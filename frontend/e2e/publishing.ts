// KLOX-122: KLOXUS_PUBLISHING defaults off, and the hosted kloxus app runs
// that way. Legacy journeys that exercise send/outbox/readers only make
// sense with the flag on: local runs say so with E2E_PUBLISHING=on; any
// hosted target (E2E_BASE_URL) or E2E_PUBLISHING=off is flag-off.
export const publishingOff =
  process.env.E2E_PUBLISHING === "off" ||
  (!!process.env.E2E_BASE_URL && process.env.E2E_PUBLISHING !== "on");
export const PUBLISHING_SKIP_REASON =
  "legacy send/readers journey — needs KLOXUS_PUBLISHING=on (E2E_PUBLISHING=on)";
